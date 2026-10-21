package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

// envRunDir tells the product and the e2e test where the current run's
// artifacts (profiles.zip, test-timing.json, logs) belong.
const envRunDir = "PERF_RUN_DIR"

type RunMetadata struct {
	RunID              string            `json:"run_id"`
	Timestamp          time.Time         `json:"timestamp"`
	ProductName        string            `json:"product_name"`
	ProductVersion     string            `json:"product_version"`
	ProductCommitID    string            `json:"product_commit_id"`
	TestCase           string            `json:"test_case"`
	TestCount          int64             `json:"test_count,omitempty"`
	TestPassCount      int64             `json:"test_pass_count,omitempty"`
	DurationMS         int64             `json:"duration_ms"`
	ProfilesCollected  bool              `json:"profiles_collected"`
	MemoryStats        map[string]uint64 `json:"memory_stats,omitempty"`
	IndexingStats      map[string]int64  `json:"indexing_stats,omitempty"`
	CPUProfileDuration int               `json:"cpu_profile_duration"`
	Success            bool              `json:"success"`
	ErrorMessage       string            `json:"error_message,omitempty"`
	AgentName          string            `json:"agent_name,omitempty"`
	CPUModel           string            `json:"cpu_model,omitempty"`
	CPUCores           int               `json:"cpu_cores,omitempty"`
}

