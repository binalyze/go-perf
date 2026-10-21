package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

// createSyntheticProfiles creates a realistic profiles.zip with pprof data
// multiplier adjusts the sample values (use 1.0 for baseline, 1.3 for current to show regression)
func createSyntheticProfilesZip(t *testing.T, zipPath string, multiplier float64) {
	t.Helper()

	tmpDir := t.TempDir()
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create CPU profile
	fn1 := &profile.Function{ID: 1, Name: "example.com/app/pkg/evidence.Collect"}
	fn2 := &profile.Function{ID: 2, Name: "runtime.mallocgc"}
	loc1 := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fn2}}}

	cpuProf := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{loc1}, Value: []int64{int64(100 * multiplier), int64(100000000 * multiplier)}},
			{Location: []*profile.Location{loc2}, Value: []int64{int64(50 * multiplier), int64(50000000 * multiplier)}},
		},
		Location:      []*profile.Location{loc1, loc2},
		Function:      []*profile.Function{fn1, fn2},
		Period:        10000000,
		DurationNanos: 5000000000,
	}
	writePprofFile(t, filepath.Join(profilesDir, "cpu.pprof"), cpuProf)

	// Create heap profile
	fn3 := &profile.Function{ID: 3, Name: "example.com/app/pkg/storage.Allocate"}
	loc3 := &profile.Location{ID: 3, Address: 0x3000, Line: []profile.Line{{Function: fn3}}}

	heapProf := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
			{Type: "inuse_objects", Unit: "count"},
			{Type: "inuse_space", Unit: "bytes"},
		},
		Sample:   []*profile.Sample{{Location: []*profile.Location{loc3}, Value: []int64{int64(1000 * multiplier), int64(10485760 * multiplier), int64(500 * multiplier), int64(5242880 * multiplier)}}},
		Location: []*profile.Location{loc3},
		Function: []*profile.Function{fn3},
	}
	writePprofFile(t, filepath.Join(profilesDir, "heap.pprof"), heapProf)

	// Create allocs profile
	fn4 := &profile.Function{ID: 4, Name: "example.com/app/pkg/network.Send"}
	loc4 := &profile.Location{ID: 4, Address: 0x4000, Line: []profile.Line{{Function: fn4}}}

	allocsProf := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
		Sample:   []*profile.Sample{{Location: []*profile.Location{loc4}, Value: []int64{int64(10000 * multiplier), int64(104857600 * multiplier)}}},
		Location: []*profile.Location{loc4},
		Function: []*profile.Function{fn4},
	}
	writePprofFile(t, filepath.Join(profilesDir, "allocs.pprof"), allocsProf)

	// Create goroutine profile
	fn5 := &profile.Function{ID: 5, Name: "example.com/app/pkg/worker.(*Pool).Run"}
	loc5 := &profile.Location{ID: 5, Address: 0x5000, Line: []profile.Line{{Function: fn5}}}

	goroutineProf := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "goroutine", Unit: "count"}},
		Sample:     []*profile.Sample{{Location: []*profile.Location{loc5}, Value: []int64{int64(100 * multiplier)}}},
		Location:   []*profile.Location{loc5},
		Function:   []*profile.Function{fn5},
	}
	writePprofFile(t, filepath.Join(profilesDir, "goroutine.pprof"), goroutineProf)

	// Zip it
	if err := zipDirectory(profilesDir, zipPath); err != nil {
		t.Fatal(err)
	}
}

