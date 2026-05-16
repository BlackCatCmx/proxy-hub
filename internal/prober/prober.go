package prober

import (
	"context"
	"time"

	"proxy-hub/internal/proxy"
)

type TestTarget struct {
	URL            string
	ExpectedStatus int
	Timeout        time.Duration
}

type Prober interface {
	Probe(context.Context, proxy.Proxy, TestTarget) proxy.LatencyResult
}
