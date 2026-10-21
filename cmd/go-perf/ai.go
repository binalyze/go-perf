package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const maxAIInputSizeBytes = 60 * 1024

type AIInput struct {
	Metadata        AIMetadata                 `json:"metadata"`
	Comparison      AIComparison               `json:"comparison"`
	ProfileChanges  map[string]AIProfileChange `json:"profile_changes,omitempty"`
	ProfileInsights AIProfileInsights          `json:"profile_insights,omitempty"`
	History         *HistoryAnalysis           `json:"history,omitempty"`
}

type AIMetadata struct {
	CurrentRunID        string  `json:"current_run_id"`
	BaselineRunID       string  `json:"baseline_run_id,omitempty"`
	BaselineBuildID     string  `json:"baseline_build_id,omitempty"`
	TestCase            string  `json:"test_case"`
	CommitSHA           string  `json:"commit_sha,omitempty"`
	RegressionThreshold float64 `json:"regression_threshold"`
	BaselineAgent       string  `json:"baseline_agent,omitempty"`
	CurrentAgent        string  `json:"current_agent,omitempty"`
	EnvNote             string  `json:"env_note,omitempty"`
}

type AIComparison struct {
	BaselineAvailable     bool    `json:"baseline_available"`
	BaselineDurationMS    int64   `json:"baseline_duration_ms,omitempty"`
	CurrentDurationMS     int64   `json:"current_duration_ms"`
	DurationChangeMS      int64   `json:"duration_change_ms,omitempty"`
	DurationChangePercent float64 `json:"duration_change_percent,omitempty"`
	HasRegression         bool    `json:"has_regression"`
}

type AIProfileChange struct {
	ProfileType   string            `json:"profile_type"`
	TotalDelta    float64           `json:"total_delta_percent"`
	TopIncreases  []AIFunctionDelta `json:"top_increases"`
	TopDecreases  []AIFunctionDelta `json:"top_decreases"`
	HasRegression bool              `json:"has_regression"`
}

type AIFunctionDelta struct {
	Function      string  `json:"function"`
	BaselineValue int64   `json:"baseline_value"`
	CurrentValue  int64   `json:"current_value"`
	Delta         int64   `json:"delta"`
	ChangePercent float64 `json:"change_percent"`
}

