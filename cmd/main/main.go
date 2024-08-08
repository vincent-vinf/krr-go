package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/prometheus/common/model"
	"krr-go/pkg/prom"
	"krr-go/pkg/utils"
)

const (
	DataDay     = 7
	DefaultStep = time.Minute * 30

	DaemonSetKind   = "DaemonSet"
	JobKind         = "Job"
	ReplicaSetKind  = "ReplicaSet"
	NodeKind        = "Node"
	DeploymentKind  = "Deployment"
	StatefulSetKind = "StatefulSet"
)

var (
	prometheusEndpoint = flag.String("prometheus", "http://10.10.103.133:31277/", "Prometheus endpoint")
	namespace          = flag.String("namespace", "kube-system", "Kubernetes namespace, defaults to all")
	nameMaxWidth       = flag.Int("name-max-width", 32, "Maximum width of the name column")
	duration           = flag.Duration("duration", 0, "Duration of the retention period")
	minMem             = flag.Int("min-memory", 100, "Minimum memory size(MiB)")
	minCPU             = flag.Float64("min-cpu", 0.05, "Minimum CPU cores")
	memoryBuffer       = flag.Float64("memory-buffer", 0.15, "Memory buffer percentage")
	logger             = utils.GetLogger()
)

func init() {
	flag.Parse()
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	prometheus, err := prom.NewPrometheus(*prometheusEndpoint)
	if err != nil {
		logger.Fatal(err)
	}

	err = ResourceRecommend(ctx, prometheus)
	if err != nil {
		logger.Fatal(err)
	}
	if *duration == 0 {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.Tick(*duration):
			logger.Infof("running retention period in %v", time.Now())
			err := ResourceRecommend(ctx, prometheus)
			if err != nil {
				logger.Error(err)
			}
		}
	}
}

func ResourceRecommend(ctx context.Context, prometheus *prom.Prometheus) error {
	workloads, err := getWorkload(ctx, *namespace, prometheus)
	if err != nil {
		return err
	}
	for _, workload := range workloads {
		err := resourceRecommend(ctx, prometheus, workload)
		if err != nil {
			return err
		}
	}

	renderResult(workloads, *nameMaxWidth)
	return nil
}

func renderResult(workloads map[WorkloadKey]*WorkloadInfo, NameWidthMax int) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetColumnConfigs([]table.ColumnConfig{
		//{Name: "Namespace", AutoMerge: true},
		{Name: "Name", WidthMax: NameWidthMax},
		//{Name: "Type", AutoMerge: true},
		//{Name: "Name", Align: text.AlignJustify},
	})
	t.SortBy([]table.SortBy{{
		Name: "Namespace",
	}})
	t.AppendHeader(table.Row{"Namespace", "Name", "Type", "Container", "Req CPU", "ReqMemory", "Limit CPU", "Limit Memory"})
	for _, w := range workloads {
		for i, c := range w.Containers {
			if i == 0 {
				t.AppendRows([]table.Row{{
					w.Namespace, w.Name, w.Kind, c.Name, c.Request.CPU, c.Request.Mem, c.Limit.CPU, c.Limit.Mem,
				}})
			} else {
				t.AppendRows([]table.Row{{
					"", "", "", c.Name, c.Request.CPU, c.Request.Mem, c.Limit.CPU, c.Limit.Mem,
				}})
			}
		}
		t.AppendSeparator()

	}

	//t.AppendFooter(rowFooter)
	t.SetIndexColumn(1)
	t.SetAutoIndex(true)
	t.Render()

	return
}

func resourceRecommend(ctx context.Context, prometheus *prom.Prometheus, workload *WorkloadInfo) error {
	err := getContainers(ctx, prometheus, workload)
	if err != nil {
		return err
	}
	err = containersResourceRecommend(ctx, prometheus, workload)
	if err != nil {
		return err
	}
	logger.Infof("workload %s %s/%s with containers %v", workload.Kind, workload.Namespace, workload.Name, workload.Containers)

	return nil
}

