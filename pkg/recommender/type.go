package recommender

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
)

const (
	DefaultStep = time.Minute * 30

	DaemonSetKind   = "DaemonSet"
	JobKind         = "Job"
	ReplicaSetKind  = "ReplicaSet"
	NodeKind        = "Node"
	DeploymentKind  = "Deployment"
	StatefulSetKind = "StatefulSet"

	CPUResourceType    = "cpu"
	MemoryResourceType = "memory"
)

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
	Name        string   `json:"name"`
	Request     Resource `json:"request"`
	Limit       Resource `json:"limit"`
	SpecRequest Resource `json:"specRequest"`
	SpecLimit   Resource `json:"specLimit"`
}

func (c *Container) String() string {
	return fmt.Sprintf("name: %s, request: %s, limit: %s, specReq: %s, specLimit: %s",
		c.Name, c.Request, c.Limit, c.SpecRequest, c.SpecLimit)
}

type Resource struct {
	CPU CPUResource    `json:"cpu"`
	Mem MemoryResource `json:"mem"`
}

type CPUResource float64

func (r CPUResource) IsZero() bool {
	return r == 0
}

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

func (r MemoryResource) IsZero() bool {
	return r == 0
}

func (r Resource) String() string {
	return fmt.Sprintf("cpu: %s, mem: %s", r.CPU, r.Mem)
}

type WorkloadKey struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}
