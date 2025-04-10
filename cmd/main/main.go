package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"krr-go/pkg/prom"
	"krr-go/pkg/recommender"
	"krr-go/pkg/utils"
)

var (
	prometheusEndpoint  = flag.String("prometheus", "http://10.10.103.133:31277/", "Prometheus endpoint")
	prometheusUsername  = flag.String("prometheus-username", "", "Prometheus username")
	prometheusPassword  = flag.String("prometheus-password", "", "Prometheus password")
	namespace           = flag.String("namespace", "", "Kubernetes namespace, defaults to all")
	dataDays            = flag.Int("data-days", 3, "Source Data Day")
	nameMaxWidth        = flag.Int("name-max-width", 32, "Maximum width of the name column")
	duration            = flag.Duration("duration", 0, "Duration of the retention period")
	minMem              = flag.Int("min-memory", 100, "Minimum memory size(MiB)")
	minCPU              = flag.Float64("min-cpu", 0.05, "Minimum CPU cores")
	memoryBuffer        = flag.Float64("memory-buffer", 0.15, "Memory buffer percentage")
	ignoreNoRecommended = flag.Bool("ignore-no-recommended", true, "Recommendations may not be made due to insufficient workload monitoring data, and these values are ignored by default")
	logger              = utils.GetLogger()
)

func init() {
	flag.Parse()
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	var prometheus *prom.Prometheus
	var err error
	if *prometheusUsername != "" {
		prometheus, err = prom.NewPrometheusWithAuth(*prometheusEndpoint, *prometheusUsername, *prometheusPassword)
	} else {
		prometheus, err = prom.NewPrometheus(*prometheusEndpoint)
	}
	if err != nil {
		logger.Fatal(err)
	}
	r := recommender.New(prometheus, *dataDays, *minCPU, *minMem, *memoryBuffer)

	workloads, err := r.Run(ctx, *namespace)
	if err != nil {
		logger.Fatal(err)
	}
	renderResult(workloads, *nameMaxWidth)
	if *duration == 0 {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.Tick(*duration):
			logger.Infof("running retention period in %v", time.Now())
			workloads, err = r.Run(ctx, *namespace)
			if err != nil {
				logger.Error(err)
				continue
			}
			renderResult(workloads, *nameMaxWidth)
		}
	}
}

func renderResult(workloads []*recommender.WorkloadInfo, NameWidthMax int) {
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
	}, {
		Name: "Name",
	}})
	t.AppendHeader(table.Row{"Namespace", "Name", "Type", "Container", "ReqCPU", "ReqMem", "LimCPU", "LimMem"})
	for _, w := range workloads {
		for _, c := range w.Containers {
			t.AppendRows([]table.Row{{
				w.Namespace, w.Name, w.Kind, c.Name,
				renderDiff(c.SpecRequest.CPU, c.Request.CPU),
				renderDiff(c.SpecRequest.Mem, c.Request.Mem),
				renderDiff(c.SpecLimit.CPU, c.Limit.CPU),
				renderDiff(c.SpecLimit.Mem, c.Limit.Mem),
			}})
		}
		t.AppendSeparator()

	}

	t.SetIndexColumn(1)
	t.SetAutoIndex(true)
	t.Render()

	return
}

func renderDiff(spec, recommend resourceRender) string {
	if recommend.IsZero() {
		return recommend.String()
	}
	if spec.IsZero() {
		return fmt.Sprintf("unset->%s", recommend)
	}
	return fmt.Sprintf("%s -> %s", spec, recommend)
}

type resourceRender interface {
	IsZero() bool
	String() string
}