func containersResourceRecommend(ctx context.Context, prometheus *prom.Prometheus, workload *WorkloadInfo) error {
	for _, container := range workload.Containers {
		cpuReq, err := percentileCPU(ctx, prometheus,
			0.85, workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, DataDay)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s cpu request value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Request.CPU = CPUResource(max(cpuReq, *minCPU))
		}
		cpuLimit, err := percentileCPU(ctx, prometheus,
			0.95, workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, DataDay)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s cpu limit value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Limit.CPU = CPUResource(max(cpuLimit, *minCPU))
		}

		maxMem, err := maxMemory(ctx, prometheus,
			workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, DataDay)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s max memory value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Request.Mem = MemoryResource(max(maxMem, float64(*minMem*1024*1024)))
			container.Limit.Mem = MemoryResource(max(maxMem*(1+*memoryBuffer), float64(*minMem*1024*1024)))
		}
	}
	return nil
}

func getContainers(ctx context.Context, prometheus *prom.Prometheus, workload *WorkloadInfo) error {
	ql := fmt.Sprintf(
		`count(last_over_time(kube_pod_container_info{pod=~"%s",namespace="%s"}[%dd])) by (container)`,
		workload.podsSelector(), workload.Namespace, DataDay,
	)
	data, err := prometheus.Query(ctx, ql, time.Now())
	if err != nil {
		return err
	}
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return err
	}
	for _, sample := range v {
		workload.Containers = append(workload.Containers, &Container{
			Name: string(sample.Metric["container"]),
		})
	}

	return nil
}

func getWorkload(ctx context.Context, namespace string, prometheus *prom.Prometheus) (map[WorkloadKey]*WorkloadInfo, error) {
	rss, err := getReplicaSetOwner(ctx, namespace, prometheus)
	if err != nil {
		return nil, fmt.Errorf("get deployments: %w", err)
	}

	//for k, v := range rss {
	//	logger.Infof("rs %s/%s owner: %s", k.Namespace, k.Name, v.Name)
	//}

	workloads, err := getPodOwner(ctx, namespace, prometheus)
	if err != nil {
		return nil, fmt.Errorf("get pod owner: %w", err)
	}
	//for _, w := range workloads {
	//	logger.Info(w)
	//}

	result := make(map[WorkloadKey]*WorkloadInfo)
	for _, workload := range workloads {
		if workload.Kind == ReplicaSetKind {
			deployKey := rss[workload.WorkloadKey]
			if _, ok := result[deployKey]; !ok {
				result[deployKey] = &WorkloadInfo{
					WorkloadKey: deployKey,
					Pods:        workload.Pods,
				}
			} else {
				result[deployKey].Pods = append(result[deployKey].Pods, workload.Pods...)
			}
		} else if workload.Kind == NodeKind {

		} else {
			result[workload.WorkloadKey] = workload
		}
	}

	//for _, workload := range result {
	//	logger.Info(workload)
	//}

	return result, nil
}

func getPodOwner(ctx context.Context, namespace string, prometheus *prom.Prometheus) (map[WorkloadKey]*WorkloadInfo, error) {
	var ql string
	if namespace != "" {
		ql = fmt.Sprintf(`last_over_time(kube_pod_owner{namespace="%s",}[%dd])`, namespace, DataDay)
	} else {
		ql = fmt.Sprintf(`last_over_time(kube_pod_owner{}[%dd])`, DataDay)
	}
	data, err := prometheus.Query(ctx, ql, time.Now())
	if err != nil {
		return nil, err
	}
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return nil, err
	}

	return getWorkloadFromProm(v), nil
}

// from rs -> deployment
func getReplicaSetOwner(ctx context.Context, namespace string, prometheus *prom.Prometheus) (map[WorkloadKey]WorkloadKey, error) {
	var ql string
	if namespace != "" {
		ql = fmt.Sprintf(`last_over_time(kube_replicaset_owner{namespace="%s"}[%dd])`, namespace, DataDay)
	} else {
		ql = fmt.Sprintf("last_over_time(kube_replicaset_owner{}[%dd])", DataDay)
	}
	data, err := prometheus.Query(ctx, ql, time.Now())
	if err != nil {
		return nil, err
	}
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return nil, err
	}

	rss := make(map[WorkloadKey]WorkloadKey)
	for _, sample := range v {
		deployKey := WorkloadKey{
			Namespace: string(sample.Metric["namespace"]),
			Kind:      string(sample.Metric["owner_kind"]),
			Name:      string(sample.Metric["owner_name"]),
		}
		replicaSetKey := WorkloadKey{
			Namespace: string(sample.Metric["namespace"]),
			Kind:      ReplicaSetKind,
			Name:      string(sample.Metric["replicaset"]),
		}
		rss[replicaSetKey] = deployKey
	}

	return rss, nil
}

