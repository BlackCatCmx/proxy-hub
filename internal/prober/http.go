package prober

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"proxy-hub/internal/proxy"
)

type HTTPProber struct{}

func (HTTPProber) Probe(ctx context.Context, p proxy.Proxy, target TestTarget) proxy.LatencyResult {
	start := time.Now()
	result := proxy.LatencyResult{
		TestURL:  target.URL,
		TestedAt: time.Now(),
	}
	status, _, err := FetchThroughHTTP(ctx, p, target.URL, target.Timeout)
	result.LatencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if target.ExpectedStatus > 0 {
		if status != target.ExpectedStatus {
			result.Error = fmt.Sprintf("unexpected HTTP status %d, expected %d", status, target.ExpectedStatus)
			return result
		}
	} else if status < 200 || status > 299 {
		result.Error = fmt.Sprintf("unexpected HTTP status %d", status)
		return result
	}
	result.OK = true
	return result
}

func FetchThroughHTTP(ctx context.Context, p proxy.Proxy, rawURL string, timeout time.Duration) (int, []byte, error) {
	transport, err := newHTTPTransport(p, timeout)
	if err != nil {
		return 0, nil, err
	}
	defer transport.CloseIdleConnections()
	return fetchWithHTTPTransport(ctx, rawURL, timeout, transport)
}

func newHTTPTransport(p proxy.Proxy, timeout time.Duration) (*http.Transport, error) {
	scheme := proxy.CanonicalScheme(p.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported HTTP proxy scheme %q", p.Scheme)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	proxyURL := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(p.Host, fmt.Sprintf("%d", p.Port)),
	}
	if p.User != "" || p.Pass != "" {
		proxyURL.User = url.UserPassword(p.User, p.Pass)
	}
	dialer := &net.Dialer{Timeout: timeout}
	return &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     false,
	}, nil
}

func fetchWithHTTPTransport(ctx context.Context, rawURL string, timeout time.Duration, transport *http.Transport) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return 0, nil, errors.New("only http and https test URLs are supported")
	}
	if u.Hostname() == "" {
		return 0, nil, errors.New("test URL host is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "proxy-hub/0.1")
	req.Header.Set("Accept", "*/*")
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}
