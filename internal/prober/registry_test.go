package prober

import "testing"

func TestDefaultRegistryAcceptsSupportedSchemes(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, scheme := range []string{"socks5", "socks5h", "http", "https"} {
		if _, err := registry.Get(scheme); err != nil {
			t.Fatalf("Get(%s) error = %v", scheme, err)
		}
	}
}
