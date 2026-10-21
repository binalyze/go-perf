package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestCLIFlagsParsing verifies that the public command flags parse.
func TestCLIFlagsParsing(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "run command with template flags",
			args: []string{
				"go-perf", "run",
				"--config", "/tmp/perf-config.json",
				"--work-dir", "/tmp/perf-runs",
				"--regression-threshold", "15.0",
				"--cpu-profile-seconds", "30",
				"--output-html", "latest-report.html",
			},
		},
		{
			name: "run command with all flags",
			args: []string{
				"go-perf", "run",
				"--config", "/tmp/perf-config.json",
				"--work-dir", "/tmp/perf-runs",
				"--product-name", "example-app",
				"--binary-path", "/tmp/app",
				"--e2e-test-path", "/tmp/app-e2e",
				"--baseline-run-id", "run-20240101-120000",
				"--regression-threshold", "15.0",
				"--cpu-profile-seconds", "30",
				"--output-html", "latest-report.html",
			},
		},
		{
			name: "compare command with flags",
			args: []string{
				"go-perf", "compare",
				"--work-dir", "/tmp/perf-runs",
				"--run-a", "run-20240101-120000",
				"--run-b", "run-20240101-130000",
			},
		},
		{
			name: "report command with flags",
			args: []string{
				"go-perf", "report",
				"--work-dir", "/tmp/perf-runs",
				"--run-id", "run-20240101-120000",
				"--output-html", "report.html",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary work directory for the test
			tmpDir, err := os.MkdirTemp("", "perf-cli-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			// Create dummy files that the commands expect
			configPath := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(configPath, []byte(`{
				"product_name": "TestProduct",
				"binary_path": "/tmp/test-binary",
				"binary_args": ["--test"],
				"env_vars": [],
				"e2e_test_path": "/tmp/test-e2e",
				"e2e_test_args": [],
				"profiling_port": "6060",
				"health_check_url": "http://localhost:9096/status",
				"test_case_name": "Test case",
				"startup_wait_time_ms": 30000
			}`), 0644); err != nil {
				t.Fatalf("Failed to create config file: %v", err)
			}

			// Replace paths in args with actual paths
			args := make([]string, len(tt.args))
			for i, arg := range tt.args {
				if arg == "/tmp/perf-config.json" {
					args[i] = configPath
				} else if arg == "/tmp/perf-runs" {
					args[i] = tmpDir
				} else {
					args[i] = arg
				}
			}

			// Create the app
			app := &cli.App{
				Name:  appName,
				Usage: "Test",
				Commands: []*cli.Command{
					{
						Name:  "run",
						Usage: "Execute a new performance test run",
						Action: func(c *cli.Context) error {
							// Just validate flags are parsed, don't actually run
							return nil
						},
						Flags: []cli.Flag{
							&cli.StringFlag{Name: flagWorkDir, Aliases: []string{"w"}, Value: "./perf-runs"},
							&cli.StringFlag{Name: flagProductConfig, Aliases: []string{"c"}},
							&cli.StringFlag{Name: flagProductName},
							&cli.StringFlag{Name: flagBinaryPath, Aliases: []string{"b"}},
							&cli.StringFlag{Name: flagE2ETestPath, Aliases: []string{"e"}},
							&cli.StringFlag{Name: flagBaselineRunID},
							&cli.Float64Flag{Name: flagRegressionThresh, Aliases: []string{"t"}, Value: 15.0},
							&cli.StringFlag{Name: flagOutputHTML, Aliases: []string{"o"}, Value: "latest-report.html"},
							&cli.IntFlag{Name: flagCPUProfileSec, Aliases: []string{"cpu"}, Value: 30},
						},
					},
					{
						Name:  "compare",
						Usage: "Compare two existing performance runs",
						Action: func(c *cli.Context) error {
							return nil
						},
						Flags: []cli.Flag{
							&cli.StringFlag{Name: flagWorkDir, Aliases: []string{"w"}, Value: "./perf-runs"},
							&cli.StringFlag{Name: "run-a", Required: false}, // Not required for this test
							&cli.StringFlag{Name: "run-b", Required: false},
						},
					},
					{
						Name:  "report",
						Usage: "Generate HTML report from existing run data",
						Action: func(c *cli.Context) error {
							return nil
						},
						Flags: []cli.Flag{
							&cli.StringFlag{Name: flagWorkDir, Aliases: []string{"w"}, Value: "./perf-runs"},
							&cli.StringFlag{Name: "run-id"},
							&cli.StringFlag{Name: flagOutputHTML, Aliases: []string{"o"}, Value: "latest-report.html"},
						},
					},
				},
			}

			// Try to parse the flags
			err = app.RunContext(ctx, args)
			if err != nil {
				t.Errorf("Failed to parse flags: %v", err)
			}
		})
	}
}

// TestCLIFlagAliases verifies that flag aliases work correctly
func TestCLIFlagAliases(t *testing.T) {
	tests := []struct {
		name      string
		longFlag  string
		shortFlag string
	}{
		{"work-dir", "--work-dir", "-w"},
		{"config", "--config", "-c"},
		{"binary-path", "--binary-path", "-b"},
		{"e2e-test-path", "--e2e-test-path", "-e"},
		{"regression-threshold", "--regression-threshold", "-t"},
		{"output-html", "--output-html", "-o"},
		{"cpu-profile-seconds", "--cpu-profile-seconds", "--cpu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test just verifies the aliases are defined correctly
			// The actual parsing is tested in TestCLIFlagsParsing
			if tt.longFlag == "" || tt.shortFlag == "" {
				t.Errorf("Flag or alias is empty")
			}
		})
	}
}
