package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/binalyze/go-perf/profiling"
)

func collectStartCommand(c *cli.Context) error {
	port := c.String("port")

	fmt.Printf("[PROFILING] Starting continuous CPU profiling capture on port %s...\n", port)

	client := &profiling.Client{Addr: "127.0.0.1:" + port}
	if err := client.Start(c.Context); err != nil {
		return fmt.Errorf("failed to start profiling capture on port %s: %w", port, err)
	}

	fmt.Printf("[PROFILING] ✓ CPU profiling started successfully\n")
	fmt.Printf("[PROFILING] Capture will continue until 'collect stop' is called\n")

	return nil
}

func collectStopCommand(c *cli.Context) error {
	port := c.String("port")
	outputDir := c.String("output-dir")
	startTime := c.Timestamp("start-time")

	if outputDir == "" {
		return fmt.Errorf("--output-dir is required")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	fmt.Printf("[PROFILING] Stopping profiling capture and collecting profiles...\n")
	fmt.Printf("[PROFILING] Output directory: %s\n", outputDir)
	if startTime != nil {
		fmt.Printf("[PROFILING] Total capture duration: %ds\n", int(time.Since(*startTime).Seconds()))
	}

	profilesZip := filepath.Join(outputDir, "profiles.zip")
	client := &profiling.Client{Addr: "127.0.0.1:" + port}
	result, err := client.Collect(c.Context, profilesZip)
	if err != nil {
		return fmt.Errorf("failed to collect profiles from port %s: %w", port, err)
	}
	if result.CPUError != "" {
		fmt.Printf("[PROFILING] WARNING: cpu.pprof not collected: %s\n", result.CPUError)
	}

	fmt.Printf("[PROFILING] ✓ Wrote %s (%v, %d bytes)\n", profilesZip, result.Files, result.Bytes)

	return nil
}

// getProfileDir returns a directory containing loose *.pprof files.
// If the directory contains profiles.zip, it extracts it to a temp directory.
// If it already has loose *.pprof files, it returns the directory as-is.
// The caller is responsible for cleaning up temp directories.
func getProfileDir(dir string) (string, bool, error) {
	profilesZip := filepath.Join(dir, "profiles.zip")

	// Check if profiles.zip exists
	if _, err := os.Stat(profilesZip); err == nil {
		// Extract to temp directory
		tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("perf-profiles-extract-%d", time.Now().UnixNano()))
		if err := extractZip(profilesZip, tempDir); err != nil {
			return "", false, fmt.Errorf("failed to extract profiles.zip: %w", err)
		}
		return tempDir, true, nil
	}

	// Check if directory has loose *.pprof files
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, fmt.Errorf("failed to read directory: %w", err)
	}

	hasPprofFiles := false
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".pprof") {
			hasPprofFiles = true
			break
		}
	}

	if !hasPprofFiles {
		return "", false, fmt.Errorf("no profiles.zip or *.pprof files found")
	}

	// Return directory as-is (no cleanup needed)
	return dir, false, nil
}

