package recommender

import (
	"context"
	"fmt"
	"time"

	"krr-go/pkg/prom"
	"krr-go/pkg/utils"
)

var (
	logger = utils.GetLogger()
)

type ResourceRecommender struct {
	prom         *prom.Prometheus
	dataDays     int
	minCPU       float64
	minMem       int
	memoryBuffer float64
}

func New(prom *prom.Prometheus, dataDays int,
	minCPU float64, minMem int, memoryBuffer float64,
) *ResourceRecommender {
	return &ResourceRecommender{
		prom:         prom,
		dataDays:     dataDays,
		minCPU:       minCPU,
		minMem:       minMem,
		memoryBuffer: memoryBuffer,
	}
}

func (r *ResourceRecommender) Run(ctx context.Context, namespace string) ([]*WorkloadInfo, error) {
	workloads, err := r.getWorkload(ctx, namespace)
	if err != nil {
		return nil, err
	}
	for _, workload := range workloads {
		err = r.recommend(ctx, workload)
		if err != nil {
			return nil, err
		}

		r.fillSpecResource(ctx, workload)
	}

	var result []*WorkloadInfo
	for _, workload := range workloads {
		result = append(result, workload)
	}

	return result, nil
}

func (r *ResourceRecommender) fillSpecResource(ctx context.Context, workload *WorkloadInfo) {
	for _, container := range workload.Containers {
		req, err := specResource(ctx, r.prom, "kube_pod_container_resource_requests", workload.Namespace, workload.podsSelector(), container.Name)
		if err != nil {
			logger.Errorf("failed to get request for container %s, err: %v", container.Name, err)
		}
		container.SpecRequest = req

		limit, err := specResource(ctx, r.prom, "kube_pod_container_resource_limits", workload.Namespace, workload.podsSelector(), container.Name)
		if err != nil {
			logger.Errorf("failed to get request for container %s, err: %v", container.Name, err)
		}
		container.SpecLimit = limit
	}
	//logger.Infof("%s %s/%s containers %v", workload.Kind, workload.Namespace, workload.Name, workload.Containers)
}

func specResource(ctx context.Context, prometheus *prom.Prometheus, metric, namespace, podSelector, container string) (r Resource, err error) {
	ql := fmt.Sprintf(`%s{namespace="%s", pod=~"%s", container="%s"}`,
		metric, namespace, podSelector, container)
	data, err := prometheus.Query(ctx, ql, time.Now())
	if err != nil {
		return
	}
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return
	}
	for _, sample := range v {
		switch string(sample.Metric["resource"]) {
		case CPUResourceType:
			r.CPU = CPUResource(sample.Value)
		case MemoryResourceType:
			r.Mem = MemoryResource(sample.Value)
		}
	}

	return
}

func (r *ResourceRecommender) recommend(ctx context.Context, workload *WorkloadInfo) error {
	err := r.fillContainers(ctx, workload)
	if err != nil {
		return err
	}
	err = r.containersResourceRecommend(ctx, workload)
	if err != nil {
		return err
	}
	logger.Infof("workload %s %s/%s with containers %v", workload.Kind, workload.Namespace, workload.Name, workload.Containers)

	return nil
}

func (r *ResourceRecommender) containersResourceRecommend(ctx context.Context, workload *WorkloadInfo) error {
	for _, container := range workload.Containers {
		cpuReq, err := percentileCPU(ctx, r.prom,
			0.85, workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, r.dataDays)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s cpu request value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Request.CPU = CPUResource(max(cpuReq, r.minCPU))
		}
		cpuLimit, err := percentileCPU(ctx, r.prom,
			0.99, workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, r.dataDays)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s cpu limit value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Limit.CPU = CPUResource(max(cpuLimit, r.minCPU))
		}

		maxMem, err := maxMemory(ctx, r.prom,
			workload.Namespace, workload.podsSelector(),
			container.Name, DefaultStep, r.dataDays)
		if err != nil {
			logger.Errorf("failed to get %s %s/%s max memory value: %v", workload.Kind, workload.Namespace, workload.Name, err)
		} else {
			container.Request.Mem = MemoryResource(max(maxMem, float64(r.minMem*1024*1024)))
			container.Limit.Mem = MemoryResource(max(maxMem*(1+r.memoryBuffer), float64(r.minMem*1024*1024)))
		}
	}
	return nil
}

func (r *ResourceRecommender) fillContainers(ctx context.Context, workload *WorkloadInfo) error {
	ql := fmt.Sprintf(
		`count(last_over_time(kube_pod_container_info{pod=~"%s",namespace="%s"}[%dd])) by (container)`,
		workload.podsSelector(), workload.Namespace, r.dataDays,
	)
	data, err := r.prom.Query(ctx, ql, time.Now())
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

func (r *ResourceRecommender) getWorkload(ctx context.Context, namespace string) (map[WorkloadKey]*WorkloadInfo, error) {
	rss, err := r.getReplicaSetOwner(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("get deployments: %w", err)
	}

	workloads, err := r.getPodOwner(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("get pod owner: %w", err)
	}

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

	return result, nil
}

func (r *ResourceRecommender) getPodOwner(ctx context.Context, namespace string) (map[WorkloadKey]*WorkloadInfo, error) {
	var ql string
	if namespace != "" {
		ql = fmt.Sprintf(`last_over_time(kube_pod_owner{namespace="%s",}[%dd])`, namespace, r.dataDays)
	} else {
		ql = fmt.Sprintf(`last_over_time(kube_pod_owner{}[%dd])`, r.dataDays)
	}
	data, err := r.prom.Query(ctx, ql, time.Now())
	if err != nil {
		return nil, err
	}
	v, err := prom.GetVectorResult(data)
	if err != nil {
		return nil, err
	}

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

	return workloads, nil
}

// from rs -> deployment
func (r *ResourceRecommender) getReplicaSetOwner(ctx context.Context, namespace string) (map[WorkloadKey]WorkloadKey, error) {
	var ql string
	if namespace != "" {
		ql = fmt.Sprintf(`last_over_time(kube_replicaset_owner{namespace="%s"}[%dd])`, namespace, r.dataDays)
	} else {
		ql = fmt.Sprintf("last_over_time(kube_replicaset_owner{}[%dd])", r.dataDays)
	}
	data, err := r.prom.Query(ctx, ql, time.Now())
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
	ql := fmt.Sprintf(`max_over_time(max(container_memory_rss{namespace="%s",pod=~"%s",container="%s"}) by (container, pod, job)[%dd:%s])`,
		namespace, podsSelector, container, duration, step)

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
