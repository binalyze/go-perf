package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/pprof/profile"
)

// ProfileDiff represents the difference between two pprof profiles.
type ProfileDiff struct {
	ProfileType     string          `json:"profile_type"`
	BaselinePath    string          `json:"baseline_path,omitempty"`
	CurrentPath     string          `json:"current_path,omitempty"`
	TopIncrease     []FunctionDelta `json:"top_increase,omitempty"`
	TopDecrease     []FunctionDelta `json:"top_decrease,omitempty"`
	TotalDelta      SampleDelta     `json:"total_delta"`
	DiffOutputPath  string          `json:"diff_output_path,omitempty"`
	DiffProfilePath string          `json:"diff_profile_path,omitempty"`
	HasRegression   bool            `json:"has_regression"`
	Threshold       float64         `json:"threshold"`
}

// FunctionDelta represents a change in a specific function's profile data.
type FunctionDelta struct {
	Function        string  `json:"function"`
	FlatDelta       int64   `json:"flat_delta"`
	CumDelta        int64   `json:"cum_delta"`
	ChangePercent   float64 `json:"change_percent"`
	BaselineFlatPct float64 `json:"baseline_flat_pct,omitempty"`
	Unit            string  `json:"unit"`
}

// SampleDelta represents overall sample count changes.
type SampleDelta struct {
	BaselineTotal int64   `json:"baseline_total"`
	CurrentTotal  int64   `json:"current_total"`
	Delta         int64   `json:"delta"`
	ChangePercent float64 `json:"change_percent"`
	Unit          string  `json:"unit"` // "nanoseconds", "bytes", "count"
}

// DiffProfiles compares current profiles against baseline profiles.
func DiffProfiles(currentDir, baselineDir string, threshold float64) ([]*ProfileDiff, error) {
	profileTypes := []string{"cpu", "heap", "allocs", "goroutine"}
	var diffs []*ProfileDiff

	for _, ptype := range profileTypes {
		baselineProfile := filepath.Join(baselineDir, ptype+".pprof")
		currentProfile := filepath.Join(currentDir, ptype+".pprof")

		// Skip if either profile doesn't exist
		if _, err := os.Stat(baselineProfile); os.IsNotExist(err) {
			logger.Infof("Skipping %s diff: baseline profile not found", ptype)
			continue
		}
		if _, err := os.Stat(currentProfile); os.IsNotExist(err) {
			logger.Infof("Skipping %s diff: current profile not found", ptype)
			continue
		}

		logger.Infof("Diffing %s profile...", ptype)
		diff, err := diffProfile(baselineProfile, currentProfile, ptype, threshold)
		if err != nil {
			logger.Warnf("Failed to diff %s profile: %v", ptype, err)
			continue
		}

		diffs = append(diffs, diff)
	}

	return diffs, nil
}

// getUnitForProfileType returns the unit string for a given profile type
func getUnitForProfileType(profileType string) string {
	switch profileType {
	case "cpu":
		return "nanoseconds"
	case "heap", "allocs":
		return "bytes"
	case "goroutine":
		return "count"
	default:
		return "count"
	}
}

// diffProfile compares two pprof profiles using go tool pprof.
func diffProfile(baselinePath, currentPath, profileType string, threshold float64) (*ProfileDiff, error) {
	diff := &ProfileDiff{
		ProfileType:  profileType,
		BaselinePath: baselinePath,
		CurrentPath:  currentPath,
		Threshold:    threshold,
	}

	// Initialize TotalDelta with unit so parseDiffOutput can use it
	diff.TotalDelta.Unit = getUnitForProfileType(profileType)

	// Generate diff output directory next to current profile
	diffDir := filepath.Join(filepath.Dir(currentPath), "diffs")
	if err := os.MkdirAll(diffDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create diffs directory: %w", err)
	}

	// Save diff text output (top movers)
	diff.DiffOutputPath = filepath.Join(diffDir, profileType+"-diff.txt")
	if err := generateDiffText(baselinePath, currentPath, diff.DiffOutputPath); err != nil {
		return nil, fmt.Errorf("failed to generate diff text: %w", err)
	}

	// Parse diff output to extract top movers
	if err := parseDiffOutput(diff); err != nil {
		logger.Warnf("Failed to parse diff output for %s: %v", profileType, err)
	}
	logger.Infof("Parsed %s diff: %d increases, %d decreases", profileType, len(diff.TopIncrease), len(diff.TopDecrease))

	// Calculate total sample delta
	if err := calculateTotalDelta(baselinePath, currentPath, diff); err != nil {
		logger.Warnf("Failed to calculate total delta for %s: %v", profileType, err)
	}

	// Recalculate percentages based on cumulative delta (which we display)
	recalculateChangePercentages(diff)

	// Check for regression based on threshold
	if diff.TotalDelta.ChangePercent > threshold {
		diff.HasRegression = true
		logger.Warnf("REGRESSION detected in %s profile: %.2f%% increase (threshold: %.2f%%)",
			profileType, diff.TotalDelta.ChangePercent, threshold)
	}

	return diff, nil
}

