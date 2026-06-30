package prober

import "testing"

func TestDefaultRegistryAcceptsSocks5AndSocks5H(t *testing.T) {
	registry := NewDefaultRegistry()
	if _, err := registry.Get("socks5"); err != nil {
		t.Fatalf("Get(socks5) error = %v", err)
	}
	if _, err := registry.Get("socks5h"); err != nil {
		t.Fatalf("Get(socks5h) error = %v", err)
	}
}
