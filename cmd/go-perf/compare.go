package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/urfave/cli/v2"
)

type ComparisonResult struct {
	BaselineRunID         string         `json:"baseline_run_id"`
	CurrentRunID          string         `json:"current_run_id"`
	BaselineDurationMS    int64          `json:"baseline_duration_ms"`
	CurrentDurationMS     int64          `json:"current_duration_ms"`
	DurationChangeMS      int64          `json:"duration_change_ms"`
	DurationChangePercent float64        `json:"duration_change_percent"`
	HasDurationRegression bool           `json:"has_duration_regression"`
	HasProfileRegression  bool           `json:"has_profile_regression"`
	HasRegression         bool           `json:"has_regression"`
	RegressionThreshold   float64        `json:"regression_threshold"`
	BaselineTimestamp     string         `json:"baseline_timestamp"`
	CurrentTimestamp      string         `json:"current_timestamp"`
	ProfileDiffs          []*ProfileDiff `json:"profile_diffs,omitempty"`
	BaselineAvailable     bool           `json:"baseline_available"`
	BaselineAgentName     string         `json:"baseline_agent_name,omitempty"`
	CurrentAgentName      string         `json:"current_agent_name,omitempty"`
	BaselineCPUCores      int            `json:"baseline_cpu_cores,omitempty"`
	CurrentCPUCores       int            `json:"current_cpu_cores,omitempty"`
	BaselineAvgCores      float64        `json:"baseline_avg_cores,omitempty"`
	CurrentAvgCores       float64        `json:"current_avg_cores,omitempty"`
	AvgCoresDelta         float64        `json:"avg_cores_delta,omitempty"`
	AvgCoresChangePercent float64        `json:"avg_cores_change_percent,omitempty"`
	BaselineBuildID       string         `json:"baseline_build_id,omitempty"`
}

func compareCommand(c *cli.Context) error {
	workDir := c.String(flagWorkDir)
	runAID := c.String("run-a")
	runBID := c.String("run-b")
	threshold := c.Float64(flagRegressionThresh)

	comparison, err := compareRuns(workDir, runAID, runBID, threshold)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(data))

	if comparison.HasRegression {
		logger.Warnf("REGRESSION: Performance degraded by %.2f%% (threshold: %.2f%%)",
			comparison.DurationChangePercent, threshold)
		return fmt.Errorf("regression detected")
	}

	logger.Infof("No regression detected. Change: %.2f%%", comparison.DurationChangePercent)
	return nil
}

func compareRuns(workDir, baselineRunID, currentRunID string, threshold float64) (*ComparisonResult, error) {
	result := &ComparisonResult{
		CurrentRunID:        currentRunID,
		RegressionThreshold: threshold,
		BaselineAvailable:   false,
	}

	// Load current metadata
	currentMetadata, err := loadMetadata(filepath.Join(workDir, currentRunID))
	if err != nil {
		return nil, fmt.Errorf("failed to load current metadata: %w", err)
	}
	result.CurrentDurationMS = currentMetadata.DurationMS
	result.CurrentTimestamp = currentMetadata.Timestamp.Format("2006-01-02 15:04:05")

	// Find baseline run
	if baselineRunID == "" {
		baselineRunID, err = findPreviousRun(workDir, currentRunID)
		if err != nil {
			logger.Warnf("Failed to find baseline run: %v", err)
			return result, nil
		}
		if baselineRunID == "" {
			logger.Infof("No previous run found for comparison")
			return result, nil
		}
	}

	// Load baseline metadata
	baselineMetadata, err := loadMetadata(filepath.Join(workDir, baselineRunID))
	if err != nil {
		logger.Warnf("Failed to load baseline metadata: %v", err)
		return result, nil
	}

	result.BaselineAvailable = true
	result.BaselineRunID = baselineRunID
	result.BaselineDurationMS = baselineMetadata.DurationMS
	result.BaselineTimestamp = baselineMetadata.Timestamp.Format("2006-01-02 15:04:05")

	// Compare durations
	durationChange := currentMetadata.DurationMS - baselineMetadata.DurationMS
	changePercent := 0.0
	if baselineMetadata.DurationMS > 0 {
		changePercent = (float64(durationChange) / float64(baselineMetadata.DurationMS)) * 100.0
	}

	result.DurationChangeMS = durationChange
	result.DurationChangePercent = changePercent
	result.HasDurationRegression = changePercent > threshold

	// Extract and diff profiles
	currentProfilesDir := filepath.Join(workDir, currentRunID, "profiles")
	baselineProfilesDir := filepath.Join(workDir, baselineRunID, "profiles")

	// Extract current profiles from zip
	currentZip := filepath.Join(workDir, currentRunID, "profiles.zip")
	if err := extractProfiles(currentZip, currentProfilesDir); err != nil {
		logger.Warnf("Failed to extract current profiles: %v", err)
	} else {
		// Extract baseline profiles from zip
		baselineZip := filepath.Join(workDir, baselineRunID, "profiles.zip")
		if err := extractProfiles(baselineZip, baselineProfilesDir); err != nil {
			logger.Warnf("Failed to extract baseline profiles: %v", err)
		} else {
			// Diff the profiles
			logger.Infof("Diffing profiles against baseline...")
			profileDiffs, err := DiffProfiles(currentProfilesDir, baselineProfilesDir, threshold)
			if err != nil {
				logger.Warnf("Failed to diff profiles: %v", err)
			} else {
				result.ProfileDiffs = profileDiffs
				result.HasProfileRegression = HasSignificantRegression(profileDiffs)
			}
		}
	}

	result.HasRegression = result.HasDurationRegression || result.HasProfileRegression

	return result, nil
}

// extractProfiles extracts pprof files from profiles.zip to a directory.
func extractProfiles(zipPath, destDir string) error {
	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		return fmt.Errorf("profiles zip not found: %s", zipPath)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create profiles directory: %w", err)
	}

	if err := extractZip(zipPath, destDir); err != nil {
		return fmt.Errorf("failed to extract %s: %w", zipPath, err)
	}

	return nil
}

func loadMetadata(runDir string) (*RunMetadata, error) {
	metadataPath := filepath.Join(runDir, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, err
	}

	var metadata RunMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

func findPreviousRun(workDir, currentRunID string) (string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", err
	}

	var runs []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != currentRunID && len(entry.Name()) > 4 && entry.Name()[:4] == "run-" {
			runs = append(runs, entry.Name())
		}
	}

	if len(runs) == 0 {
		return "", nil
	}

	sort.Strings(runs)
	return runs[len(runs)-1], nil
}