func writePprofFile(t *testing.T, path string, prof *profile.Profile) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := prof.Write(f); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeCommandEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()

	// Create baseline run
	baselineRunID := "run-20261019-185717"
	baselineDir := filepath.Join(tmpDir, baselineRunID)
	if err := os.MkdirAll(baselineDir, 0755); err != nil {
		t.Fatal(err)
	}

	baselineTimestamp := time.Date(2026, 10, 19, 18, 57, 17, 0, time.UTC)
	baselineMetadata := RunMetadata{
		RunID:             baselineRunID,
		Timestamp:         baselineTimestamp,
		ProductName:       "example-app",
		ProductVersion:    "dev",
		TestCase:          "e2e test suite",
		DurationMS:        2463143,
		ProfilesCollected: true,
		Success:           true,
	}

	baselineMetaJSON, _ := json.MarshalIndent(baselineMetadata, "", "  ")
	if err := os.WriteFile(filepath.Join(baselineDir, "metadata.json"), baselineMetaJSON, 0644); err != nil {
		t.Fatal(err)
	}

	// Create baseline profiles.zip with baseline values (multiplier = 1.0)
	baselineZip := filepath.Join(baselineDir, "profiles.zip")
	createSyntheticProfilesZip(t, baselineZip, 1.0)

	// Create current run
	currentRunID := "run-20261019-200242"
	currentDir := filepath.Join(tmpDir, currentRunID)
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatal(err)
	}

	currentTimestamp := time.Date(2026, 10, 19, 20, 2, 42, 0, time.UTC)
	currentMetadata := RunMetadata{
		RunID:             currentRunID,
		Timestamp:         currentTimestamp,
		ProductName:       "example-app",
		ProductVersion:    "dev",
		TestCase:          "e2e test suite",
		DurationMS:        2437182,
		ProfilesCollected: true,
		Success:           true,
	}

	currentMetaJSON, _ := json.MarshalIndent(currentMetadata, "", "  ")
	if err := os.WriteFile(filepath.Join(currentDir, "metadata.json"), currentMetaJSON, 0644); err != nil {
		t.Fatal(err)
	}

	// Create current profiles.zip with increased values (multiplier = 1.3 for 30% regression)
	currentZip := filepath.Join(currentDir, "profiles.zip")
	createSyntheticProfilesZip(t, currentZip, 1.3)

	// Run analyze command
	outputHTML := filepath.Join(tmpDir, "report.html")
	threshold := 15.0
	buildURL := "https://dev.azure.com/test/project/_build/results?buildId=123"

	if err := runAnalyze(currentDir, baselineDir, outputHTML, threshold, buildURL); err != nil {
		t.Fatalf("runAnalyze failed: %v", err)
	}

	// Read generated HTML
	htmlContent, err := os.ReadFile(outputHTML)
	if err != nil {
		t.Fatalf("Failed to read generated HTML: %v", err)
	}

	html := string(htmlContent)

	// Verify baseline duration appears in HTML
	if !strings.Contains(html, "2463143ms") {
		t.Error("HTML should contain baseline duration 2463143ms")
	}

	// Verify current duration appears in HTML
	if !strings.Contains(html, "2437182ms") {
		t.Error("HTML should contain current duration 2437182ms")
	}

	// Verify human-readable durations
	if !strings.Contains(html, "41m 03s") || !strings.Contains(html, "40m 37s") {
		t.Error("HTML should contain human-readable durations (41m 03s baseline, 40m 37s current)")
	}

	// Verify percentage change (should be negative since current < baseline)
	if !strings.Contains(html, "-1.05%") && !strings.Contains(html, "-1.0%") && !strings.Contains(html, "-1.1%") {
		t.Errorf("HTML should contain duration change percentage around -1.05%%, got: (check HTML for actual value)")
	}

	// Verify threshold appears
	if !strings.Contains(html, "15.0%") && !strings.Contains(html, "15%") {
		t.Error("HTML should contain regression threshold 15%")
	}

	// Verify timestamps
	if !strings.Contains(html, "2026-10-19") {
		t.Error("HTML should contain timestamp dates")
	}

	// Check for Performance Comparison section
	if !strings.Contains(html, "Performance Comparison") {
		t.Error("HTML should contain Performance Comparison section")
	}

	// Check for Profile Diffs section (even if diffs fail, section should exist if comparison is available)
	// Note: Profile diffing might fail with dummy data, but we should at least try

	// Verify AI input was generated
	aiInputPath := filepath.Join(currentDir, "ai-input.json")
	if _, err := os.Stat(aiInputPath); err != nil {
		t.Errorf("ai-input.json should be generated: %v", err)
	} else {
		aiInputData, err := os.ReadFile(aiInputPath)
		if err != nil {
			t.Fatalf("Failed to read ai-input.json: %v", err)
		}

		var aiInput AIInput
		if err := json.Unmarshal(aiInputData, &aiInput); err != nil {
			t.Fatalf("Failed to parse ai-input.json: %v", err)
		}

		// Verify AI input has correct values
		if aiInput.Comparison.BaselineDurationMS != 2463143 {
			t.Errorf("AI input baseline duration: got %d, want 2463143", aiInput.Comparison.BaselineDurationMS)
		}

		if aiInput.Comparison.CurrentDurationMS != 2437182 {
			t.Errorf("AI input current duration: got %d, want 2437182", aiInput.Comparison.CurrentDurationMS)
		}

		if aiInput.Comparison.DurationChangeMS != -25961 {
			t.Errorf("AI input duration change: got %d, want -25961", aiInput.Comparison.DurationChangeMS)
		}

		if aiInput.Metadata.RegressionThreshold != 15.0 {
			t.Errorf("AI input threshold: got %.1f, want 15.0", aiInput.Metadata.RegressionThreshold)
		}

		if !aiInput.Comparison.BaselineAvailable {
			t.Error("AI input should show baseline as available")
		}

		// Verify profile_changes is not empty (since we created different baseline vs current profiles)
		if len(aiInput.ProfileChanges) == 0 {
			t.Error("AI input profile_changes should not be empty - baseline and current profiles differ")
		} else {
			t.Logf("AI input profile_changes has %d profile types with diffs", len(aiInput.ProfileChanges))
		}
	}

	// Verify Profile Diffs section appears in HTML when profiles differ
	if !strings.Contains(html, "Profile Diffs") {
		t.Error("HTML should contain Profile Diffs section when baseline and current profiles differ")
	}

	// Verify no profiles_extracted directories remain in run directories
	if _, err := os.Stat(filepath.Join(currentDir, "profiles_extracted")); err == nil {
		t.Error("profiles_extracted should not remain in current run directory")
	}

	if _, err := os.Stat(filepath.Join(baselineDir, "profiles_extracted")); err == nil {
		t.Error("profiles_extracted should not remain in baseline run directory")
	}

	// Verify build URL appears in HTML
	if !strings.Contains(html, buildURL) {
		t.Error("HTML should contain build URL")
	}
}