func analyzeCommand(c *cli.Context) error {
	runDir := c.String("run-dir")
	baselineDir := c.String("baseline-dir")
	outputHTML := c.String("output-html")
	threshold := c.Float64("threshold")
	buildURL := c.String("build-url")
	baselineBuildID := c.String("baseline-build-id")
	baselineAgentFlag := c.String("baseline-agent")
	historyDirFlag := c.String("history-dir")
	historySize := c.Int("history-size")

	if runDir == "" {
		return fmt.Errorf("--run-dir is required")
	}

	fmt.Printf("[ANALYSIS] Analyzing performance run...\n")
	fmt.Printf("[ANALYSIS] Run directory: %s\n", runDir)
	fmt.Printf("[ANALYSIS] Output report: %s\n", outputHTML)
	fmt.Printf("[ANALYSIS] Regression threshold: %.1f%%\n", threshold)
	if buildURL != "" {
		fmt.Printf("[ANALYSIS] Build URL: %s\n", buildURL)
	}

	// Load current run metadata
	currentMeta, err := loadMetadata(runDir)
	if err != nil {
		return fmt.Errorf("failed to load run metadata: %w", err)
	}

	runID := currentMeta.RunID

	historySource := historyDirFlag
	if historySource == "" || historySource == "none" {
		historySource = baselineDir
	}
	historyDirs, err := listHistoryDirs(historySource, historySize)
	if err != nil {
		fmt.Printf("[ANALYSIS] WARNING: Failed to list history: %v\n", err)
	}
	historyDirs = withoutDir(historyDirs, runDir)
	if (baselineDir == "" || baselineDir == "none" || !isRunDir(baselineDir)) && len(historyDirs) > 0 {
		baselineDir = historyDirs[0]
		fmt.Printf("[ANALYSIS] Using newest history run as baseline: %s\n", baselineDir)
	}

	// Compare with baseline if available
	var comparison *ComparisonResult
	if baselineDir != "" && baselineDir != "none" {
		fmt.Printf("[ANALYSIS] Baseline directory: %s\n", baselineDir)

		baselineMeta, err := loadMetadata(baselineDir)
		if err != nil {
			fmt.Printf("[ANALYSIS] WARNING: Failed to load baseline metadata: %v\n", err)
		} else {
			baselineID := baselineMeta.RunID
			if baselineBuildID != "" {
				fmt.Printf("[ANALYSIS] Baseline build: %s (run: %s)\n", baselineBuildID, baselineID)
			} else {
				fmt.Printf("[ANALYSIS] Baseline run: %s\n", baselineID)
			}

			// Calculate duration comparison
			baselineDuration := baselineMeta.DurationMS
			durationChangeMS := currentMeta.DurationMS - baselineDuration
			durationChangePercent := 0.0
			if baselineDuration > 0 {
				durationChangePercent = (float64(durationChangeMS) / float64(baselineDuration)) * 100.0
			}

			// Get agent names for display only (not for gating)
			currentAgent := currentMeta.AgentName
			baselineAgent := baselineMeta.AgentName
			if currentAgent == "" {
				currentAgent = "unknown"
			}

			// Use flag-provided baseline agent if metadata agent is empty/unknown (for old baselines)
			if (baselineAgent == "" || baselineAgent == "unknown") && baselineAgentFlag != "" {
				baselineAgent = baselineAgentFlag
				fmt.Printf("[ANALYSIS] Using baseline agent from flag: %s\n", baselineAgent)
			}
			if baselineAgent == "" {
				baselineAgent = "unknown"
			}

			// Display agents if they differ (informational only, no gating)
			if baselineAgent != currentAgent && baselineAgent != "unknown" && currentAgent != "unknown" {
				fmt.Printf("[ANALYSIS] ℹ Baseline ran on agent '%s', current on '%s'\n", baselineAgent, currentAgent)
			}

			comparison = &ComparisonResult{
				BaselineRunID:         baselineID,
				CurrentRunID:          runID,
				BaselineDurationMS:    baselineDuration,
				CurrentDurationMS:     currentMeta.DurationMS,
				DurationChangeMS:      durationChangeMS,
				DurationChangePercent: durationChangePercent,
				HasDurationRegression: durationChangePercent > threshold,
				RegressionThreshold:   threshold,
				BaselineTimestamp:     baselineMeta.Timestamp.Format("2006-01-02 15:04:05"),
				CurrentTimestamp:      currentMeta.Timestamp.Format("2006-01-02 15:04:05"),
				BaselineAvailable:     true,
				BaselineAgentName:     baselineAgent,
				CurrentAgentName:      currentAgent,
				BaselineCPUCores:      baselineMeta.CPUCores,
				CurrentCPUCores:       currentMeta.CPUCores,
				BaselineBuildID:       baselineBuildID,
			}

			// Get profile directories (handles both profiles.zip and loose *.pprof files)
			fmt.Printf("[ANALYSIS] Preparing profile directories for diffing...\n")
			currentProfileDir, currentNeedsCleanup, err := getProfileDir(runDir)
			if err != nil {
				fmt.Printf("[ANALYSIS] WARNING: Failed to get current profile directory: %v\n", err)
			} else {
				if currentNeedsCleanup {
					defer os.RemoveAll(currentProfileDir)
				}

				baselineProfileDir, baselineNeedsCleanup, err := getProfileDir(baselineDir)
				if err != nil {
					fmt.Printf("[ANALYSIS] WARNING: Failed to get baseline profile directory: %v\n", err)
				} else {
					if baselineNeedsCleanup {
						defer os.RemoveAll(baselineProfileDir)
					}

					fmt.Printf("[ANALYSIS] Diffing profiles...\n")
					fmt.Printf("[ANALYSIS] Current profile dir: %s\n", currentProfileDir)
					fmt.Printf("[ANALYSIS] Baseline profile dir: %s\n", baselineProfileDir)

					diffs, err := DiffProfiles(currentProfileDir, baselineProfileDir, threshold)
					if err != nil {
						fmt.Printf("[ANALYSIS] WARNING: Failed to diff profiles: %v\n", err)
					} else {
						comparison.ProfileDiffs = diffs
						fmt.Printf("[ANALYSIS] Generated %d profile diffs\n", len(diffs))

						// Calculate average CPU cores from CPU profile
						for _, diff := range diffs {
							fmt.Printf("[ANALYSIS]   %s: %d entries, total delta %.2f%%, regression=%v\n",
								diff.ProfileType, len(diff.TopIncrease)+len(diff.TopDecrease),
								diff.TotalDelta.ChangePercent, diff.HasRegression)

							if diff.ProfileType == "cpu" && diff.TotalDelta.Unit == "nanoseconds" {
								// Calculate average cores: CPU nanoseconds / wall time nanoseconds
								if baselineMeta.DurationMS > 0 {
									comparison.BaselineAvgCores = float64(diff.TotalDelta.BaselineTotal) / (float64(baselineMeta.DurationMS) * 1000000.0)
								}
								if currentMeta.DurationMS > 0 {
									comparison.CurrentAvgCores = float64(diff.TotalDelta.CurrentTotal) / (float64(currentMeta.DurationMS) * 1000000.0)
								}
								comparison.AvgCoresDelta = comparison.CurrentAvgCores - comparison.BaselineAvgCores

								fmt.Printf("[ANALYSIS]   CPU average cores: %.2f → %.2f (Δ %.2f)\n",
									comparison.BaselineAvgCores, comparison.CurrentAvgCores, comparison.AvgCoresDelta)
							}
						}

						// Check for profile regression
						// Only duration, CPU (via avg cores), and allocs may gate
						// Heap/goroutine are snapshots with high variance - informational only
						comparison.HasProfileRegression = false
						cpuCoresRegressed := false
						cpuCoresChangePercent := 0.0

						for _, diff := range diffs {
							if diff.ProfileType == "cpu" {
								// For CPU, check average cores regression
								if comparison.BaselineAvgCores > 0 {
									coresDelta := comparison.CurrentAvgCores - comparison.BaselineAvgCores
									cpuCoresChangePercent = (coresDelta / comparison.BaselineAvgCores) * 100.0
									comparison.AvgCoresChangePercent = cpuCoresChangePercent
									if cpuCoresChangePercent > threshold {
										cpuCoresRegressed = true
										comparison.HasProfileRegression = true
										fmt.Printf("[ANALYSIS]   ⚠ CPU cores regression: %.2f%% increase in average cores\n", cpuCoresChangePercent)
									}
								}
							} else if diff.ProfileType == "allocs" && diff.HasRegression {
								// Allocs may gate (stable metric)
								comparison.HasProfileRegression = true
							}
							// Heap and goroutine are snapshots with high variance - informational only, never gate
						}

						// Normalize ProfileDiffs: set HasRegression based on profile type
						for i := range comparison.ProfileDiffs {
							diff := comparison.ProfileDiffs[i]
							if diff.ProfileType == "cpu" {
								// CPU uses cores check
								diff.HasRegression = cpuCoresRegressed
							} else if diff.ProfileType == "heap" || diff.ProfileType == "goroutine" {
								// Heap and goroutine are snapshots - never gate
								diff.HasRegression = false
							}
							// allocs keeps its original HasRegression (from total delta check)
						}
					}
				}
			}

			comparison.HasRegression = comparison.HasDurationRegression || comparison.HasProfileRegression

			// Log results
			fmt.Printf("[ANALYSIS] Duration change: %.2f%% (%dms -> %dms)\n",
				comparison.DurationChangePercent, baselineMeta.DurationMS, currentMeta.DurationMS)
			if comparison.HasDurationRegression {
				fmt.Printf("[ANALYSIS] ⚠ Duration regression detected\n")
			}
			if comparison.HasProfileRegression {
				fmt.Printf("[ANALYSIS] ⚠ Profile regression detected\n")
			}
		}
	} else {
		fmt.Printf("[ANALYSIS] No baseline available for comparison\n")
		comparison = &ComparisonResult{
			BaselineAvailable: false,
			CurrentRunID:      runID,
		}
	}

	// Extract profile insights
	fmt.Printf("[ANALYSIS] Extracting profile insights...\n")
	var baselineInsights *ProfileInsights
	if comparison != nil && comparison.BaselineAvailable {
		// Use baseline duration for profile insights
		baselineInsights, _ = ExtractProfileInsights(baselineDir, comparison.BaselineDurationMS, nil)
	}
	insights, err := ExtractProfileInsights(runDir, currentMeta.DurationMS, baselineInsights)
	if err != nil {
		fmt.Printf("[ANALYSIS] WARNING: Failed to extract insights: %v\n", err)
	} else {
		fmt.Printf("[ANALYSIS] ✓ Insights extracted (CPU: %.2f cores, Heap in-use: %d bytes, Goroutines: %d)\n",
			insights.Headlines.CPUCores, insights.Headlines.HeapInUseBytes, insights.Headlines.GoroutineCount)
	}

	currentSummary, err := writeRunSummary(runDir, currentMeta)
	if err != nil {
		fmt.Printf("[ANALYSIS] WARNING: Failed to write summary: %v\n", err)
	}
	var history *HistoryAnalysis
	if currentSummary != nil && len(historyDirs) > 0 {
		history, err = buildHistory(historyDirs, currentSummary, threshold)
		if err != nil {
			fmt.Printf("[ANALYSIS] WARNING: Failed to analyze history: %v\n", err)
		} else if err := saveHistory(runDir, history); err != nil {
			fmt.Printf("[ANALYSIS] WARNING: Failed to save history: %v\n", err)
		} else {
			fmt.Printf("[ANALYSIS] History: %d runs\n", history.RunCount)
		}
	}

	// Generate AI input JSON
	if err := generateAIInput(runDir, currentMeta, comparison, threshold, insights, history); err != nil {
		fmt.Printf("[ANALYSIS] WARNING: Failed to generate AI input: %v\n", err)
	}

	// Load AI analysis if available
	aiAnalysis, err := loadAIAnalysis(runDir)
	if err != nil {
		fmt.Printf("[ANALYSIS] WARNING: Failed to load AI analysis: %v\n", err)
	}
	if aiAnalysis != nil {
		fmt.Printf("[ANALYSIS] AI analysis loaded: verdict=%s findings=%d\n", aiAnalysis.Verdict, len(aiAnalysis.Findings))
	}

	// Generate HTML report
	fmt.Printf("[ANALYSIS] Generating HTML report...\n")
	if err := generateReport(runDir, outputHTML, comparison, buildURL, insights); err != nil {
		return fmt.Errorf("failed to generate report: %w", err)
	}

	fmt.Printf("[ANALYSIS] ✓ Report generated: %s\n", outputHTML)

	if comparison != nil && comparison.BaselineAvailable {
		if comparison.HasDurationRegression {
			fmt.Printf("[ANALYSIS] ⚠ Duration regression: %.2f%% increase (threshold: %.1f%%)\n",
				comparison.DurationChangePercent, threshold)
		}
		if comparison.HasProfileRegression {
			for _, diff := range comparison.ProfileDiffs {
				if diff.HasRegression {
					if diff.ProfileType == "cpu" && comparison.AvgCoresChangePercent != 0 {
						fmt.Printf("[ANALYSIS] ⚠ CPU avg cores regression: %.2f%% increase\n",
							comparison.AvgCoresChangePercent)
					} else {
						fmt.Printf("[ANALYSIS] ⚠ %s profile regression: %.2f%% increase\n",
							diff.ProfileType, diff.TotalDelta.ChangePercent)
					}
				}
			}
		}
	}

	// Always exit 0 (errors are for actual failures, not regressions)
	return nil
}

func withoutDir(dirs []string, skip string) []string {
	skip = filepath.Clean(skip)
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if filepath.Clean(dir) == skip {
			continue
		}
		out = append(out, dir)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