func getWorkloadFromProm(v model.Vector) map[WorkloadKey]*WorkloadInfo {
	workloads := make(map[WorkloadKey]*WorkloadInfo)
	for _, sample := range v {
		key := WorkloadKey{
			Namespace: string(sample.Metric["namespace"]),
			Kind:      string(sample.Metric["owner_kind"]),
			Name:      string(sample.Metric["owner_name"]),
		}
		if _, ok := workloads[key]; !ok {
			workloads[key] = &WorkloadInfo{
				WorkloadKey: key,
				Pods:        []string{string(sample.Metric["pod"])},
			}
		} else {
			workloads[key].Pods = append(workloads[key].Pods, string(sample.Metric["pod"]))
		}
	}

	return workloads
}

type WorkloadInfo struct {
	WorkloadKey
	Pods       []string     `json:"pods"`
	Containers []*Container `json:"containers"`
}

func (i *WorkloadInfo) String() string {
	return fmt.Sprintf("kind: %s, name: %s, namespace: %s, pods: %v", i.Kind, i.Name, i.Namespace, i.Pods)
}

func (i *WorkloadInfo) podsSelector() string {
	return strings.Join(i.Pods, "|")
}

type Container struct {
	Name    string   `json:"name"`
	Request Resource `json:"request"`
	Limit   Resource `json:"limit"`
}

func (c *Container) String() string {
	return fmt.Sprintf("name: %s, request: %s, limit: %s", c.Name, c.Request, c.Limit)
}

type Resource struct {
	CPU CPUResource    `json:"cpu"`
	Mem MemoryResource `json:"mem"`
}

type CPUResource float64

func (r CPUResource) String() string {
	if r < 1 {
		return fmt.Sprintf("%dm", int(r*1000))
	}
	return fmt.Sprintf("%.1f", float64(r))
}

type MemoryResource float64

func (r MemoryResource) String() string {
	return humanize.Bytes(uint64(r))
}

func (r Resource) String() string {
	return fmt.Sprintf("cpu: %sC, mem: %s", r.CPU, r.Mem)
}

type WorkloadKey struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

// percentileCPU percentile example: 0.85
func percentileCPU(
	ctx context.Context, prometheus *prom.Prometheus, percentile float32,
	namespace, podsSelector, container string,
	step time.Duration, duration int,
) (float64, error) {
	ql := fmt.Sprintf(`quantile_over_time(%.2f,max(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s",container="%s"}[%s])) by (container, pod, job)[%dd:%s])`,
		percentile, namespace, podsSelector, container, step, duration, step)

	data, err := prometheus.Query(ctx, ql, time.Now())
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return 0, fmt.Errorf("error querying workload metrics: %w, ql: %s", err, ql)
	}
	if v.Len() == 0 {
		return 0, fmt.Errorf("empty result, ql: %s", ql)
	}

	return float64(v[0].Value), nil
}

func maxMemory(
	ctx context.Context, prometheus *prom.Prometheus,
	namespace, podsSelector, container string,
	step time.Duration, duration int,
) (float64, error) {
	ql := fmt.Sprintf(`max_over_time(
                max(
                    container_memory_working_set_bytes{
                        namespace="%s",
                        pod=~"%s",
                        container="%s"
                    }
                ) by (container, pod, job)
                [%dd:%s]
            )`, namespace, podsSelector, container, duration, step)

	noSpaces := strings.ReplaceAll(ql, " ", "")
	noTabs := strings.ReplaceAll(noSpaces, "\t", "")
	noNewlines := strings.ReplaceAll(noTabs, "\n", "")
	logger.Info(noNewlines)
	data, err := prometheus.Query(ctx, ql, time.Now())
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return 0, err
	}
	if v.Len() == 0 {
		return 0, fmt.Errorf("empty result")
	}

	return float64(v[0].Value), nil
}