// runAnalyze is a helper that calls the analyze logic directly
func runAnalyze(runDir, baselineDir, outputHTML string, threshold float64, buildURL string) error {
	// Load current run metadata
	currentMeta, err := loadMetadata(runDir)
	if err != nil {
		return err
	}

	runID := filepath.Base(runDir)

	// Compare with baseline if available
	var comparison *ComparisonResult
	if baselineDir != "" && baselineDir != "none" {
		baselineMeta, err := loadMetadata(baselineDir)
		if err != nil {
			comparison = &ComparisonResult{
				BaselineAvailable: false,
				CurrentRunID:      runID,
			}
		} else {
			baselineID := filepath.Base(baselineDir)

			// Calculate duration comparison - populate ALL fields
			durationChangeMS := currentMeta.DurationMS - baselineMeta.DurationMS
			durationChangePercent := 0.0
			if baselineMeta.DurationMS > 0 {
				durationChangePercent = (float64(durationChangeMS) / float64(baselineMeta.DurationMS)) * 100.0
			}

			comparison = &ComparisonResult{
				BaselineRunID:         baselineID,
				CurrentRunID:          runID,
				BaselineDurationMS:    baselineMeta.DurationMS,
				CurrentDurationMS:     currentMeta.DurationMS,
				DurationChangeMS:      durationChangeMS,
				DurationChangePercent: durationChangePercent,
				HasDurationRegression: durationChangePercent > threshold,
				RegressionThreshold:   threshold,
				BaselineTimestamp:     baselineMeta.Timestamp.Format("2006-01-02 15:04:05"),
				CurrentTimestamp:      currentMeta.Timestamp.Format("2006-01-02 15:04:05"),
				BaselineAvailable:     true,
			}

			// Try to diff profiles (will likely fail with dummy data but that's OK for this test)
			profilesPath := filepath.Join(runDir, "profiles.zip")
			baselineProfilesPath := filepath.Join(baselineDir, "profiles.zip")

			if fileExists(profilesPath) && fileExists(baselineProfilesPath) {
				currentExtractDir := filepath.Join(os.TempDir(), "perf-test-current-"+runID)
				baselineExtractDir := filepath.Join(os.TempDir(), "perf-test-baseline-"+baselineID)

				defer os.RemoveAll(currentExtractDir)
				defer os.RemoveAll(baselineExtractDir)

				if err := extractZip(profilesPath, currentExtractDir); err == nil {
					if err := extractZip(baselineProfilesPath, baselineExtractDir); err == nil {
						diffs, err := DiffProfiles(currentExtractDir, baselineExtractDir, threshold)
						if err == nil {
							comparison.ProfileDiffs = diffs

							for _, diff := range diffs {
								if diff.HasRegression {
									comparison.HasProfileRegression = true
									break
								}
							}
						}
					}
				}
			}

			comparison.HasRegression = comparison.HasDurationRegression || comparison.HasProfileRegression
		}
	} else {
		comparison = &ComparisonResult{
			BaselineAvailable: false,
			CurrentRunID:      runID,
		}
	}

	// Extract insights
	var baselineInsights *ProfileInsights
	if comparison != nil && comparison.BaselineAvailable {
		baselineInsights, _ = ExtractProfileInsights(baselineDir, comparison.BaselineDurationMS, nil)
	}
	insights, _ := ExtractProfileInsights(runDir, currentMeta.DurationMS, baselineInsights)

	// Generate AI input JSON
	if err := generateAIInput(runDir, currentMeta, comparison, threshold, insights, nil); err != nil {
		return err
	}

	// Generate HTML report
	if err := generateReport(runDir, outputHTML, comparison, buildURL, insights); err != nil {
		return err
	}

	return nil
}
