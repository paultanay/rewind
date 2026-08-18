package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_EnvironmentOverridesNestedSourceURL(t *testing.T) {
	t.Setenv("REWIND_ALERTMANAGER_URL", "http://localhost:20093")
	path := filepath.Join(t.TempDir(), "rewind.yaml")
	if err := os.WriteFile(path, []byte("alertmanager:\n  url: http://localhost:19093\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AlertMgr.URL != "http://localhost:20093" {
		t.Fatalf("alertmanager URL = %q, want environment override", cfg.AlertMgr.URL)
	}
}