// generateDiffText runs go tool pprof with -diff_base to generate diff output.
func generateDiffText(baselinePath, currentPath, outputPath string) error {
	// Use go tool pprof -diff_base to compare profiles
	// pprof -diff_base=baseline.pprof -top -nodecount=20 current.pprof
	cmd := exec.Command("go", "tool", "pprof",
		"-diff_base="+baselinePath,
		"-top",
		"-nodecount=20",
		currentPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pprof diff failed: %w\nOutput: %s", err, string(output))
	}

	if err := os.WriteFile(outputPath, output, 0644); err != nil {
		return fmt.Errorf("failed to write diff output: %w", err)
	}

	return nil
}

// parseValue converts a pprof value string to canonical units (nanoseconds for time, bytes for memory, count for others)
func parseValue(valueStr string) (int64, error) {
	valueStr = strings.TrimSpace(valueStr)

	// Handle bare numbers
	if val, err := strconv.ParseFloat(valueStr, 64); err == nil {
		return int64(val), nil
	}

	// Extract numeric part and unit
	var numericPart string
	var unit string

	for i, ch := range valueStr {
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' || ch == '+' {
			numericPart += string(ch)
		} else {
			unit = valueStr[i:]
			break
		}
	}

	if numericPart == "" {
		return 0, fmt.Errorf("no numeric value found in: %s", valueStr)
	}

	val, err := strconv.ParseFloat(numericPart, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse numeric part %q: %w", numericPart, err)
	}

	unit = strings.TrimSpace(unit)

	// Time units -> nanoseconds
	switch unit {
	case "ns":
		return int64(val), nil
	case "us", "µs":
		return int64(val * 1000), nil
	case "ms":
		return int64(val * 1000000), nil
	case "s":
		return int64(val * 1000000000), nil
	case "min":
		return int64(val * 60 * 1000000000), nil
	case "hrs", "hr", "h":
		return int64(val * 3600 * 1000000000), nil
	}

	// Byte units -> bytes
	switch unit {
	case "B":
		return int64(val), nil
	case "kB", "KB":
		return int64(val * 1024), nil
	case "MB":
		return int64(val * 1024 * 1024), nil
	case "GB":
		return int64(val * 1024 * 1024 * 1024), nil
	case "TB":
		return int64(val * 1024 * 1024 * 1024 * 1024), nil
	}

	// No unit or unknown unit - treat as count
	if unit == "" {
		return int64(val), nil
	}

	// Unknown unit - log warning but return value
	logger.Warnf("Unknown unit in value %q, treating as count", valueStr)
	return int64(val), nil
}

// parseDiffOutput parses the text output from go tool pprof -diff_base.
func parseDiffOutput(diff *ProfileDiff) error {
	data, err := os.ReadFile(diff.DiffOutputPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var increases, decreases []FunctionDelta

	// Parse pprof -top output format:
	// flat  flat%   sum%        cum   cum%  [function]
	// Values can have units (53.70s, 1.20GB, etc.), be signed (+/-), and have (inline) suffixes
	// Skip header lines like "Duration:", "Dropped N nodes", blank lines, column headers

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines, headers, and info lines
		if line == "" ||
			strings.HasPrefix(line, "flat") ||
			strings.HasPrefix(line, "Duration:") ||
			strings.HasPrefix(line, "Dropped ") ||
			strings.HasPrefix(line, "Showing ") ||
			strings.HasPrefix(line, "Type:") ||
			strings.HasPrefix(line, "Main binary") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Parse flat delta (first field, e.g., "+53.70s", "-12.41s", "+1.20GB")
		flatDelta, err := parseValue(fields[0])
		if err != nil {
			logger.Debugf("Skipping line, failed to parse flat delta %q: %v", fields[0], err)
			continue
		}

		// Parse cum delta (fourth field)
		cumDelta, err := parseValue(fields[3])
		if err != nil {
			logger.Debugf("Skipping line, failed to parse cum delta %q: %v", fields[3], err)
			continue
		}

		// Function name is the last field(s), may include (inline) suffix
		function := strings.Join(fields[5:], " ")
		function = strings.TrimSuffix(function, " (inline)")

		// Calculate change percentage (from flat%)
		var changePercent float64
		var baselineFlatPct float64
		if len(fields) > 1 {
			percentStr := strings.TrimSuffix(fields[1], "%")
			percentStr = strings.TrimPrefix(percentStr, "+")
			if val, err := strconv.ParseFloat(percentStr, 64); err == nil {
				changePercent = val
				// If this is a positive change, the percent is the increase
				// If negative, it's the decrease
				// The flat% in pprof output is relative to baseline total
				baselineFlatPct = changePercent
				if changePercent < 0 {
					baselineFlatPct = -changePercent
				}
			}
		}

		delta := FunctionDelta{
			Function:        function,
			FlatDelta:       flatDelta,
			CumDelta:        cumDelta,
			ChangePercent:   changePercent,
			BaselineFlatPct: baselineFlatPct,
			Unit:            diff.TotalDelta.Unit,
		}

		// Classify based on cumDelta since that's what we display
		if cumDelta > 0 {
			increases = append(increases, delta)
		} else if cumDelta < 0 {
			decreases = append(decreases, delta)
		}
	}

	// Sort by absolute cumulative delta (which is what we display)
	sort.Slice(increases, func(i, j int) bool {
		return increases[i].CumDelta > increases[j].CumDelta
	})
	sort.Slice(decreases, func(i, j int) bool {
		return -decreases[i].CumDelta > -decreases[j].CumDelta
	})

	// Keep top 10 of each
	if len(increases) > 10 {
		increases = increases[:10]
	}
	if len(decreases) > 10 {
		decreases = decreases[:10]
	}

	diff.TopIncrease = increases
	diff.TopDecrease = decreases

	return nil
}

