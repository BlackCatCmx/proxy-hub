package prober

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"proxy-hub/internal/proxy"
)

func TestFetchThroughHTTPProxy(t *testing.T) {
	var gotURL string
	var gotAuth string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotAuth = r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "proxied")
	}))
	t.Cleanup(proxyServer.Close)

	p := proxyFromServerURL(t, "http", proxyServer.URL)
	p.User = "alice"
	p.Pass = "secret"
	status, body, err := FetchThroughHTTP(context.Background(), p, "http://example.test/resource?q=1", time.Second)
	if err != nil {
		t.Fatalf("FetchThroughHTTP() error = %v", err)
	}
	if status != http.StatusCreated || string(body) != "proxied" {
		t.Fatalf("FetchThroughHTTP() = (%d, %q), want (201, %q)", status, body, "proxied")
	}
	if gotURL != "http://example.test/resource?q=1" {
		t.Fatalf("proxy request URL = %q", gotURL)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	if gotAuth != wantAuth {
		t.Fatalf("Proxy-Authorization = %q, want %q", gotAuth, wantAuth)
	}
}

func TestFetchThroughHTTPProxyConnectsToHTTPSOrigin(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secure origin")
	}))
	t.Cleanup(origin.Close)

	connectTarget := make(chan string, 1)
	connectAuth := make(chan string, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		connectTarget <- r.Host
		connectAuth <- r.Header.Get("Proxy-Authorization")
		tunnelHTTPConnect(w, r)
	}))
	t.Cleanup(proxyServer.Close)

	p := proxyFromServerURL(t, "http", proxyServer.URL)
	p.User = "alice"
	p.Pass = "secret"
	transport, err := newHTTPTransport(p, time.Second)
	if err != nil {
		t.Fatalf("newHTTPTransport() error = %v", err)
	}
	transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	status, body, err := fetchWithHTTPTransport(context.Background(), origin.URL, time.Second, transport)
	if err != nil {
		t.Fatalf("fetchWithHTTPTransport() error = %v", err)
	}
	if status != http.StatusOK || string(body) != "secure origin" {
		t.Fatalf("fetchWithHTTPTransport() = (%d, %q)", status, body)
	}
	if got := <-connectTarget; got != strings.TrimPrefix(origin.URL, "https://") {
		t.Fatalf("CONNECT target = %q", got)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	if got := <-connectAuth; got != wantAuth {
		t.Fatalf("CONNECT Proxy-Authorization = %q, want %q", got, wantAuth)
	}
}

func TestFetchThroughHTTPSProxy(t *testing.T) {
	proxyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("proxy request did not use TLS")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(proxyServer.Close)

	p := proxyFromServerURL(t, "https", proxyServer.URL)
	transport, err := newHTTPTransport(p, time.Second)
	if err != nil {
		t.Fatalf("newHTTPTransport() error = %v", err)
	}
	transport.TLSClientConfig = proxyServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	status, _, err := fetchWithHTTPTransport(context.Background(), "http://example.test/", time.Second, transport)
	if err != nil {
		t.Fatalf("fetchWithHTTPTransport() error = %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
}

func proxyFromServerURL(t *testing.T, scheme, rawURL string) proxy.Proxy {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("net.SplitHostPort() error = %v", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse port error = %v", err)
	}
	return proxy.Proxy{Scheme: scheme, Host: host, Port: portNumber}
}

func tunnelHTTPConnect(w http.ResponseWriter, r *http.Request) {
	destination, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		destination.Close()
		http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
		return
	}
	client, rw, err := hijacker.Hijack()
	if err != nil {
		destination.Close()
		return
	}
	_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = rw.Flush()
	go func() {
		defer client.Close()
		defer destination.Close()
		_, _ = io.Copy(destination, bufio.NewReader(client))
	}()
	go func() {
		defer client.Close()
		defer destination.Close()
		_, _ = io.Copy(client, destination)
	}()
}
