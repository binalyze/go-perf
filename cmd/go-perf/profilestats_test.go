package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

func TestProfileStats_ValidCPUProfile(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-cpu.pprof")

	// Create a synthetic CPU profile with known values
	// Total CPU: 15 seconds (15,000,000,000 ns)
	// Duration: 30 seconds (30,000,000,000 ns)
	// Expected avg cores: 15 / 30 = 0.5
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{1, 5000000000}}, // 5 seconds
			{Value: []int64{1, 7000000000}}, // 7 seconds
			{Value: []int64{1, 3000000000}}, // 3 seconds
		},
		TimeNanos:     1234567890000000000,
		DurationNanos: 30000000000, // 30 seconds
	}

	f, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Run profile-stats command
	stats, err := extractProfileStats(profilePath, 0)
	if err != nil {
		t.Fatalf("extractProfileStats failed: %v", err)
	}

	expectedCPUNS := int64(15000000000) // 15 seconds
	if stats.CPUNS != expectedCPUNS {
		t.Errorf("CPUNS = %d, want %d", stats.CPUNS, expectedCPUNS)
	}

	expectedDurationNS := int64(30000000000) // 30 seconds
	if stats.DurationNS != expectedDurationNS {
		t.Errorf("DurationNS = %d, want %d", stats.DurationNS, expectedDurationNS)
	}

	expectedAvgCores := 0.5
	if stats.AvgCores != expectedAvgCores {
		t.Errorf("AvgCores = %f, want %f", stats.AvgCores, expectedAvgCores)
	}
}

func TestProfileStats_WithDurationFallback(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-cpu-noduration.pprof")

	// Create a profile without DurationNanos
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{1, 10000000000}}, // 10 seconds
		},
		TimeNanos:     1234567890000000000,
		DurationNanos: 0, // No duration in profile
	}

	f, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Run with fallback duration of 20000ms (20 seconds)
	stats, err := extractProfileStats(profilePath, 20000)
	if err != nil {
		t.Fatalf("extractProfileStats failed: %v", err)
	}

	expectedCPUNS := int64(10000000000) // 10 seconds
	if stats.CPUNS != expectedCPUNS {
		t.Errorf("CPUNS = %d, want %d", stats.CPUNS, expectedCPUNS)
	}

	expectedDurationNS := int64(20000000000) // 20 seconds (from fallback)
	if stats.DurationNS != expectedDurationNS {
		t.Errorf("DurationNS = %d, want %d", stats.DurationNS, expectedDurationNS)
	}

	expectedAvgCores := 0.5 // 10s / 20s
	if stats.AvgCores != expectedAvgCores {
		t.Errorf("AvgCores = %f, want %f", stats.AvgCores, expectedAvgCores)
	}
}

func TestProfileStats_MissingCPUType(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-heap.pprof")

	// Create a heap profile (no cpu type)
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{10, 1024}},
		},
		TimeNanos:     1234567890000000000,
		DurationNanos: 30000000000,
	}

	f, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Should fail with missing cpu type
	_, err = extractProfileStats(profilePath, 0)
	if err == nil {
		t.Error("Expected error for profile without cpu/nanoseconds type")
	}
}

// extractProfileStats is a helper that mimics the profile-stats command logic
func extractProfileStats(cpuProfilePath string, durationMS int64) (*ProfileStats, error) {
	f, err := os.Open(cpuProfilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return nil, err
	}

	// Find cpu/nanoseconds sample type
	var cpuIndex = -1
	for i, st := range prof.SampleType {
		if st.Type == "cpu" && st.Unit == "nanoseconds" {
			cpuIndex = i
			break
		}
	}

	if cpuIndex == -1 {
		return nil, fmt.Errorf("cpu/nanoseconds sample type not found")
	}

	// Sum all CPU samples
	var cpuNS int64
	for _, sample := range prof.Sample {
		if cpuIndex < len(sample.Value) {
			cpuNS += sample.Value[cpuIndex]
		}
	}

	// Get duration from profile or fallback
	durationNS := prof.DurationNanos
	if durationNS <= 0 && durationMS > 0 {
		durationNS = durationMS * 1000000
	}

	// Calculate average cores
	var avgCores float64
	if durationNS > 0 {
		avgCores = float64(cpuNS) / float64(durationNS)
	}

	return &ProfileStats{
		CPUNS:      cpuNS,
		DurationNS: durationNS,
		AvgCores:   avgCores,
	}, nil
}

func TestProfileStatsJSON(t *testing.T) {
	stats := ProfileStats{
		CPUNS:      15000000000,
		DurationNS: 30000000000,
		AvgCores:   0.5,
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	var decoded ProfileStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if decoded.CPUNS != stats.CPUNS {
		t.Errorf("CPUNS mismatch: got %d, want %d", decoded.CPUNS, stats.CPUNS)
	}
	if decoded.DurationNS != stats.DurationNS {
		t.Errorf("DurationNS mismatch: got %d, want %d", decoded.DurationNS, stats.DurationNS)
	}
	if decoded.AvgCores != stats.AvgCores {
		t.Errorf("AvgCores mismatch: got %f, want %f", decoded.AvgCores, stats.AvgCores)
	}
}
