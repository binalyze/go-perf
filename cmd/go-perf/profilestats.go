package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/pprof/profile"
	"github.com/urfave/cli/v2"
)

// ProfileStats represents statistics extracted from a profile
type ProfileStats struct {
	CPUNS      int64   `json:"cpu_ns"`
	DurationNS int64   `json:"duration_ns"`
	AvgCores   float64 `json:"avg_cores"`
}

func profileStatsCommand(c *cli.Context) error {
	cpuProfilePath := c.String("cpu")
	durationMS := c.Int64("duration-ms")

	if cpuProfilePath == "" {
		return fmt.Errorf("--cpu is required")
	}

	// Open and parse CPU profile
	f, err := os.Open(cpuProfilePath)
	if err != nil {
		return fmt.Errorf("failed to open CPU profile: %w", err)
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return fmt.Errorf("failed to parse CPU profile: %w", err)
	}

	// Get CPU total using the existing getTotalSamples logic
	// Find cpu/nanoseconds sample type
	var cpuIndex = -1
	for i, st := range prof.SampleType {
		if st.Type == "cpu" && st.Unit == "nanoseconds" {
			cpuIndex = i
			break
		}
	}

	if cpuIndex == -1 {
		return fmt.Errorf("cpu/nanoseconds sample type not found in profile")
	}

	// Sum all CPU samples
	var cpuNS int64
	for _, sample := range prof.Sample {
		if cpuIndex < len(sample.Value) {
			cpuNS += sample.Value[cpuIndex]
		}
	}

	// Get duration from profile or fallback to provided duration
	durationNS := prof.DurationNanos
	if durationNS <= 0 && durationMS > 0 {
		durationNS = durationMS * 1000000 // Convert ms to ns
	}

	// Calculate average cores
	var avgCores float64
	if durationNS > 0 {
		avgCores = float64(cpuNS) / float64(durationNS)
	}

	// Output as JSON
	stats := ProfileStats{
		CPUNS:      cpuNS,
		DurationNS: durationNS,
		AvgCores:   avgCores,
	}

	output, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(output))
	return nil
}
