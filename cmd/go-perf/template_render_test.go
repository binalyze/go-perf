package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTemplateRenderWithRealisticData tests that the template renders successfully
// with realistic ReportData including AI analysis, profile diffs, and insights.
// This catches template field errors that would only show up at runtime.
func TestTemplateRenderWithRealisticData(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-template-render-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	runDir := filepath.Join(tmpDir, "001")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create metadata
	metadataJSON := `{
		"run_id": "001",
		"timestamp": "2026-10-19T12:00:00Z",
		"product_name": "example-app",
		"product_version": "feature/e2e-perf-pipeline",
		"test_case": "Test",
		"duration_ms": 125000,
		"profiles_collected": true,
		"cpu_profile_duration": 10,
		"success": true,
		"agent_name": "agent-linux-01",
		"cpu_cores": 8
	}`
	if err := os.WriteFile(filepath.Join(runDir, "metadata.json"), []byte(metadataJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create realistic comparison with profile diffs
	comparison := &ComparisonResult{
		BaselineRunID:         "000",
		CurrentRunID:          "001",
		BaselineDurationMS:    120000,
		CurrentDurationMS:     125000,
		DurationChangeMS:      5000,
		DurationChangePercent: 4.17,
		HasDurationRegression: false,
		HasProfileRegression:  true,
		HasRegression:         true,
		RegressionThreshold:   15.0,
		BaselineTimestamp:     "2026-10-19 11:00:00",
		CurrentTimestamp:      "2026-10-19 12:00:00",
		BaselineAvailable:     true,
		BaselineAgentName:     "agent-linux-02",
		CurrentAgentName:      "agent-linux-01",
		BaselineCPUCores:      8,
		CurrentCPUCores:       8,
		BaselineAvgCores:      3.5,
		CurrentAvgCores:       3.8,
		AvgCoresDelta:         0.3,
		AvgCoresChangePercent: 8.57,
		BaselineBuildID:       "12345",
		ProfileDiffs: []*ProfileDiff{
			{
				ProfileType:   "cpu",
				HasRegression: true,
				TotalDelta: SampleDelta{
					BaselineTotal: 1000000000,
					CurrentTotal:  1300000000,
					Delta:         300000000,
					ChangePercent: 30.0,
					Unit:          "nanoseconds",
				},
				TopIncrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/evidence.Collect[example.com/evidencerepo.RemoteFileInfo, error]",
						CumDelta:      45470000000,
						ChangePercent: 40.0,
						Unit:          "nanoseconds",
					},
					{
						Function:      "example.com/app/pkg/storage.Store",
						CumDelta:      20000000000,
						ChangePercent: 25.0,
						Unit:          "nanoseconds",
					},
				},
				TopDecrease: []FunctionDelta{
					{
						Function:      "runtime.gcBgMarkWorker",
						CumDelta:      -5000000000,
						ChangePercent: -10.0,
						Unit:          "nanoseconds",
					},
				},
			},
			{
				ProfileType:   "heap",
				HasRegression: false,
				TotalDelta: SampleDelta{
					BaselineTotal: 100000000,
					CurrentTotal:  130000000,
					Delta:         30000000,
					ChangePercent: 30.0,
					Unit:          "bytes",
				},
				TopIncrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/cache.New",
						CumDelta:      826781204,
						ChangePercent: 35.0,
						Unit:          "bytes",
					},
				},
			},
			{
				ProfileType:   "allocs",
				HasRegression: true,
				TotalDelta: SampleDelta{
					BaselineTotal: 500000000,
					CurrentTotal:  650000000,
					Delta:         150000000,
					ChangePercent: 30.0,
					Unit:          "bytes",
				},
				TopIncrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/parser.Parse",
						CumDelta:      50000000,
						ChangePercent: 20.0,
						Unit:          "bytes",
					},
				},
				TopDecrease: []FunctionDelta{
					{
						Function:      "runtime.mallocgc",
						CumDelta:      -10737418,
						ChangePercent: -5.0,
						Unit:          "bytes",
					},
				},
			},
			{
				ProfileType:   "goroutine",
				HasRegression: false,
				TotalDelta: SampleDelta{
					BaselineTotal: 50,
					CurrentTotal:  65,
					Delta:         15,
					ChangePercent: 30.0,
					Unit:          "count",
				},
				TopIncrease: []FunctionDelta{
					{
						Function:      "example.com/app/pkg/worker.Run",
						CumDelta:      10,
						ChangePercent: 25.0,
						Unit:          "count",
					},
				},
			},
		},
	}

	// Create realistic insights
	insights := &ProfileInsights{
		Headlines: HeadlineNumbers{
			CPUTime:              125000000000,
			CPUCores:             3.8,
			HeapInUseBytes:       52428800,
			TotalAllocBytes:      2147483648,
			AllocObjects:         1000000,
			GoroutineCount:       65,
			CPUTimeDelta:         5000000000,
			CPUCoresDelta:        0.3,
			HeapInUseBytesDelta:  5242880,
			TotalAllocBytesDelta: 104857600,
			AllocObjectsDelta:    50000,
			GoroutineCountDelta:  15,
			HasBaseline:          true,
		},
		CPU: CPUInsights{
			TotalSamples:   125000,
			SampleDuration: 10000000000,
			CPUTimeNanos:   125000000000,
			Utilization:    3.8,
			TopFlat: []FunctionSample{
				{
					Function:    "example.com/app/pkg/evidence.Collect",
					Flat:        10000000000,
					FlatPercent: 8.0,
					Cum:         15000000000,
					CumPercent:  12.0,
				},
				{
					Function:    "example.com/app/pkg/storage.Store",
					Flat:        8000000000,
					FlatPercent: 6.4,
					Cum:         12000000000,
					CumPercent:  9.6,
				},
			},
			TopPackages: []PackageSample{
				{
					Package:     "example.com/app/pkg/evidence",
					Flat:        20000000000,
					FlatPercent: 16.0,
				},
				{
					Package:     "example.com/app/pkg/storage",
					Flat:        15000000000,
					FlatPercent: 12.0,
				},
			},
		},
		Memory: MemoryInsights{
			InUseBytes:   52428800,
			InUseObjects: 100000,
			TopInUseSpace: []FunctionSample{
				{
					Function:    "example.com/app/pkg/cache.New",
					Flat:        10485760,
					FlatPercent: 20.0,
				},
			},
		},
		Allocs: AllocsInsights{
			AllocBytes:   2147483648,
			AllocObjects: 1000000,
			TopAllocSpace: []FunctionSample{
				{
					Function:    "example.com/app/pkg/parser.Parse",
					Flat:        104857600,
					FlatPercent: 4.9,
				},
			},
		},
		Goroutines: GoroutineInsights{
			TotalCount: 65,
			TopFunctions: []GoroutineSample{
				{
					Function: "example.com/app/pkg/worker.Run",
					Count:    25,
					Percent:  38.5,
				},
			},
		},
	}

	// Create AI analysis and save it to file so generateReport can load it
	aiAnalysisJSON := `{
		"verdict": "warning",
		"headline": "CPU utilization increased by 8.6% with evidence collection overhead",
		"key_numbers": [
			{"label": "CPU Time Increase", "value": "+5.0s"},
			{"label": "Memory Growth", "value": "+5.0 MB"}
		],
		"notable_changes": [
			{
				"function": "example.com/app/pkg/evidence.Collect",
				"profile": "cpu",
				"change": "+44.95s (+40%)",
				"likely_cause": "Increased file I/O operations",
				"source_ref": "pkg/evidence/collect.go"
			}
		],
		"recommendations": [
			"Review evidence collection batching strategy",
			"Consider caching frequently accessed metadata"
		],
		"confidence": "high"
	}`
	if err := os.WriteFile(filepath.Join(runDir, "ai-analysis.json"), []byte(aiAnalysisJSON), 0644); err != nil {
		t.Fatal(err)
	}

	htmlPath := filepath.Join(tmpDir, "report.html")
	if err := generateReport(runDir, htmlPath, comparison, "https://build.example.com/12345", insights); err != nil {
		t.Fatalf("generateReport failed: %v", err)
	}

	htmlContent, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}

	html := string(htmlContent)

	// Verify key sections render
	if !strings.Contains(html, "Executive Summary") {
		t.Error("Report should contain Executive Summary")
	}

	if !strings.Contains(html, "Profile Insights") {
		t.Error("Report should contain Profile Insights")
	}

	if !strings.Contains(html, "Profile Diffs") {
		t.Error("Report should contain Profile Diffs")
	}

	// Verify AI content renders (headline)
	if !strings.Contains(html, "CPU utilization increased by 8.6%") {
		t.Error("Report should contain AI headline")
	}

	// Verify confidence renders
	if !strings.Contains(html, "Confidence: high") {
		t.Error("Report should contain confidence level")
	}

	// Verify formatted deltas (not raw numbers)
	if strings.Contains(html, "+45,470,000,000") {
		t.Error("Report should not contain unformatted CPU delta (+45,470,000,000)")
	}
	if !strings.Contains(html, "+44.") || !strings.Contains(html, "+45.") {
		t.Log("Warning: Expected to find formatted CPU delta like +44.95s or +45.0s")
	}

	if strings.Contains(html, "+826,781,204") {
		t.Error("Report should not contain unformatted byte delta (+826,781,204)")
	}

	// Verify decrease has negative sign
	if strings.Contains(html, "Top Decreases") {
		if strings.Contains(html, "runtime.mallocgc") {
			// Check that the allocs decrease shows a negative value
			if !strings.Contains(html, "-10") && !strings.Contains(html, "−10") {
				t.Error("Allocs Top Decreases should show negative delta")
			}
		}
	}

	// Verify diagnostics section
	if !strings.Contains(html, "Diagnostics") {
		t.Error("Report should contain Diagnostics section")
	}

	if !strings.Contains(html, "perf-data.zip") {
		t.Error("Diagnostics should mention perf-data.zip")
	}

	// Verify struct fields render correctly (no "can't evaluate field" errors)
	// If there were field errors, the template would have failed to render
	if len(html) < 10000 {
		t.Errorf("Generated HTML is suspiciously short (%d bytes), may indicate template errors", len(html))
	}
}
