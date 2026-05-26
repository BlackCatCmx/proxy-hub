package prober

import (
	"fmt"

	"proxy-hub/internal/proxy"
)

type Registry struct {
	probers map[string]Prober
}

func NewRegistry() *Registry {
	return &Registry{probers: make(map[string]Prober)}
}

func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register("socks5", Socks5Prober{})
	r.Register("http", UnsupportedProber{Scheme: "http"})
	r.Register("https", UnsupportedProber{Scheme: "https"})
	return r
}

func (r *Registry) Register(scheme string, prober Prober) {
	r.probers[proxy.CanonicalScheme(scheme)] = prober
}

func (r *Registry) Get(scheme string) (Prober, error) {
	p, ok := r.probers[proxy.CanonicalScheme(scheme)]
	if !ok {
		return nil, fmt.Errorf("unsupported proxy scheme %q", scheme)
	}
	return p, nil
}
