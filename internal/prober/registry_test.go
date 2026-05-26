package prober

import "testing"

func TestDefaultRegistryAcceptsSocks5HAlias(t *testing.T) {
	registry := NewDefaultRegistry()
	if _, err := registry.Get("socks5h"); err != nil {
		t.Fatalf("Get(socks5h) error = %v", err)
	}
}