// recalculateChangePercentages recalculates the ChangePercent for TopIncrease and TopDecrease
// based on cumulative delta (which is what we display) instead of flat delta.
func recalculateChangePercentages(diff *ProfileDiff) {
	baselineTotal := diff.TotalDelta.BaselineTotal
	if baselineTotal == 0 {
		return
	}

	for i := range diff.TopIncrease {
		diff.TopIncrease[i].ChangePercent = (float64(diff.TopIncrease[i].CumDelta) / float64(baselineTotal)) * 100.0
	}

	for i := range diff.TopDecrease {
		diff.TopDecrease[i].ChangePercent = (float64(diff.TopDecrease[i].CumDelta) / float64(baselineTotal)) * 100.0
	}
}

// calculateTotalDelta calculates the total sample count delta between profiles.
func calculateTotalDelta(baselinePath, currentPath string, diff *ProfileDiff) error {
	// Get total samples from baseline
	baselineTotal, unit, err := getTotalSamples(baselinePath, diff.ProfileType)
	if err != nil {
		return fmt.Errorf("failed to get baseline samples: %w", err)
	}

	// Get total samples from current
	currentTotal, _, err := getTotalSamples(currentPath, diff.ProfileType)
	if err != nil {
		return fmt.Errorf("failed to get current samples: %w", err)
	}

	delta := currentTotal - baselineTotal
	changePercent := 0.0
	if baselineTotal > 0 {
		changePercent = (float64(delta) / float64(baselineTotal)) * 100.0
	}

	diff.TotalDelta = SampleDelta{
		BaselineTotal: baselineTotal,
		CurrentTotal:  currentTotal,
		Delta:         delta,
		ChangePercent: changePercent,
		Unit:          unit,
	}

	return nil
}

// getTotalSamples extracts the total sample count from a profile using the google/pprof library.
// Returns the total value and the unit (e.g., "nanoseconds", "bytes", "count").
func getTotalSamples(profilePath string, profileType string) (int64, string, error) {
	// Read and parse the profile
	f, err := os.Open(profilePath)
	if err != nil {
		return 0, "", fmt.Errorf("failed to open profile: %w", err)
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return 0, "", fmt.Errorf("failed to parse profile: %w", err)
	}

	// Determine which sample type to use based on profile type
	var sampleTypeName string
	var unit string
	switch profileType {
	case "cpu":
		sampleTypeName = "cpu"
		unit = "nanoseconds"
	case "heap":
		sampleTypeName = "inuse_space"
		unit = "bytes"
	case "allocs":
		sampleTypeName = "alloc_space"
		unit = "bytes"
	case "goroutine":
		sampleTypeName = "goroutine"
		unit = "count"
	default:
		return 0, "", fmt.Errorf("unknown profile type: %s", profileType)
	}

	// Find the sample type index
	sampleTypeIndex := -1
	for i, st := range prof.SampleType {
		if st.Type == sampleTypeName {
			sampleTypeIndex = i
			break
		}
	}

	if sampleTypeIndex == -1 {
		return 0, "", fmt.Errorf("sample type %q not found in profile (available: %v)", sampleTypeName, prof.SampleType)
	}

	// Sum all samples for this type
	var total int64
	for _, sample := range prof.Sample {
		if sampleTypeIndex < len(sample.Value) {
			total += sample.Value[sampleTypeIndex]
		}
	}

	return total, unit, nil
}

// HasSignificantRegression checks if any profile has a significant regression.
func HasSignificantRegression(diffs []*ProfileDiff) bool {
	for _, diff := range diffs {
		if diff.HasRegression {
			return true
		}
	}
	return false
}
