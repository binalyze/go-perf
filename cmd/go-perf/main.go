package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
)

const (
	appName = "go-perf"

	flagWorkDir          = "work-dir"
	flagProductConfig    = "config"
	flagProductName      = "product-name"
	flagBinaryPath       = "binary-path"
	flagE2ETestPath      = "e2e-test-path"
	flagBaselineRunID    = "baseline-run-id"
	flagRegressionThresh = "regression-threshold"
	flagOutputHTML       = "output-html"
	flagCPUProfileSec    = "cpu-profile-seconds"
)

func main() {
	// Print version banner to stderr to avoid polluting JSON output from subcommands
	fmt.Fprintf(os.Stderr, "%s %s\n", appName, versionString())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runApp(ctx); err != nil {
		logger.Errorf("Failed. Error: %v", err)
		os.Exit(1)
	}
}

func runApp(ctx context.Context) error {
	app := &cli.App{
		Name:    appName,
		Usage:   "Compare Go pprof profiles between runs, detect regressions, and generate HTML/AI reports",
		Version: versionString(),
		Commands: []*cli.Command{
			{
				Name:   "run",
				Usage:  "Execute a new performance test run (launches product and e2e test)",
				Action: runCommand,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    flagWorkDir,
						Aliases: []string{"w"},
						Usage:   "Working directory for performance run data (profiles, metadata, reports)",
						Value:   "./perf-runs",
					},
					&cli.StringFlag{
						Name:    flagProductConfig,
						Aliases: []string{"c"},
						Usage:   "Path to product config JSON file (flags override its values)",
					},
					&cli.StringFlag{
						Name:  flagProductName,
						Usage: "Product name shown in reports (overrides config)",
					},
					&cli.StringFlag{
						Name:    flagBinaryPath,
						Aliases: []string{"b"},
						Usage:   "Path to the product binary to test (overrides config)",
					},
					&cli.StringFlag{
						Name:    flagE2ETestPath,
						Aliases: []string{"e"},
						Usage:   "Path to the e2e test binary (overrides config)",
					},
					&cli.StringFlag{
						Name:  flagBaselineRunID,
						Usage: "Baseline run ID to compare against (if empty, compares with previous run)",
					},
					&cli.Float64Flag{
						Name:    flagRegressionThresh,
						Aliases: []string{"t"},
						Usage:   "Regression threshold percentage (e.g., 10.0 for 10% increase)",
						Value:   15.0,
					},
					&cli.StringFlag{
						Name:    flagOutputHTML,
						Aliases: []string{"o"},
						Usage:   "Output HTML report path (relative to work-dir)",
						Value:   "latest-report.html",
					},
					&cli.IntFlag{
						Name:    flagCPUProfileSec,
						Aliases: []string{"cpu"},
						Usage:   "Duration in seconds for CPU profile capture",
						Value:   30,
					},
				},
			},
			{
				Name:  "collect",
				Usage: "Collect profiling data from a running process",
				Subcommands: []*cli.Command{
					{
						Name:   "start",
						Usage:  "Start continuous profiling capture",
						Action: collectStartCommand,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "port",
								Usage:    "Profiling server port",
								Value:    "6060",
								Required: false,
							},
						},
					},
					{
						Name:   "stop",
						Usage:  "Stop profiling and collect all profiles",
						Action: collectStopCommand,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "port",
								Usage:    "Profiling server port",
								Value:    "6060",
								Required: false,
							},
							&cli.StringFlag{
								Name:     "output-dir",
								Usage:    "Output directory for collected profiles",
								Required: true,
							},
							&cli.TimestampFlag{
								Name:   "start-time",
								Usage:  "When profiling capture started (for duration calculation)",
								Layout: time.RFC3339,
							},
						},
					},
				},
			},
			{
				Name:   "analyze",
				Usage:  "Analyze performance run and generate report",
				Action: analyzeCommand,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "run-dir",
						Usage:    "Directory containing the current run data (with metadata.json, profiles.zip)",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "baseline-dir",
						Usage: "Single previous run directory (deprecated; prefer --history-dir). 'none' skips it",
						Value: "none",
					},
					&cli.StringFlag{
						Name:  "history-dir",
						Usage: "Directory of past runs (one subdirectory each) or one run directory. 'none' skips history",
						Value: "none",
					},
					&cli.IntFlag{
						Name:  "history-size",
						Usage: "How many past runs to include, newest first",
						Value: defaultHistorySize,
					},
					&cli.StringFlag{
						Name:  "output-html",
						Usage: "Output HTML report path",
						Value: "report.html",
					},
					&cli.Float64Flag{
						Name:  "threshold",
						Usage: "Regression threshold percentage",
						Value: 15.0,
					},
					&cli.StringFlag{
						Name:  "build-url",
						Usage: "URL linked from the report (optional)",
						Value: "",
					},
					&cli.StringFlag{
						Name:  "baseline-build-id",
						Usage: "Baseline build ID for display in report (optional)",
						Value: "",
					},
					&cli.StringFlag{
						Name:  "baseline-agent",
						Usage: "Baseline agent name for display in report (optional)",
						Value: "",
					},
				},
			},
			{
				Name:   "profile-stats",
				Usage:  "Extract statistics from a CPU profile",
				Action: profileStatsCommand,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cpu",
						Usage:    "Path to CPU profile (cpu.pprof)",
						Required: true,
					},
					&cli.Int64Flag{
						Name:  "duration-ms",
						Usage: "Fallback duration in milliseconds if profile doesn't have DurationNanos",
						Value: 0,
					},
				},
			},
			{
				Name:   "compare",
				Usage:  "Compare two existing performance runs",
				Action: compareCommand,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    flagWorkDir,
						Aliases: []string{"w"},
						Usage:   "Working directory containing performance run data",
						Value:   "./perf-runs",
					},
					&cli.StringFlag{
						Name:     "run-a",
						Usage:    "First run ID to compare",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "run-b",
						Usage:    "Second run ID to compare",
						Required: true,
					},
				},
			},
			{
				Name:   "report",
				Usage:  "Generate HTML report from existing run data",
				Action: reportCommand,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    flagWorkDir,
						Aliases: []string{"w"},
						Usage:   "Working directory containing performance run data",
						Value:   "./perf-runs",
					},
					&cli.StringFlag{
						Name:  "run-id",
						Usage: "Run ID to generate report for (latest if empty)",
					},
					&cli.StringFlag{
						Name:    flagOutputHTML,
						Aliases: []string{"o"},
						Usage:   "Output HTML report path (relative to work-dir)",
						Value:   "latest-report.html",
					},
				},
			},
		},
	}

	return app.RunContext(ctx, os.Args)
}