func generateAIInput(runDir string, currentMeta *RunMetadata, comparison *ComparisonResult, threshold float64, insights *ProfileInsights, history *HistoryAnalysis) error {
	runID := filepath.Base(runDir)

	aiInput := AIInput{
		Metadata: AIMetadata{
			CurrentRunID:        runID,
			TestCase:            currentMeta.TestCase,
			CommitSHA:           currentMeta.ProductCommitID,
			RegressionThreshold: threshold,
		},
		History: history,
		Comparison: AIComparison{
			BaselineAvailable: comparison != nil && comparison.BaselineAvailable,
			CurrentDurationMS: currentMeta.DurationMS,
		},
		ProfileChanges: make(map[string]AIProfileChange),
	}
	if history != nil {
		aiInput.Metadata.EnvNote = history.EnvNote
	}

	if comparison != nil && comparison.BaselineAvailable {
		aiInput.Metadata.BaselineRunID = comparison.BaselineRunID
		aiInput.Metadata.BaselineBuildID = comparison.BaselineBuildID
		aiInput.Metadata.BaselineAgent = comparison.BaselineAgentName
		aiInput.Metadata.CurrentAgent = comparison.CurrentAgentName
		aiInput.Comparison.BaselineDurationMS = comparison.BaselineDurationMS
		aiInput.Comparison.DurationChangeMS = comparison.DurationChangeMS
		aiInput.Comparison.DurationChangePercent = comparison.DurationChangePercent
		aiInput.Comparison.HasRegression = comparison.HasRegression

		// Extract profile changes from diffs
		for _, diff := range comparison.ProfileDiffs {
			profileChange := AIProfileChange{
				ProfileType:   diff.ProfileType,
				TotalDelta:    diff.TotalDelta.ChangePercent,
				HasRegression: diff.HasRegression,
			}

			// Extract top increases
			for i, fn := range diff.TopIncrease {
				if i >= 15 {
					break
				}
				profileChange.TopIncreases = append(profileChange.TopIncreases, AIFunctionDelta{
					Function:      fn.Function,
					BaselineValue: 0,
					CurrentValue:  0,
					Delta:         fn.CumDelta,
					ChangePercent: fn.ChangePercent,
				})
			}

			// Extract top decreases
			for i, fn := range diff.TopDecrease {
				if i >= 15 {
					break
				}
				profileChange.TopDecreases = append(profileChange.TopDecreases, AIFunctionDelta{
					Function:      fn.Function,
					BaselineValue: 0,
					CurrentValue:  0,
					Delta:         fn.CumDelta,
					ChangePercent: fn.ChangePercent,
				})
			}

			aiInput.ProfileChanges[diff.ProfileType] = profileChange
		}
	}

	// Add profile insights (compact representation)
	if insights != nil {
		aiInsights := AIProfileInsights{
			CPUCores:       insights.Headlines.CPUCores,
			CPUTimeMS:      insights.Headlines.CPUTime / 1000000,
			HeapInUseMB:    insights.Headlines.HeapInUseBytes / (1024 * 1024),
			TotalAllocGB:   float64(insights.Headlines.TotalAllocBytes) / (1024 * 1024 * 1024),
			GoroutineCount: insights.Headlines.GoroutineCount,
			HasBaseline:    insights.Headlines.HasBaseline,
		}

		// Top CPU functions (top 5 for compactness)
		for i := 0; i < len(insights.CPU.TopFlat) && i < 5; i++ {
			aiInsights.TopCPUFunctions = append(aiInsights.TopCPUFunctions,
				fmt.Sprintf("%s (%.1f%%)", TrimFunctionName(insights.CPU.TopFlat[i].Function), insights.CPU.TopFlat[i].FlatPercent))
		}

		// Top CPU packages (top 5)
		for i := 0; i < len(insights.CPU.TopPackages) && i < 5; i++ {
			aiInsights.TopCPUPackages = append(aiInsights.TopCPUPackages,
				fmt.Sprintf("%s (%.1f%%)", insights.CPU.TopPackages[i].Package, insights.CPU.TopPackages[i].FlatPercent))
		}

		// Top heap functions (top 5)
		for i := 0; i < len(insights.Memory.TopInUseSpace) && i < 5; i++ {
			aiInsights.TopHeapFuncs = append(aiInsights.TopHeapFuncs,
				fmt.Sprintf("%s (%s)", TrimFunctionName(insights.Memory.TopInUseSpace[i].Function), formatBytes(insights.Memory.TopInUseSpace[i].Flat)))
		}

		// Top alloc functions (top 5)
		for i := 0; i < len(insights.Allocs.TopAllocSpace) && i < 5; i++ {
			aiInsights.TopAllocFuncs = append(aiInsights.TopAllocFuncs,
				fmt.Sprintf("%s (%s)", TrimFunctionName(insights.Allocs.TopAllocSpace[i].Function), formatBytes(insights.Allocs.TopAllocSpace[i].Flat)))
		}

		// Deltas
		if insights.Headlines.HasBaseline {
			aiInsights.Deltas = &AIProfileInsightDeltas{
				CPUCores:       insights.Headlines.CPUCoresDelta,
				HeapInUseMB:    insights.Headlines.HeapInUseBytesDelta / (1024 * 1024),
				GoroutineCount: insights.Headlines.GoroutineCountDelta,
			}
		}

		aiInput.ProfileInsights = aiInsights
	}

	data, err := fitAIInput(&aiInput)
	if err != nil {
		return err
	}

	aiInputPath := filepath.Join(runDir, "ai-input.json")
	if err := os.WriteFile(aiInputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write AI input: %w", err)
	}

	fmt.Printf("[ANALYSIS] Generated AI input: %s (%d bytes)\n", aiInputPath, len(data))
	return nil
}

func loadAIAnalysis(runDir string) (*AIAnalysis, error) {
	aiAnalysisPath := filepath.Join(runDir, "ai-analysis.json")
	data, err := os.ReadFile(aiAnalysisPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read AI analysis: %w", err)
	}

	var analysis AIAnalysis
	if err := json.Unmarshal(data, &analysis); err != nil {
		return nil, fmt.Errorf("failed to parse AI analysis: %w", err)
	}
	if err := validateAIAnalysis(&analysis); err != nil {
		return nil, fmt.Errorf("invalid AI analysis: %w", err)
	}

	return &analysis, nil
}

func validateAIAnalysis(analysis *AIAnalysis) error {
	switch analysis.Verdict {
	case "ok", "warning", "regression":
	default:
		return fmt.Errorf("verdict %q is not ok, warning, or regression", analysis.Verdict)
	}
	switch analysis.Confidence {
	case "", "high", "medium", "low":
	default:
		return fmt.Errorf("confidence %q is not high, medium, or low", analysis.Confidence)
	}
	for i, finding := range analysis.Findings {
		if finding.ID == "" || finding.Title == "" {
			return fmt.Errorf("finding %d is missing id or title", i)
		}
		switch finding.Severity {
		case "high", "medium", "low":
		default:
			return fmt.Errorf("finding %s has severity %q", finding.ID, finding.Severity)
		}
	}
	if analysis.Findings == nil {
		analysis.Findings = []AIFinding{}
	}
	if analysis.MetricNotes == nil {
		analysis.MetricNotes = []AIMetricNote{}
	}
	return nil
}

