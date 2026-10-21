package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompareRuns_NoBaseline(t *testing.T) {
	// Create temp work directory
	tmpDir, err := os.MkdirTemp("", "perf-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create current run metadata
	currentRunID := "run-20241019-120000"
	currentRunDir := filepath.Join(tmpDir, currentRunID)
	if err := os.MkdirAll(currentRunDir, 0755); err != nil {
		t.Fatalf("Failed to create current run dir: %v", err)
	}

	currentMetadata := &RunMetadata{
		RunID:       currentRunID,
		Timestamp:   time.Now(),
		ProductName: "example-app",
		TestCase:    "Test",
		DurationMS:  5000,
		Success:     true,
	}

	if err := saveMetadata(currentRunDir, currentMetadata); err != nil {
		t.Fatalf("Failed to save current metadata: %v", err)
	}

	// Compare with no baseline
	result, err := compareRuns(tmpDir, "", currentRunID, 15.0)
	if err != nil {
		t.Fatalf("compareRuns failed: %v", err)
	}

	if result.BaselineAvailable {
		t.Errorf("Expected no baseline available, but got baseline")
	}

	if result.CurrentRunID != currentRunID {
		t.Errorf("Expected current run ID %s, got %s", currentRunID, result.CurrentRunID)
	}
}

func TestCompareRuns_WithBaseline(t *testing.T) {
	// Create temp work directory
	tmpDir, err := os.MkdirTemp("", "perf-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create baseline run
	baselineRunID := "run-20241019-110000"
	baselineRunDir := filepath.Join(tmpDir, baselineRunID)
	if err := os.MkdirAll(baselineRunDir, 0755); err != nil {
		t.Fatalf("Failed to create baseline run dir: %v", err)
	}

	baselineMetadata := &RunMetadata{
		RunID:       baselineRunID,
		Timestamp:   time.Now().Add(-1 * time.Hour),
		ProductName: "example-app",
		TestCase:    "Test",
		DurationMS:  4000,
		Success:     true,
	}

	if err := saveMetadata(baselineRunDir, baselineMetadata); err != nil {
		t.Fatalf("Failed to save baseline metadata: %v", err)
	}

	// Create current run
	currentRunID := "run-20241019-120000"
	currentRunDir := filepath.Join(tmpDir, currentRunID)
	if err := os.MkdirAll(currentRunDir, 0755); err != nil {
		t.Fatalf("Failed to create current run dir: %v", err)
	}

	currentMetadata := &RunMetadata{
		RunID:       currentRunID,
		Timestamp:   time.Now(),
		ProductName: "example-app",
		TestCase:    "Test",
		DurationMS:  5000,
		Success:     true,
	}

	if err := saveMetadata(currentRunDir, currentMetadata); err != nil {
		t.Fatalf("Failed to save current metadata: %v", err)
	}

	// Compare
	result, err := compareRuns(tmpDir, baselineRunID, currentRunID, 15.0)
	if err != nil {
		t.Fatalf("compareRuns failed: %v", err)
	}

	if !result.BaselineAvailable {
		t.Errorf("Expected baseline available")
	}

	if result.BaselineRunID != baselineRunID {
		t.Errorf("Expected baseline run ID %s, got %s", baselineRunID, result.BaselineRunID)
	}

	expectedChange := 25.0 // (5000 - 4000) / 4000 * 100
	if result.DurationChangePercent != expectedChange {
		t.Errorf("Expected duration change %.2f%%, got %.2f%%", expectedChange, result.DurationChangePercent)
	}

	if !result.HasDurationRegression {
		t.Errorf("Expected duration regression (25%% > 15%% threshold)")
	}
}

func TestFindPreviousRun(t *testing.T) {
	// Create temp work directory
	tmpDir, err := os.MkdirTemp("", "perf-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create multiple runs
	runs := []string{
		"run-20241019-090000",
		"run-20241019-100000",
		"run-20241019-110000",
		"run-20241019-120000", // current
	}

	for _, runID := range runs {
		if err := os.MkdirAll(filepath.Join(tmpDir, runID), 0755); err != nil {
			t.Fatalf("Failed to create run dir: %v", err)
		}
	}

	// Find previous run for the last one
	currentRunID := "run-20241019-120000"
	previousRunID, err := findPreviousRun(tmpDir, currentRunID)
	if err != nil {
		t.Fatalf("findPreviousRun failed: %v", err)
	}

	expected := "run-20241019-110000"
	if previousRunID != expected {
		t.Errorf("Expected previous run %s, got %s", expected, previousRunID)
	}
}

func TestLoadMetadata(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "perf-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create metadata
	metadata := &RunMetadata{
		RunID:              "run-test",
		Timestamp:          time.Now(),
		ProductName:        "example-app",
		ProductVersion:     "v1.36.0",
		TestCase:           "E01 NTFS disk image open",
		DurationMS:         5000,
		ProfilesCollected:  true,
		CPUProfileDuration: 30,
		Success:            true,
	}

	// Save it
	metadataPath := filepath.Join(tmpDir, "metadata.json")
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}
	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		t.Fatalf("Failed to write metadata: %v", err)
	}

	// Load it back
	loaded, err := loadMetadata(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load metadata: %v", err)
	}

	if loaded.RunID != metadata.RunID {
		t.Errorf("Expected RunID %s, got %s", metadata.RunID, loaded.RunID)
	}

	if loaded.ProductName != metadata.ProductName {
		t.Errorf("Expected ProductName %s, got %s", metadata.ProductName, loaded.ProductName)
	}

	if loaded.DurationMS != metadata.DurationMS {
		t.Errorf("Expected DurationMS %d, got %d", metadata.DurationMS, loaded.DurationMS)
	}
}
