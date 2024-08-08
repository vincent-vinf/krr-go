package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/common/model"
	"krr-go/pkg/prom"
	"krr-go/pkg/utils"
)

const (
	DataDay     = 7
	DefaultStep = time.Minute * 30

	MinCPU       = 0.05
	MiniMem      = 10
	MemoryBuffer = 0.15

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
	workloads, err := getWorkload(ctx, *namespace, prometheus)
	if err != nil {
		logger.Fatal(err)
	}
	_ = workloads
	for _, workload := range workloads {
		err := resourceRecommend(ctx, prometheus, workload)
		if err != nil {
			logger.Fatal(err)
		}
	}

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
			logger.Errorf("failed to get cpu request value: %v", err)
			cpuReq = -1
		}
		cpuLimit, err := percentileCPU(ctx, prometheus,
			0.95, workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, DataDay)
		if err != nil {
			logger.Errorf("failed to get cpu limit value: %v", err)
			cpuLimit = -1
		}
		maxMem, err := maxMemory(ctx, prometheus,
			workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, DataDay)

		container.Request.CPU = max(cpuReq, MinCPU)
		container.Request.Mem = maxMem
		container.Limit.CPU = max(cpuLimit, MinCPU)
		container.Limit.Mem = maxMem * (1 + MemoryBuffer)
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
	CPU float64 `json:"cpu"`
	Mem float64 `json:"mem"`
}

func (r Resource) String() string {
	return fmt.Sprintf("cpu: %f, mem: %f", r.CPU, r.Mem)
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
	ql := fmt.Sprintf(`quantile_over_time(
				%.2f,
				max(
					rate(
						container_cpu_usage_seconds_total{
							namespace="%s",
							pod=~"%s",
							container="%s"
						}[%s]
					)
				) by (container, pod, job)
				[%dd:%s]
			)`, percentile, namespace, podsSelector, container, step, duration, step)

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
