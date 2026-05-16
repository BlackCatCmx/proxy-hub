package config

import (
	"errors"
	"os"
	"strings"
)

type Env struct {
	AdminKey          string
	DataDir           string
	Listen            string
	TrustProxyHeaders bool
}

func LoadEnv() (Env, error) {
	env := Env{
		AdminKey:          os.Getenv("ADMIN_KEY"),
		DataDir:           getenv("DATA_DIR", "/data"),
		Listen:            listenAddress(),
		TrustProxyHeaders: os.Getenv("TRUST_PROXY_HEADERS") == "1",
	}
	if env.AdminKey == "" {
		return Env{}, errors.New("ADMIN_KEY is required")
	}
	return env, nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func listenAddress() string {
	if listen := os.Getenv("LISTEN"); listen != "" {
		return listen
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return ":8080"
	}
	if strings.Contains(port, ":") {
		return port
	}
	return ":" + port
}
