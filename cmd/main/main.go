package main

import (
	"context"
	"flag"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/common/model"
	"krr-go/pkg/prom"
	"krr-go/pkg/utils"
)

const (
	DataDay         = 7
	DaemonSetKind   = "DaemonSet"
	JobKind         = "Job"
	ReplicaSetKind  = "ReplicaSet"
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
	getWorkload(ctx, *namespace, prometheus)
}

func getWorkload(ctx context.Context, namespace string, prometheus *prom.Prometheus) error {
	deployments, err := getDeployments(ctx, namespace, prometheus)
	if err != nil {
		return fmt.Errorf("get deployments: %w", err)
	}

	for _, deployment := range deployments {
		logger.Info(deployment)
	}

	workloads, err := getPodOwner(ctx, namespace, prometheus)
	if err != nil {
		return fmt.Errorf("get pod owner: %w", err)
	}
	for _, w := range workloads {
		logger.Info(w)
	}

	for _, workload := range workloads {
		if workload.Kind == ReplicaSetKind {

		}
	}

	return nil
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

func getReplicaSetOwner(ctx context.Context, namespace string, prometheus *prom.Prometheus) (map[WorkloadKey]*WorkloadKey, error) {
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

	workloads := make(map[WorkloadKey]*DeploymentsInfo)
	for _, sample := range v {
		key := WorkloadKey{
			Namespace: string(sample.Metric["namespace"]),
			Kind:      string(sample.Metric["owner_kind"]),
			Name:      string(sample.Metric["owner_name"]),
		}
		if _, ok := workloads[key]; !ok {
			workloads[key] = &DeploymentsInfo{
				WorkloadKey: key,
				ReplicaSets: []string{string(sample.Metric["replicaset"])},
			}
		} else {
			workloads[key].ReplicaSets = append(workloads[key].ReplicaSets, string(sample.Metric["replicaset"]))
		}
	}

	return workloads, nil
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
	Pods []string `json:"pods"`
}

func (i WorkloadInfo) String() string {
	return fmt.Sprintf("kind: %s, name: %s, namespace: %s, pods: %v", i.Kind, i.Name, i.Namespace, i.Pods)
}

//type DeploymentsInfo struct {
//	WorkloadKey
//	ReplicaSets []string `json:"replicaSets"`
//}
//
//func (i DeploymentsInfo) String() string {
//	return fmt.Sprintf("kind: %s, name: %s, namespace: %s, replicasets: %v", i.Kind, i.Name, i.Namespace, i.ReplicaSets)
//}

type WorkloadKey struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}