// fitAIInput marshals the payload and, if it exceeds the size budget, drops
// the least important lists first: profile decreases, then extra increases,
// then hotspot tails.
func fitAIInput(in *AIInput) ([]byte, error) {
	shrink := []func(*AIInput) bool{
		func(in *AIInput) bool { return trimDeltas(in, true, 5) },
		func(in *AIInput) bool { return trimDeltas(in, false, 5) },
		func(in *AIInput) bool { return trimHotspots(in, 5) },
		func(in *AIInput) bool {
			if len(in.ProfileInsights.TopCPUFunctions) == 0 && len(in.ProfileInsights.TopHeapFuncs) == 0 {
				return false
			}
			in.ProfileInsights.TopCPUFunctions = nil
			in.ProfileInsights.TopCPUPackages = nil
			in.ProfileInsights.TopHeapFuncs = nil
			in.ProfileInsights.TopAllocFuncs = nil
			return true
		},
	}
	for {
		data, err := json.MarshalIndent(in, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal AI input: %w", err)
		}
		if len(data) <= maxAIInputSizeBytes {
			return data, nil
		}
		trimmed := false
		for _, step := range shrink {
			if step(in) {
				trimmed = true
				break
			}
		}
		if !trimmed {
			return nil, fmt.Errorf("AI input too large (%d bytes > %d bytes max)", len(data), maxAIInputSizeBytes)
		}
	}
}

func trimDeltas(in *AIInput, decreases bool, keep int) bool {
	trimmed := false
	for key, change := range in.ProfileChanges {
		list := change.TopIncreases
		if decreases {
			list = change.TopDecreases
		}
		if len(list) > keep {
			list = list[:keep]
			trimmed = true
		}
		if decreases {
			change.TopDecreases = list
		} else {
			change.TopIncreases = list
		}
		in.ProfileChanges[key] = change
	}
	return trimmed
}

func trimHotspots(in *AIInput, keep int) bool {
	if in.History == nil {
		return false
	}
	trimmed := false
	cut := func(items []Hotspot) []Hotspot {
		if len(items) > keep {
			trimmed = true
			return items[:keep]
		}
		return items
	}
	in.History.PersistentHotspots = cut(in.History.PersistentHotspots)
	in.History.NewHotspots = cut(in.History.NewHotspots)
	in.History.GrowingHotspots = cut(in.History.GrowingHotspots)
	return trimmed
}

type AIAnalysis struct {
	Verdict         string            `json:"verdict"`
	Headline        string            `json:"headline"`
	KeyNumbers      []AIKeyNumber     `json:"key_numbers,omitempty"`
	MetricNotes     []AIMetricNote    `json:"metric_notes,omitempty"`
	Findings        []AIFinding       `json:"findings,omitempty"`
	NotableChanges  []AINotableChange `json:"notable_changes,omitempty"`
	Recommendations []string          `json:"recommendations,omitempty"`
	Confidence      string            `json:"confidence"`
}

type AIMetricNote struct {
	Metric string `json:"metric"`
	Note   string `json:"note"`
}

type AIFinding struct {
	ID          string     `json:"id"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Evidence    string     `json:"evidence,omitempty"`
	Location    AILocation `json:"location"`
	Explanation string     `json:"explanation,omitempty"`
	FixPrompt   string     `json:"fix_prompt,omitempty"`
}

type AILocation struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}

type AIKeyNumber struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type AINotableChange struct {
	Function    string `json:"function"`
	Profile     string `json:"profile"`
	Change      string `json:"change"`
	LikelyCause string `json:"likely_cause"`
	SourceRef   string `json:"source_ref,omitempty"`
}

type AIProfileInsights struct {
	CPUCores        float64                 `json:"cpu_cores"`
	CPUTimeMS       int64                   `json:"cpu_time_ms"`
	HeapInUseMB     int64                   `json:"heap_inuse_mb"`
	TotalAllocGB    float64                 `json:"total_alloc_gb"`
	GoroutineCount  int64                   `json:"goroutine_count"`
	TopCPUFunctions []string                `json:"top_cpu_functions,omitempty"`
	TopCPUPackages  []string                `json:"top_cpu_packages,omitempty"`
	TopHeapFuncs    []string                `json:"top_heap_funcs,omitempty"`
	TopAllocFuncs   []string                `json:"top_alloc_funcs,omitempty"`
	HasBaseline     bool                    `json:"has_baseline"`
	Deltas          *AIProfileInsightDeltas `json:"deltas,omitempty"`
}

type AIProfileInsightDeltas struct {
	CPUCores       float64 `json:"cpu_cores_delta"`
	HeapInUseMB    int64   `json:"heap_inuse_mb_delta"`
	GoroutineCount int64   `json:"goroutine_count_delta"`
}
