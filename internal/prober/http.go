package prober

import (
	"context"
	"fmt"
	"time"

	"proxy-hub/internal/proxy"
)

type UnsupportedProber struct {
	Scheme string
}

func (p UnsupportedProber) Probe(_ context.Context, _ proxy.Proxy, target TestTarget) proxy.LatencyResult {
	return proxy.LatencyResult{
		OK:       false,
		TestURL:  target.URL,
		TestedAt: time.Now(),
		Error:    fmt.Sprintf("%s proxy probing is not implemented", p.Scheme),
	}
}
