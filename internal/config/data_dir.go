package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func ProbeDataDir(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("data dir %q is not writable: create directory failed: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, ".write_probe")
	if err := os.WriteFile(path, []byte("ok\n"), 0o600); err != nil {
		return fmt.Errorf("data dir %q is not writable: write probe failed: %w", dataDir, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("data dir %q is not writable: remove probe failed: %w", dataDir, err)
	}
	return nil
}
