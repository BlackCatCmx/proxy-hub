package tester

import (
	"context"
	"time"

	"proxy-hub/internal/config"
	"proxy-hub/internal/prober"
	"proxy-hub/internal/proxy"
)

type Tester struct {
	registry *prober.Registry
}

func New(registry *prober.Registry) *Tester {
	return &Tester{registry: registry}
}

func (t *Tester) Latency(ctx context.Context, p proxy.Proxy, settings config.Settings) proxy.LatencyResult {
	target := prober.TestTarget{
		URL:            settings.TestURL,
		ExpectedStatus: settings.ExpectedStatus,
		Timeout:        time.Duration(settings.TimeoutMs) * time.Millisecond,
	}
	probe, err := t.registry.Get(p.Scheme)
	if err != nil {
		return proxy.LatencyResult{
			OK:       false,
			TestURL:  settings.TestURL,
			TestedAt: time.Now(),
			Error:    err.Error(),
		}
	}
	return probe.Probe(ctx, p, target)
}
