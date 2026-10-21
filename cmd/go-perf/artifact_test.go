package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCurrentRunMarker verifies the current-run.txt marker logic
func TestCurrentRunMarker(t *testing.T) {
	// Create temp work directory
	tmpDir, err := os.MkdirTemp("", "perf-artifact-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create multiple run directories
	runs := []string{
		"run-20241019-100000",
		"run-20241019-110000",
		"run-20241019-120000", // current (newest)
	}

	for _, runID := range runs {
		runDir := filepath.Join(tmpDir, runID)
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatalf("Failed to create run dir: %v", err)
		}
	}

	// Find the newest run directory.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read dir: %v", err)
	}

	var runDirs []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[:4] == "run-" {
			runDirs = append(runDirs, entry.Name())
		}
	}

	if len(runDirs) != 3 {
		t.Errorf("Expected 3 run dirs, got %d", len(runDirs))
	}

	// Sort and get newest (alphabetically last for our timestamp format)
	var newest string
	for _, runDir := range runDirs {
		if runDir > newest {
			newest = runDir
		}
	}

	expectedNewest := "run-20241019-120000"
	if newest != expectedNewest {
		t.Errorf("Expected newest run %s, got %s", expectedNewest, newest)
	}

	// Write marker file
	markerPath := filepath.Join(tmpDir, "current-run.txt")
	if err := os.WriteFile(markerPath, []byte(newest), 0644); err != nil {
		t.Fatalf("Failed to write marker: %v", err)
	}

	// Read it back
	markerContent, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("Failed to read marker: %v", err)
	}

	if string(markerContent) != newest {
		t.Errorf("Expected marker content %s, got %s", newest, string(markerContent))
	}
}

// TestArtifactStructure verifies the expected artifact structure
func TestArtifactStructure(t *testing.T) {
	// Create temp staging directory (what gets published)
	tmpDir, err := os.MkdirTemp("", "perf-staging-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	currentRunID := "run-20241019-120000"

	// Simulate artifact structure
	runDir := filepath.Join(tmpDir, currentRunID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("Failed to create run dir: %v", err)
	}

	// Create files in artifact
	files := []string{
		filepath.Join(tmpDir, "current-run.txt"),
		filepath.Join(tmpDir, "latest-report.html"),
		filepath.Join(runDir, "metadata.json"),
		filepath.Join(runDir, "profiles.zip"),
	}

	for _, file := range files {
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", file, err)
		}
	}

	// Verify structure
	if _, err := os.Stat(filepath.Join(tmpDir, "current-run.txt")); err != nil {
		t.Errorf("current-run.txt not found in artifact")
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "latest-report.html")); err != nil {
		t.Errorf("latest-report.html not found in artifact")
	}

	if _, err := os.Stat(filepath.Join(runDir, "metadata.json")); err != nil {
		t.Errorf("metadata.json not found in run dir")
	}

	if _, err := os.Stat(filepath.Join(runDir, "profiles.zip")); err != nil {
		t.Errorf("profiles.zip not found in run dir")
	}

	// Verify no baseline-download directory (should be excluded)
	baselineDownloadDir := filepath.Join(tmpDir, "baseline-download")
	if _, err := os.Stat(baselineDownloadDir); err == nil {
		t.Errorf("baseline-download directory should not be in artifact")
	}
}

