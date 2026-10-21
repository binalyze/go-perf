package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProductConfig(t *testing.T) {
	t.Run("empty path returns defaults", func(t *testing.T) {
		cfg, err := LoadProductConfig("")
		if err != nil {
			t.Fatalf("LoadProductConfig(\"\"): %v", err)
		}
		if cfg.ProfilingPort != "6060" {
			t.Errorf("ProfilingPort = %q, want 6060", cfg.ProfilingPort)
		}
	})

	t.Run("missing file is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.json")
		if _, err := LoadProductConfig(path); err == nil {
			t.Fatalf("LoadProductConfig(%q) succeeded, want error", path)
		}
	})

	t.Run("file values override defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"product_name": "my-service", "e2e_test_path": "/tmp/e2e"}`
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		cfg, err := LoadProductConfig(path)
		if err != nil {
			t.Fatalf("LoadProductConfig(%q): %v", path, err)
		}
		if cfg.ProductName != "my-service" || cfg.E2ETestPath != "/tmp/e2e" {
			t.Errorf("got product=%q e2e=%q, want my-service and /tmp/e2e", cfg.ProductName, cfg.E2ETestPath)
		}
		if cfg.ProfilingPort != "6060" {
			t.Errorf("ProfilingPort = %q, want default 6060", cfg.ProfilingPort)
		}
	})
}

func TestProductConfigValidate(t *testing.T) {
	valid := ProductConfig{
		E2ETestPath:         "/tmp/e2e",
		HealthCheckURL:      "http://localhost:8080/healthz",
		ProfilingPort:       "6060",
		TestLaunchesProduct: true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on valid config: %v", err)
	}

	harnessLaunches := valid
	harnessLaunches.TestLaunchesProduct = false
	err := harnessLaunches.Validate()
	if err == nil || !strings.Contains(err.Error(), "binary_path") {
		t.Fatalf("Validate() = %v, want binary_path error when the harness launches the product", err)
	}

	if err := (&ProductConfig{}).Validate(); err == nil {
		t.Fatal("Validate() on empty config succeeded, want error")
	}
}
