package prom

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
)

// StaticSecret implements the SecretReader interface for static strings
type StaticSecret string

func (s StaticSecret) Fetch(ctx context.Context) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (s StaticSecret) Description() string {
	//TODO implement me
	panic("implement me")
}

func (s StaticSecret) Immutable() bool {
	//TODO implement me
	panic("implement me")
}

// Get implements the SecretReader interface, returning the secret
func (s StaticSecret) Get() (string, error) {
	return string(s), nil
}

type Prometheus struct {
	api v1.API
}

func NewPrometheus(endpoint string) (*Prometheus, error) {
	client, err := api.NewClient(api.Config{
		Address: endpoint,
	})
	if err != nil {
		return nil, err
	}

	return &Prometheus{api: v1.NewAPI(client)}, nil
}

func NewPrometheusWithAuth(endpoint string, username, password string) (*Prometheus, error) {
	// 创建带认证信息的 RoundTripper
	usernameSecret := StaticSecret(username)
	passwordSecret := StaticSecret(password)
	rt := config.NewBasicAuthRoundTripper(usernameSecret, passwordSecret, api.DefaultRoundTripper)

	// 使用带认证的 RoundTripper 创建 Prometheus 客户端
	client, err := api.NewClient(api.Config{
		Address:      endpoint,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, err
	}

	return &Prometheus{api: v1.NewAPI(client)}, nil
}

func (p *Prometheus) Query(ctx context.Context, query string, t time.Time) (model.Value, error) {
	result, _, err := p.api.Query(ctx, query, t, v1.WithTimeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Prometheus) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (model.Value, error) {
	result, _, err := p.api.QueryRange(ctx, query, v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	}, v1.WithTimeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetPodCPU return Pod CPU usage(m core * minute) in minute
//func (p *Prometheus) GetPodCPU(ctx context.Context, promql, namespace, name string, t time.Time) (int64, error) {
//	query := fmt.Sprintf(promql, namespace, name)
//	result, err := p.Query(ctx, query, t)
//	if err != nil {
//		return 0, fmt.Errorf("query: %s, err: %s", query, err)
//	}
//
//	f, err := getVectorResult(result)
//	if err != nil {
//		return 0, fmt.Errorf("query: %s, err: %s", query, err)
//	}
//
//	f = f * 1000 / 60
//	return int64(math.Ceil(f)), nil
//}
//
//// GetPodMem return Pod max Memory(MiB) in minute
//func (p *Prometheus) GetPodMem(ctx context.Context, promql, namespace, name string, t time.Time) (int64, error) {
//	query := fmt.Sprintf(promql, namespace, name)
//	result, err := p.Query(ctx, query, t)
//	if err != nil {
//		return 0, fmt.Errorf("query: %s, err: %s", query, err)
//	}
//	f, err := getVectorResult(result)
//	if err != nil {
//		return 0, fmt.Errorf("query: %s, err: %s", query, err)
//	}
//
//	f = f / 1024 / 1024
//
//	return int64(math.Ceil(f)), nil
//}

func GetVectorResult(result model.Value) (model.Vector, error) {
	if result.Type() != model.ValVector {
		return nil, fmt.Errorf("not vector type, %s", result.Type())
	}
	v := result.(model.Vector)
	return v, nil
}

func GetMatrixResultOne(result model.Value) ([]model.SamplePair, error) {
	if result.Type() != model.ValMatrix {
		return nil, fmt.Errorf("not matrix type, %s", result.Type())
	}
	m := result.(model.Matrix)
	if m.Len() != 1 {
		return nil, fmt.Errorf("unexpected matrix len, %d", m.Len())
	}
	return m[0].Values, nil
}
