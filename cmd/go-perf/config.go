package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ProductConfig holds the product-specific settings for a performance run,
// so the same harness can drive any Go service that embeds the profiling
// control server.
type ProductConfig struct {
	ProductName         string   `json:"product_name"`          // Shown in reports, e.g. "my-service"
	BinaryPath          string   `json:"binary_path"`           // Product binary; unused when TestLaunchesProduct is true
	BinaryArgs          []string `json:"binary_args"`           // Arguments to start the binary (e.g., ["serve", "--listen", ":8080"])
	EnvVars             []string `json:"env_vars"`              // Extra env vars for the product and the e2e test (e.g., ["MYAPP_ENABLE_PROFILING=1"])
	E2ETestPath         string   `json:"e2e_test_path"`         // Path to the e2e test binary
	E2ETestArgs         []string `json:"e2e_test_args"`         // Arguments for the e2e test
	ProfilingPort       string   `json:"profiling_port"`        // Profiling control server port (e.g., "6060")
	HealthCheckURL      string   `json:"health_check_url"`      // Readiness endpoint (e.g., "http://localhost:8080/healthz")
	TestCaseName        string   `json:"test_case_name"`        // Human-readable test case description
	StartupWaitTimeMS   int      `json:"startup_wait_time_ms"`  // Time to wait for the product to become ready
	TestLaunchesProduct bool     `json:"test_launches_product"` // If true, the e2e test starts the product; the harness only wraps it
}

// DefaultProductConfig returns the settings used for any field the config
// file and flags leave unset.
func DefaultProductConfig() *ProductConfig {
	return &ProductConfig{
		ProductName:       "app",
		ProfilingPort:     "6060",
		TestCaseName:      "e2e test suite",
		StartupWaitTimeMS: 60000,
	}
}

// LoadProductConfig loads configuration from a JSON file on top of
// DefaultProductConfig. An empty path returns the defaults; a missing or
// invalid file is an error.
func LoadProductConfig(path string) (*ProductConfig, error) {
	config := DefaultProductConfig()
	if path == "" {
		return config, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	return config, nil
}

// Validate checks that the fields required by the selected run mode are set.
func (c *ProductConfig) Validate() error {
	var errs []error
	if c.E2ETestPath == "" {
		errs = append(errs, errors.New("e2e_test_path is required"))
	}
	if c.HealthCheckURL == "" {
		errs = append(errs, errors.New("health_check_url is required"))
	}
	if c.ProfilingPort == "" {
		errs = append(errs, errors.New("profiling_port is required"))
	}
	if !c.TestLaunchesProduct && c.BinaryPath == "" {
		errs = append(errs, errors.New("binary_path is required unless test_launches_product is true"))
	}
	return errors.Join(errs...)
}

// SaveProductConfig saves configuration to a JSON file.
func SaveProductConfig(path string, config *ProductConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