func runCommand(c *cli.Context) error {
	workDir := c.String(flagWorkDir)
	cpuProfileSec := c.Int(flagCPUProfileSec)

	// Load product configuration
	config, err := LoadProductConfig(c.String(flagProductConfig))
	if err != nil {
		return fmt.Errorf("failed to load product config: %w", err)
	}

	// Allow command-line overrides
	if c.IsSet(flagProductName) {
		config.ProductName = c.String(flagProductName)
	}
	if c.IsSet(flagBinaryPath) {
		config.BinaryPath = c.String(flagBinaryPath)
	}
	if c.IsSet(flagE2ETestPath) {
		config.E2ETestPath = c.String(flagE2ETestPath)
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid product config: %w", err)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}

	runID := fmt.Sprintf("run-%s", time.Now().Format("20060102-150405"))
	runDir := filepath.Join(workDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	fmt.Printf("================================================================================\n")
	fmt.Printf("PERFORMANCE TEST RUN: %s\n", runID)
	fmt.Printf("================================================================================\n")
	fmt.Printf("Product:               %s\n", config.ProductName)
	fmt.Printf("Test Case:             %s\n", config.TestCaseName)
	fmt.Printf("Work Directory:        %s\n", runDir)
	fmt.Printf("Test Launches Product: %v\n", config.TestLaunchesProduct)
	if !config.TestLaunchesProduct {
		fmt.Printf("Binary Path:           %s\n", config.BinaryPath)
		fmt.Printf("Binary Args:           %v\n", config.BinaryArgs)
	}
	fmt.Printf("E2E Test Path:         %s\n", config.E2ETestPath)
	fmt.Printf("E2E Test Args:         %v\n", config.E2ETestArgs)
	fmt.Printf("Health Check URL:      %s\n", config.HealthCheckURL)
	fmt.Printf("Profiling Port:        %s\n", config.ProfilingPort)
	fmt.Printf("CPU Profile Duration:  %ds\n", cpuProfileSec)
	fmt.Printf("Environment (keys):    %v\n", getEnvKeys(config.EnvVars))
	fmt.Printf("================================================================================\n\n")

	metadata := &RunMetadata{
		RunID:              runID,
		Timestamp:          time.Now(),
		ProductName:        config.ProductName,
		TestCase:           config.TestCaseName,
		CPUProfileDuration: cpuProfileSec,
		Success:            false,
		ErrorMessage:       "Performance run in progress or crashed before completion",
	}

	// Write current-run marker immediately
	currentRunMarker := filepath.Join(workDir, "current-run.txt")
	if err := os.WriteFile(currentRunMarker, []byte(runID), 0644); err != nil {
		fmt.Printf("WARNING: Failed to write current-run marker: %v\n", err)
	}

	// Write initial failed metadata immediately
	if err := saveMetadata(runDir, metadata); err != nil {
		fmt.Printf("WARNING: Failed to write initial metadata: %v\n", err)
	}

	ctx := c.Context
	var productCmd *exec.Cmd
	var testCmd *exec.Cmd
	var testFailure error

	if config.TestLaunchesProduct {
		// Mode: E2E test launches product itself
		fmt.Printf("\n[STEP 1] Running e2e test (test will start product with profiling)...\n")
		fmt.Printf("  Command: %s %v\n", config.E2ETestPath, config.E2ETestArgs)
		fmt.Printf("  Environment will include: %v, %s\n\n", getEnvKeys(config.EnvVars), envRunDir)

		testStart := time.Now()
		testCmd, err = startE2ETestAsync(ctx, config, runDir)
		if err != nil {
			metadata.Success = false
			metadata.ErrorMessage = fmt.Sprintf("Failed to start e2e test: %v", err)
			saveMetadata(runDir, metadata)
			return fmt.Errorf("failed to start e2e test: %w", err)
		}
		defer func() {
			if testCmd != nil && testCmd.Process != nil {
				fmt.Printf("\n[CLEANUP] Terminating e2e test process...\n")
				testCmd.Process.Kill()
				testCmd.Wait()
			}
		}()

		// Wait for product to become ready (test starts it)
		fmt.Printf("[STEP 2] Waiting for product to become ready (health check: %s)...\n", config.HealthCheckURL)
		if err := waitForProductReady(ctx, config); err != nil {
			metadata.Success = false
			metadata.ErrorMessage = fmt.Sprintf("Product failed to become ready: %v", err)
			metadata.DurationMS = time.Since(testStart).Milliseconds()
			saveMetadata(runDir, metadata)
			return fmt.Errorf("product failed to become ready: %w", err)
		}

		fmt.Printf("[STEP 3] Enabling pprof capture...\n")
		if err := enableProfilingCapture(config.ProfilingPort); err != nil {
			fmt.Printf("  WARNING: Failed to enable pprof capture: %v\n", err)
			metadata.ProfilesCollected = false
		} else {
			fmt.Printf("  ✓ Profiling enabled\n")
		}

		fmt.Printf("[STEP 4] Waiting for e2e test to complete...\n")
		testErr := testCmd.Wait()
		metadata.DurationMS = time.Since(testStart).Milliseconds()

		if testErr != nil {
			exitCode := -1
			if exitErr, ok := testErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			metadata.Success = false
			metadata.ErrorMessage = fmt.Sprintf("E2E test failed: exit code %d", exitCode)
			fmt.Printf("  ✗ E2E test failed with exit code: %d\n", exitCode)
			saveMetadata(runDir, metadata)
			testFailure = fmt.Errorf("e2e test failed with exit code %d: %w", exitCode, testErr)
		} else {
			fmt.Printf("  ✓ E2E test completed successfully in %dms\n", metadata.DurationMS)
		}

	} else {
		// Mode: Harness launches product, then runs e2e test
		fmt.Printf("\n[STEP 1] Starting %s with profiling enabled...\n", config.ProductName)
		fmt.Printf("  Command: %s %v\n", config.BinaryPath, config.BinaryArgs)
		fmt.Printf("  Environment: %v\n", getEnvKeys(config.EnvVars))

		productCmd, err = startProductWithProfiling(ctx, config, runDir)
		if err != nil {
			exitCode := getProcessExitCode(err)
			metadata.Success = false
			metadata.ErrorMessage = fmt.Sprintf("Failed to start %s: %v (exit code: %d)", config.ProductName, err, exitCode)
			saveMetadata(runDir, metadata)
			return fmt.Errorf("failed to start %s: %w", config.ProductName, err)
		}
		defer func() {
			if productCmd != nil && productCmd.Process != nil {
				fmt.Printf("\n[CLEANUP] Terminating %s process...\n", config.ProductName)
				productCmd.Process.Kill()
				productCmd.Wait()
			}
		}()

		fmt.Printf("\n[STEP 2] Enabling pprof capture...\n")
		if err := enableProfilingCapture(config.ProfilingPort); err != nil {
			fmt.Printf("  WARNING: Failed to enable pprof capture: %v\n", err)
			metadata.ProfilesCollected = false
		} else {
			fmt.Printf("  ✓ Profiling enabled\n")
		}

		fmt.Printf("\n[STEP 3] Running e2e test...\n")
		testStart := time.Now()
		if err := runE2ETest(ctx, config); err != nil {
			metadata.Success = false
			metadata.ErrorMessage = fmt.Sprintf("E2E test failed: %v", err)
			metadata.DurationMS = time.Since(testStart).Milliseconds()
			saveMetadata(runDir, metadata)
			return fmt.Errorf("e2e test failed: %w", err)
		}
		metadata.DurationMS = time.Since(testStart).Milliseconds()
		fmt.Printf("  ✓ E2E test completed in %dms\n", metadata.DurationMS)
	}

	fmt.Printf("\n[STEP 5] Collecting pprof profiles (CPU profile duration: %ds)...\n", cpuProfileSec)
	if err := collectProfiles(runDir, config.ProfilingPort, cpuProfileSec); err != nil {
		fmt.Printf("  WARNING: Failed to collect profiles: %v\n", err)
		metadata.ProfilesCollected = false
	} else {
		metadata.ProfilesCollected = true
		fmt.Printf("  ✓ Profiles collected\n")
	}

	fmt.Printf("\n[STEP 6] Saving metadata...\n")
	if err := saveMetadata(runDir, metadata); err != nil {
		fmt.Printf("  WARNING: Failed to save metadata: %v\n", err)
	}

	if testFailure != nil {
		return testFailure
	}

	fmt.Printf("\n[STEP 7] Comparing with baseline...\n")
	baselineRunID := c.String(flagBaselineRunID)
	threshold := c.Float64(flagRegressionThresh)
	comparison, err := compareRuns(workDir, baselineRunID, runID, threshold)
	if err != nil {
		fmt.Printf("  WARNING: Failed to compare with baseline: %v\n", err)
	} else if comparison != nil {
		if !comparison.BaselineAvailable {
			fmt.Printf("  No baseline available for comparison\n")
		} else {
			fmt.Printf("  Baseline: %s\n", comparison.BaselineRunID)
			fmt.Printf("  Duration change: %.2f%%\n", comparison.DurationChangePercent)
			fmt.Printf("  Duration regression: %v\n", comparison.HasDurationRegression)
			fmt.Printf("  Profile regression: %v\n", comparison.HasProfileRegression)
		}
	}

	fmt.Printf("\n[STEP 8] Generating HTML report...\n")
	htmlPath := filepath.Join(workDir, c.String(flagOutputHTML))
	if err := generateReport(runDir, htmlPath, comparison, "", nil); err != nil {
		return fmt.Errorf("failed to generate report: %w", err)
	}
	fmt.Printf("  ✓ Report generated: %s\n", htmlPath)

	// Mark run as successful
	metadata.Success = true
	metadata.ErrorMessage = ""

	// Check for regressions but don't fail the run
	hasRegression := false
	if comparison != nil && comparison.BaselineAvailable && comparison.HasRegression {
		if comparison.HasDurationRegression {
			metadata.ErrorMessage = fmt.Sprintf("Performance regression detected: duration increased by %.2f%% (threshold: %.2f%%)",
				comparison.DurationChangePercent, threshold)
			hasRegression = true
		} else if comparison.HasProfileRegression {
			metadata.ErrorMessage = "Performance regression detected: significant increases in profiling data"
			hasRegression = true
		}
	}

	// Save final metadata with success=true
	if err := saveMetadata(runDir, metadata); err != nil {
		fmt.Printf("  WARNING: Failed to save final metadata: %v\n", err)
	}

	fmt.Printf("\n================================================================================\n")
	fmt.Printf("PERFORMANCE RUN COMPLETED SUCCESSFULLY\n")
	fmt.Printf("================================================================================\n")
	fmt.Printf("Run ID:       %s\n", runID)
	fmt.Printf("Duration:     %dms\n", metadata.DurationMS)
	fmt.Printf("Profiles:     %v\n", metadata.ProfilesCollected)
	fmt.Printf("Report:       %s\n", htmlPath)
	if hasRegression {
		fmt.Printf("WARNING:      Performance regression detected\n")
	}
	fmt.Printf("================================================================================\n\n")

	if hasRegression {
		return fmt.Errorf("performance regression detected")
	}

	return nil
}

func startE2ETestAsync(ctx context.Context, config *ProductConfig, runDir string) (*exec.Cmd, error) {
	if _, err := os.Stat(config.E2ETestPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("e2e test binary not found: %s", config.E2ETestPath)
	}

	logFile := filepath.Join(runDir, "e2e-test.log")
	f, err := os.Create(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	cmd := exec.CommandContext(ctx, config.E2ETestPath, config.E2ETestArgs...)
	cmd.Env = append(os.Environ(), config.EnvVars...)
	cmd.Env = append(cmd.Env, envRunDir+"="+runDir)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Dir = filepath.Dir(config.E2ETestPath)

	fmt.Printf("  Starting test from directory: %s\n", cmd.Dir)
	fmt.Printf("  Output will be logged to: %s\n", logFile)

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to start e2e test: %w", err)
	}

	fmt.Printf("  ✓ E2E test process started (PID: %d)\n", cmd.Process.Pid)
	return cmd, nil
}

func waitForProductReady(ctx context.Context, config *ProductConfig) error {
	maxWait := config.StartupWaitTimeMS / 1000
	if maxWait <= 0 {
		maxWait = 60
	}

	fmt.Printf("  Polling %s (timeout: %ds)...\n", config.HealthCheckURL, maxWait)

	for i := 0; i < maxWait; i++ {
		resp, err := http.Get(config.HealthCheckURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Printf("  ✓ Product is ready (after %ds, status: %d)\n", i+1, resp.StatusCode)
				return nil
			}
			fmt.Printf("    Attempt %d/%d: HTTP %d\n", i+1, maxWait, resp.StatusCode)
		} else {
			if i == 0 || i%10 == 0 {
				fmt.Printf("    Attempt %d/%d: %v\n", i+1, maxWait, err)
			}
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("product failed to become ready within %ds", maxWait)
}

func getEnvKeys(envVars []string) []string {
	keys := make([]string, len(envVars))
	for i, env := range envVars {
		parts := strings.SplitN(env, "=", 2)
		keys[i] = parts[0]
	}
	return keys
}

func getProcessExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func startProductWithProfiling(ctx context.Context, config *ProductConfig, runDir string) (*exec.Cmd, error) {
	if _, err := os.Stat(config.BinaryPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s binary not found: %s", config.ProductName, config.BinaryPath)
	}

	logFile := filepath.Join(runDir, "product.log")
	f, err := os.Create(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	cmd := exec.CommandContext(ctx, config.BinaryPath, config.BinaryArgs...)
	cmd.Env = append(os.Environ(), config.EnvVars...)
	cmd.Env = append(cmd.Env, envRunDir+"="+runDir)
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to start %s: %w", config.ProductName, err)
	}

	logger.Infof("Waiting for %s to be ready...", config.ProductName)
	maxWait := config.StartupWaitTimeMS / 1000
	if maxWait <= 0 {
		maxWait = 30
	}
	for i := 0; i < maxWait; i++ {
		resp, err := http.Get(config.HealthCheckURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				logger.Infof("%s is ready", config.ProductName)
				return cmd, nil
			}
		}
		time.Sleep(1 * time.Second)
	}

	return nil, fmt.Errorf("%s failed to become ready within %ds", config.ProductName, maxWait)
}

func enableProfilingCapture(port string) error {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/start", port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func runE2ETest(ctx context.Context, config *ProductConfig) error {
	if _, err := os.Stat(config.E2ETestPath); os.IsNotExist(err) {
		return fmt.Errorf("e2e test binary not found: %s", config.E2ETestPath)
	}

	cmd := exec.CommandContext(ctx, config.E2ETestPath, config.E2ETestArgs...)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("e2e test execution failed: %w", err)
	}

	return nil
}

func collectProfiles(runDir, port string, cpuProfileSec int) error {
	profilesZip := filepath.Join(runDir, "profiles.zip")
	url := fmt.Sprintf("http://127.0.0.1:%s/save?seconds=%d", port, cpuProfileSec)

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	f, err := os.Create(profilesZip)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	logger.Infof("Profiles saved to: %s", profilesZip)
	return nil
}

func saveMetadata(runDir string, metadata *RunMetadata) error {
	metadataPath := filepath.Join(runDir, "metadata.json")
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataPath, data, 0644)
}
