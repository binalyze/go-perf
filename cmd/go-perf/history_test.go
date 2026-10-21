package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestRobustMetricStats(t *testing.T) {
	history := []float64{10, 12, 11, 13, 10, 12, 11, 14, 9}
	stat := robustMetric("duration_ms", "ms", true, history, 12, 15)
	if stat.Median != 11 {
		t.Fatalf("median = %v, want 11", stat.Median)
	}
	if stat.MAD <= 0 || stat.P90 < stat.Median {
		t.Fatalf("mad=%v p90=%v, want a positive spread and p90 at or above the median", stat.MAD, stat.P90)
	}
	if math.Abs(stat.Slope) > 1 {
		t.Fatalf("slope = %v, want a nearly flat series", stat.Slope)
	}
	if stat.Label != "stable" && stat.Label != "noisy" {
		t.Fatalf("label = %s, want stable or noisy for a value inside the band", stat.Label)
	}
}

func TestNoisySeriesIsNotARegression(t *testing.T) {
	history := []float64{80, 140, 90, 160, 70, 150, 100, 130, 110}
	stat := robustMetric("duration_ms", "ms", true, history, 150, 15)
	if stat.Label == "regressed" {
		t.Fatalf("label = regressed, z=%v change=%v; a noisy series must not regress", stat.ZScore, stat.ChangeVsMedianPercent)
	}
	if stat.CV <= noisyCV {
		t.Fatalf("cv = %v, want a noisy series", stat.CV)
	}
}

func TestSustainedShiftRegresses(t *testing.T) {
	history := make([]float64, 8)
	for i := range history {
		history[i] = 100
	}
	stat := robustMetric("duration_ms", "ms", true, history, 130, 15)
	if stat.Label != "regressed" {
		t.Fatalf("label = %s, want regressed (change %.1f%%, z %.2f)", stat.Label, stat.ChangeVsMedianPercent, stat.ZScore)
	}
}

func TestSlowCreep(t *testing.T) {
	history := []float64{100, 103, 106, 109, 112, 115, 118, 121, 124}
	stat := robustMetric("avg_cores", "cores", true, history, 127, 15)
	if stat.Label != "creeping" {
		t.Fatalf("label = %s, want creeping (change %.1f%%, z %.2f, slope %.2f)", stat.Label, stat.ChangeVsMedianPercent, stat.ZScore, stat.Slope)
	}
}

func TestHeapDoesNotGate(t *testing.T) {
	history := make([]float64, 6)
	for i := range history {
		history[i] = 100
	}
	stat := robustMetric("heap_inuse_bytes", "bytes", false, history, 400, 15)
	if stat.Label == "regressed" {
		t.Fatal("heap must not be labeled regressed; it does not gate")
	}
}

func TestListHistoryDirsNewestFirst(t *testing.T) {
	root := t.TempDir()
	writeSummaryFixture(t, filepath.Join(root, "old"), "2020-01-01T00:00:00Z", "old")
	writeSummaryFixture(t, filepath.Join(root, "new"), "2024-01-01T00:00:00Z", "new")
	writeSummaryFixture(t, filepath.Join(root, "mid"), "2022-01-01T00:00:00Z", "mid")

	dirs, err := listHistoryDirs(root, 2)
	if err != nil {
		t.Fatalf("listHistoryDirs: %v", err)
	}
	if len(dirs) != 2 || filepath.Base(dirs[0]) != "new" || filepath.Base(dirs[1]) != "mid" {
		t.Fatalf("dirs = %v, want new then mid", dirs)
	}

	current := &RunSummary{RunID: "now", Timestamp: "2025-01-01T00:00:00Z", DurationMS: 100, Success: true, Profiles: map[string]ProfileSummary{}}
	analysis, err := buildHistory(dirs, current, 15)
	if err != nil {
		t.Fatalf("buildHistory: %v", err)
	}
	if analysis.LatestRunID != "new" || analysis.RunCount != 2 {
		t.Fatalf("latest=%s count=%d, want new and 2", analysis.LatestRunID, analysis.RunCount)
	}
}

func writeSummaryFixture(t *testing.T, dir, timestamp, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	summary := &RunSummary{
		RunID: id, Timestamp: timestamp, DurationMS: 100, Success: true,
		Profiles: map[string]ProfileSummary{},
	}
	if err := saveRunSummary(dir, summary); err != nil {
		t.Fatalf("save summary: %v", err)
	}
}