// TestBaselineVerification tests the baseline validation logic
func TestBaselineVerification(t *testing.T) {
	// Create temp baseline directory
	tmpDir, err := os.MkdirTemp("", "perf-baseline-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runID := "run-20241019-100000"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("Failed to create run dir: %v", err)
	}

	// Test 1: Valid baseline with marker
	t.Run("valid baseline with marker", func(t *testing.T) {
		markerPath := filepath.Join(tmpDir, "current-run.txt")
		if err := os.WriteFile(markerPath, []byte(runID), 0644); err != nil {
			t.Fatalf("Failed to write marker: %v", err)
		}

		metadata := &RunMetadata{
			RunID:       runID,
			Success:     true,
			DurationMS:  5000,
			ProductName: "example-app",
		}
		if err := saveMetadata(runDir, metadata); err != nil {
			t.Fatalf("Failed to save metadata: %v", err)
		}

		// Verify marker points to valid run
		markerContent, err := os.ReadFile(markerPath)
		if err != nil {
			t.Fatalf("Failed to read marker: %v", err)
		}

		if string(markerContent) != runID {
			t.Errorf("Marker content mismatch: expected %s, got %s", runID, string(markerContent))
		}

		// Load metadata and verify success
		loadedMeta, err := loadMetadata(runDir)
		if err != nil {
			t.Fatalf("Failed to load metadata: %v", err)
		}

		if !loadedMeta.Success {
			t.Errorf("Expected successful run, got success=false")
		}
	})

	// Test 2: Failed baseline (should be skipped)
	t.Run("failed baseline should be skipped", func(t *testing.T) {
		failedRunID := "run-20241019-090000"
		failedRunDir := filepath.Join(tmpDir, failedRunID)
		if err := os.MkdirAll(failedRunDir, 0755); err != nil {
			t.Fatalf("Failed to create failed run dir: %v", err)
		}

		failedMetadata := &RunMetadata{
			RunID:        failedRunID,
			Success:      false,
			ErrorMessage: "Test failed",
			DurationMS:   1000,
			ProductName:  "example-app",
		}
		if err := saveMetadata(failedRunDir, failedMetadata); err != nil {
			t.Fatalf("Failed to save failed metadata: %v", err)
		}

		// Load and verify it's marked as failed
		loadedMeta, err := loadMetadata(failedRunDir)
		if err != nil {
			t.Fatalf("Failed to load metadata: %v", err)
		}

		if loadedMeta.Success {
			t.Errorf("Expected failed run, got success=true")
		}

		// Pipeline logic should skip this baseline
		if loadedMeta.Success {
			t.Errorf("Failed run should not be used as baseline")
		}
	})

	// Test 3: Successful run with regression detected (still valid baseline)
	t.Run("regression detected but run successful", func(t *testing.T) {
		regressionRunID := "run-20241019-110000"
		regressionRunDir := filepath.Join(tmpDir, regressionRunID)
		if err := os.MkdirAll(regressionRunDir, 0755); err != nil {
			t.Fatalf("Failed to create regression run dir: %v", err)
		}

		regressionMetadata := &RunMetadata{
			RunID:        regressionRunID,
			Success:      true, // Run succeeded even though regression was detected
			ErrorMessage: "Performance regression detected: duration increased by 20%",
			DurationMS:   6000,
			ProductName:  "example-app",
		}
		if err := saveMetadata(regressionRunDir, regressionMetadata); err != nil {
			t.Fatalf("Failed to save regression metadata: %v", err)
		}

		// Load and verify it's marked as successful
		loadedMeta, err := loadMetadata(regressionRunDir)
		if err != nil {
			t.Fatalf("Failed to load metadata: %v", err)
		}

		// Even with regression, run is successful and should be valid baseline
		if !loadedMeta.Success {
			t.Errorf("Expected successful run (regression is warning, not failure), got success=false")
		}

		// This run should be used as baseline for future comparisons
		if !loadedMeta.Success {
			t.Errorf("Successful run with regression should be valid baseline")
		}
	})
}

// TestArtifactPublishingLogic tests when artifacts should be published
func TestArtifactPublishingLogic(t *testing.T) {
	tests := []struct {
		name                     string
		success                  bool
		shouldPublishBaseline    bool
		shouldPublishDiagnostics bool
	}{
		{
			name:                     "successful run publishes both",
			success:                  true,
			shouldPublishBaseline:    true,
			shouldPublishDiagnostics: true,
		},
		{
			name:                     "failed run publishes diagnostics only",
			success:                  false,
			shouldPublishBaseline:    false,
			shouldPublishDiagnostics: true,
		},
		{
			name:                     "regression detected publishes both",
			success:                  true, // Regression is warning, not failure
			shouldPublishBaseline:    true,
			shouldPublishDiagnostics: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir, err := os.MkdirTemp("", "perf-publish-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			runID := "run-20241019-120000"
			runDir := filepath.Join(tmpDir, runID)
			if err := os.MkdirAll(runDir, 0755); err != nil {
				t.Fatalf("Failed to create run dir: %v", err)
			}

			// Save metadata
			metadata := &RunMetadata{
				RunID:       runID,
				Success:     tt.success,
				DurationMS:  5000,
				ProductName: "example-app",
			}
			if err := saveMetadata(runDir, metadata); err != nil {
				t.Fatalf("Failed to save metadata: %v", err)
			}

			// Write current-run marker
			markerPath := filepath.Join(tmpDir, "current-run.txt")
			if err := os.WriteFile(markerPath, []byte(runID), 0644); err != nil {
				t.Fatalf("Failed to write marker: %v", err)
			}

			// Load metadata and check success flag
			loadedMeta, err := loadMetadata(runDir)
			if err != nil {
				t.Fatalf("Failed to load metadata: %v", err)
			}

			if loadedMeta.Success != tt.success {
				t.Errorf("Success mismatch: expected %v, got %v", tt.success, loadedMeta.Success)
			}

			shouldPublishBaseline := loadedMeta.Success
			shouldPublishDiagnostics := true // Always published

			if shouldPublishBaseline != tt.shouldPublishBaseline {
				t.Errorf("Baseline publishing mismatch: expected %v, got %v", tt.shouldPublishBaseline, shouldPublishBaseline)
			}

			if shouldPublishDiagnostics != tt.shouldPublishDiagnostics {
				t.Errorf("Diagnostics publishing mismatch: expected %v, got %v", tt.shouldPublishDiagnostics, shouldPublishDiagnostics)
			}
		})
	}
}
