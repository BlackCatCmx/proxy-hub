package config

import (
	"errors"
	"net/url"
)

type Settings struct {
	Concurrency    int    `json:"concurrency"`
	TimeoutMs      int    `json:"timeout_ms"`
	TestURL        string `json:"test_url"`
	ExpectedStatus int    `json:"expected_status"`
	LowLatencyMs   int    `json:"low_latency_ms"`
	RedLatencyMs   int    `json:"red_latency_ms"`
	LogToFile      bool   `json:"log_to_file"`
	LogMaxMB       int    `json:"log_max_mb"`
}

func DefaultSettings() Settings {
	return Settings{
		Concurrency:    20,
		TimeoutMs:      5000,
		TestURL:        "https://cp.cloudflare.com/generate_204",
		ExpectedStatus: 204,
		LowLatencyMs:   100,
		RedLatencyMs:   201,
		LogToFile:      true,
		LogMaxMB:       3,
	}
}

func (s *Settings) FillDefaults() {
	def := DefaultSettings()
	if s.Concurrency == 0 {
		s.Concurrency = def.Concurrency
	}
	if s.TimeoutMs == 0 {
		s.TimeoutMs = def.TimeoutMs
	}
	if s.TestURL == "" {
		s.TestURL = def.TestURL
	}
	if s.LowLatencyMs == 0 {
		s.LowLatencyMs = def.LowLatencyMs
	}
	if s.RedLatencyMs == 0 {
		s.RedLatencyMs = s.LowLatencyMs*2 + 1
	}
	if s.LogMaxMB == 0 {
		s.LogMaxMB = def.LogMaxMB
	}
}

func (s Settings) Validate() error {
	if s.Concurrency < 1 || s.Concurrency > 200 {
		return errors.New("concurrency must be between 1 and 200")
	}
	if s.TimeoutMs < 500 || s.TimeoutMs > 60000 {
		return errors.New("timeout_ms must be between 500 and 60000")
	}
	if s.LowLatencyMs < 1 || s.LowLatencyMs > 60000 {
		return errors.New("low_latency_ms must be between 1 and 60000")
	}
	if s.RedLatencyMs < 2 || s.RedLatencyMs > 120000 {
		return errors.New("red_latency_ms must be between 2 and 120000")
	}
	if s.RedLatencyMs <= s.LowLatencyMs {
		return errors.New("red_latency_ms must be greater than low_latency_ms")
	}
	if s.ExpectedStatus != 0 && (s.ExpectedStatus < 100 || s.ExpectedStatus > 599) {
		return errors.New("expected_status must be 0 or an HTTP status code")
	}
	if s.LogMaxMB < 1 || s.LogMaxMB > 100 {
		return errors.New("log_max_mb must be between 1 and 100")
	}
	u, err := url.Parse(s.TestURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("test_url must be an http or https URL")
	}
	return nil
}
