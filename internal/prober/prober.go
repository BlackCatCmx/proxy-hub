package prober

import (
	"context"
	"fmt"
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

func FetchThroughProxy(ctx context.Context, p proxy.Proxy, rawURL string, timeout time.Duration) (int, []byte, string, error) {
	switch proxy.CanonicalScheme(p.Scheme) {
	case "socks5", "socks5h":
		return FetchThroughSOCKS5(ctx, p, rawURL, timeout)
	case "http", "https":
		status, body, err := FetchThroughHTTP(ctx, p, rawURL, timeout)
		return status, body, "", err
	default:
		return 0, nil, "", fmt.Errorf("unsupported proxy scheme %q", p.Scheme)
	}
}
