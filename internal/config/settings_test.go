package config

import "testing"

func TestSettingsFillDefaultsUsesPreviousLatencyColorBoundary(t *testing.T) {
	settings := Settings{LowLatencyMs: 150}

	settings.FillDefaults()

	if settings.RedLatencyMs != 301 {
		t.Fatalf("RedLatencyMs = %d, want 301", settings.RedLatencyMs)
	}
}

func TestSettingsValidateRequiresRedLatencyAboveLowLatency(t *testing.T) {
	settings := DefaultSettings()
	settings.LowLatencyMs = 250
	settings.RedLatencyMs = 250

	if err := settings.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}
