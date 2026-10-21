package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateAIInput(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "run-test")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	currentMeta := &RunMetadata{
		RunID:          "run-test",
		Timestamp:      time.Now(),
		ProductName:    "example-app",
		ProductVersion: "1.0.0",
		TestCase:       "E2E Integration Test",
		DurationMS:     125000,
		Success:        true,
	}

	comparison := &ComparisonResult{
		BaselineRunID:         "run-baseline",
		CurrentRunID:          "run-test",
		BaselineDurationMS:    100000,
		CurrentDurationMS:     125000,
		DurationChangeMS:      25000,
		DurationChangePercent: 25.0,
		HasDurationRegression: true,
		HasRegression:         true,
		BaselineAvailable:     true,
		ProfileDiffs: []*ProfileDiff{
			{
				ProfileType:   "cpu",
				HasRegression: true,
				TotalDelta: SampleDelta{
					BaselineTotal: 1000,
					CurrentTotal:  1250,
					Delta:         250,
					ChangePercent: 25.0,
				},
				TopIncrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/evidence.Collect",
						CumDelta:      200,
						ChangePercent: 40.0,
					},
				},
				TopDecrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/storage.Store",
						CumDelta:      -50,
						ChangePercent: -16.7,
					},
				},
			},
		},
	}

	threshold := 15.0

	if err := generateAIInput(runDir, currentMeta, comparison, threshold, nil, nil); err != nil {
		t.Fatalf("generateAIInput failed: %v", err)
	}

	aiInputPath := filepath.Join(runDir, "ai-input.json")
	if _, err := os.Stat(aiInputPath); os.IsNotExist(err) {
		t.Fatal("ai-input.json was not created")
	}

	data, err := os.ReadFile(aiInputPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) > maxAIInputSizeBytes {
		t.Errorf("AI input too large: %d bytes > %d max", len(data), maxAIInputSizeBytes)
	}

	var aiInput AIInput
	if err := json.Unmarshal(data, &aiInput); err != nil {
		t.Fatalf("Failed to parse AI input: %v", err)
	}

	if aiInput.Metadata.CurrentRunID != "run-test" {
		t.Errorf("Expected current run ID 'run-test', got '%s'", aiInput.Metadata.CurrentRunID)
	}

	if aiInput.Metadata.BaselineRunID != "run-baseline" {
		t.Errorf("Expected baseline run ID 'run-baseline', got '%s'", aiInput.Metadata.BaselineRunID)
	}

	if !aiInput.Comparison.BaselineAvailable {
		t.Error("Expected baseline to be available")
	}

	if aiInput.Comparison.DurationChangePercent != 25.0 {
		t.Errorf("Expected duration change 25.0%%, got %.1f%%", aiInput.Comparison.DurationChangePercent)
	}

	if len(aiInput.ProfileChanges) != 1 {
		t.Errorf("Expected 1 profile change, got %d", len(aiInput.ProfileChanges))
	}

	cpuChange, ok := aiInput.ProfileChanges["cpu"]
	if !ok {
		t.Fatal("Expected cpu profile change")
	}

	if len(cpuChange.TopIncreases) != 1 {
		t.Errorf("Expected 1 top increase, got %d", len(cpuChange.TopIncreases))
	}

	if len(cpuChange.TopDecreases) != 1 {
		t.Errorf("Expected 1 top decrease, got %d", len(cpuChange.TopDecreases))
	}
}

func TestGenerateAIInputNoBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	runDir := filepath.Join(tmpDir, "run-test")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	currentMeta := &RunMetadata{
		RunID:      "run-test",
		Timestamp:  time.Now(),
		DurationMS: 125000,
		Success:    true,
	}

	comparison := &ComparisonResult{
		BaselineAvailable: false,
		CurrentRunID:      "run-test",
	}

	if err := generateAIInput(runDir, currentMeta, comparison, 15.0, nil, nil); err != nil {
		t.Fatalf("generateAIInput failed: %v", err)
	}

	aiInputPath := filepath.Join(runDir, "ai-input.json")
	data, err := os.ReadFile(aiInputPath)
	if err != nil {
		t.Fatal(err)
	}

	var aiInput AIInput
	if err := json.Unmarshal(data, &aiInput); err != nil {
		t.Fatalf("Failed to parse AI input: %v", err)
	}

	if aiInput.Comparison.BaselineAvailable {
		t.Error("Expected baseline to be unavailable")
	}

	if aiInput.Metadata.BaselineRunID != "" {
		t.Error("Expected no baseline run ID")
	}
}

func TestLoadAIAnalysis(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("valid analysis", func(t *testing.T) {
		runDir := filepath.Join(tmpDir, "run-1")
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatal(err)
		}

		analysis := AIAnalysis{
			Verdict:  "regression",
			Headline: "Performance degraded by 25%",
			KeyNumbers: []AIKeyNumber{
				{Label: "Duration", Value: "2m 05s"},
			},
			Confidence: "high",
		}

		data, _ := json.Marshal(analysis)
		if err := os.WriteFile(filepath.Join(runDir, "ai-analysis.json"), data, 0644); err != nil {
			t.Fatal(err)
		}

		loaded, err := loadAIAnalysis(runDir)
		if err != nil {
			t.Fatalf("loadAIAnalysis failed: %v", err)
		}

		if loaded == nil {
			t.Fatal("Expected analysis to be loaded")
		}

		if loaded.Verdict != "regression" {
			t.Errorf("Expected verdict 'regression', got '%s'", loaded.Verdict)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		runDir := filepath.Join(tmpDir, "run-2")
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatal(err)
		}

		loaded, err := loadAIAnalysis(runDir)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if loaded != nil {
			t.Error("Expected nil when file is missing")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		runDir := filepath.Join(tmpDir, "run-3")
		if err := os.MkdirAll(runDir, 0755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(runDir, "ai-analysis.json"), []byte("invalid json"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := loadAIAnalysis(runDir)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})
}
