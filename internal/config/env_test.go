package config

import "testing"

func TestLoadEnvUsesPortWhenListenUnset(t *testing.T) {
	t.Setenv("ADMIN_KEY", "secret")
	t.Setenv("PORT", "18080")
	env, err := LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv() error = %v", err)
	}
	if env.Listen != ":18080" {
		t.Fatalf("Listen = %q, want :18080", env.Listen)
	}
}

func TestLoadEnvPrefersListenOverPort(t *testing.T) {
	t.Setenv("ADMIN_KEY", "secret")
	t.Setenv("LISTEN", "127.0.0.1:18081")
	t.Setenv("PORT", "18080")
	env, err := LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv() error = %v", err)
	}
	if env.Listen != "127.0.0.1:18081" {
		t.Fatalf("Listen = %q, want 127.0.0.1:18081", env.Listen)
	}
}
