package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// TestAnalyzeEndToEndWithBaseline tests analyze with baseline comparison using real profile fixtures
func TestAnalyzeEndToEndWithBaseline(t *testing.T) {
	// Create temporary directories for baseline and current runs
	tmpBase := t.TempDir()
	tmpCur := t.TempDir()

	// Generate baseline profile (lighter load)
	baselineDir := filepath.Join(tmpBase, "baseline-run")
	if err := os.MkdirAll(baselineDir, 0755); err != nil {
		t.Fatalf("Failed to create baseline dir: %v", err)
	}
	baselineCPU := filepath.Join(baselineDir, "cpu.pb.gz")
	baselineMetadata := filepath.Join(baselineDir, "metadata.json")
	if err := generateTestProfile(baselineCPU, 10); err != nil {
		t.Fatalf("Failed to generate baseline profile: %v", err)
	}
	if err := generateTestMetadata(baselineMetadata, "baseline-run", 1000, 10, "test-agent"); err != nil {
		t.Fatalf("Failed to generate baseline metadata: %v", err)
	}

	// Generate current profile (heavier load for comparison)
	currentDir := filepath.Join(tmpCur, "current-run")
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatalf("Failed to create current dir: %v", err)
	}
	currentCPU := filepath.Join(currentDir, "cpu.pb.gz")
	currentMetadata := filepath.Join(currentDir, "metadata.json")
	if err := generateTestProfile(currentCPU, 20); err != nil {
		t.Fatalf("Failed to generate current profile: %v", err)
	}
	if err := generateTestMetadata(currentMetadata, "current-run", 1200, 20, "test-agent"); err != nil {
		t.Fatalf("Failed to generate current metadata: %v", err)
	}

	// Load metadata files
	currentMetaBytes, err := os.ReadFile(currentMetadata)
	if err != nil {
		t.Fatalf("Failed to read current metadata: %v", err)
	}
	var currentMeta RunMetadata
	if err := json.Unmarshal(currentMetaBytes, &currentMeta); err != nil {
		t.Fatalf("Failed to parse current metadata: %v", err)
	}

	baselineMetaBytes, err := os.ReadFile(baselineMetadata)
	if err != nil {
		t.Fatalf("Failed to read baseline metadata: %v", err)
	}
	var baselineMeta RunMetadata
	if err := json.Unmarshal(baselineMetaBytes, &baselineMeta); err != nil {
		t.Fatalf("Failed to parse baseline metadata: %v", err)
	}

	// Create comparison result (similar to what analyzeCommand does)
	durationChangeMS := currentMeta.DurationMS - baselineMeta.DurationMS
	durationChangePercent := 0.0
	if baselineMeta.DurationMS > 0 {
		durationChangePercent = (float64(durationChangeMS) / float64(baselineMeta.DurationMS)) * 100.0
	}

	comparison := &ComparisonResult{
		BaselineRunID:         baselineMeta.RunID,
		CurrentRunID:          currentMeta.RunID,
		BaselineDurationMS:    baselineMeta.DurationMS,
		CurrentDurationMS:     currentMeta.DurationMS,
		DurationChangeMS:      durationChangeMS,
		DurationChangePercent: durationChangePercent,
		HasDurationRegression: durationChangePercent > 15.0,
		RegressionThreshold:   15.0,
		BaselineTimestamp:     baselineMeta.Timestamp.Format("2006-01-02 15:04:05"),
		CurrentTimestamp:      currentMeta.Timestamp.Format("2006-01-02 15:04:05"),
		BaselineAvailable:     true,
		BaselineAgentName:     baselineMeta.AgentName,
		CurrentAgentName:      currentMeta.AgentName,
		BaselineCPUCores:      baselineMeta.CPUCores,
		CurrentCPUCores:       currentMeta.CPUCores,
		BaselineBuildID:       "100",
		AvgCoresDelta:         0.5,
		AvgCoresChangePercent: 10.0,
	}

	// Generate a minimal diff with increases/decreases
	diff := &ProfileDiff{
		ProfileType: "cpu",
		TopIncrease: []FunctionDelta{
			{
				Function:      "slowFunction",
				FlatDelta:     1000000000,
				CumDelta:      2000000000,
				ChangePercent: 50.0,
				Unit:          "nanoseconds",
			},
		},
		TopDecrease: []FunctionDelta{
			{
				Function:      "fastFunction",
				FlatDelta:     -500000000,
				CumDelta:      -1000000000,
				ChangePercent: -25.0,
				Unit:          "nanoseconds",
			},
		},
		TotalDelta: SampleDelta{
			BaselineTotal: 10000000000,
			CurrentTotal:  12000000000,
			Delta:         2000000000,
			ChangePercent: 20.0,
			Unit:          "nanoseconds",
		},
	}
	comparison.ProfileDiffs = []*ProfileDiff{diff}
	comparison.HasRegression = comparison.HasDurationRegression

	// Generate insights (minimal for test)
	insights := &ProfileInsights{
		Headlines: HeadlineNumbers{
			CPUTime:         2000000000,
			CPUCores:        2.5,
			HeapInUseBytes:  100 * 1024 * 1024,
			TotalAllocBytes: 500 * 1024 * 1024,
			AllocObjects:    10000,
			GoroutineCount:  100,
		},
	}

	outputHTML := filepath.Join(t.TempDir(), "report.html")

	// Generate report using internal function
	if err := generateReport(currentDir, outputHTML, comparison, "https://test.example.com/builds/123", insights); err != nil {
		t.Fatalf("Failed to generate report: %v", err)
	}

	reportPath := outputHTML

	// Read and verify HTML content
	htmlContent, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("Failed to read report HTML from %s: %v", reportPath, err)
	}

	htmlStr := string(htmlContent)

	// Check for template errors
	if strings.Contains(htmlStr, "<no value>") {
		t.Errorf("Report contains '<no value>' template errors")
	}
	if strings.Contains(htmlStr, "%!") {
		t.Errorf("Report contains '%%!' template formatting errors")
	}
	if strings.Contains(htmlStr, "{{") && !strings.Contains(htmlStr, "{{/*") {
		t.Errorf("Report contains unrendered '{{' template tags")
	}

	// Check for comparison cards presence
	if !strings.Contains(htmlStr, "Baseline comparison") && !strings.Contains(htmlStr, "Performance comparison") {
		t.Errorf("Report does not contain baseline comparison section")
	}

	t.Logf("Successfully generated report with %d bytes", len(htmlContent))
}

// generateTestProfile creates a minimal CPU profile for testing
func generateTestProfile(path string, iterations int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Start CPU profiling
	if err := pprof.StartCPUProfile(f); err != nil {
		return err
	}

	// Generate some CPU activity
	for i := 0; i < iterations; i++ {
		testWorkload()
	}

	pprof.StopCPUProfile()
	return nil
}

// testWorkload performs some CPU-intensive work
func testWorkload() {
	sum := 0
	for i := 0; i < 1000000; i++ {
		sum += i
	}
	// Allocate some memory
	_ = make([]byte, 1024*1024)
}

// generateTestMetadata creates a minimal metadata.json for testing
func generateTestMetadata(path string, runID string, durationMS int64, iterations int, agentName string) error {
	metadata := RunMetadata{
		RunID:              runID,
		Timestamp:          time.Now(),
		ProductName:        "Test Product",
		ProductVersion:     "1.0.0",
		ProductCommitID:    "abc123",
		TestCase:           "TestCase",
		TestCount:          10,
		TestPassCount:      10,
		DurationMS:         durationMS,
		ProfilesCollected:  true,
		Success:            true,
		CPUProfileDuration: 5,
		AgentName:          agentName,
		CPUCores:           4,
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
