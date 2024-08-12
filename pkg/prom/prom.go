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
	usernameSecret := config.NewInlineSecret(username)
	passwordSecret := config.NewInlineSecret(password)
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

func GetVectorResult(result model.Value) (model.Vector, error) {
	if result.Type() != model.ValVector {
		return nil, fmt.Errorf("not vector type, %s", result.Type())
	}
	v := result.(model.Vector)
	return v, nil
}
