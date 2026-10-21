package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/pprof/profile"
)

const (
	summaryFile    = "summary.json"
	summaryTopN    = 50
	hotspotTopN    = 10
	historySeenTop = 50
)

// FunctionStat is one function's flat and cumulative value in a profile.
type FunctionStat struct {
	Function string `json:"function"`
	Flat     int64  `json:"flat"`
	Cum      int64  `json:"cum"`
}

// ProfileSummary is the compact view of one profile type.
type ProfileSummary struct {
	Unit  string         `json:"unit"`
	Total int64          `json:"total"`
	Top   []FunctionStat `json:"top"`
}

// RunSummary is the per-run record stored next to profiles.zip so later
// audits do not have to re-parse every profile.
type RunSummary struct {
	RunID           string                    `json:"run_id"`
	Timestamp       string                    `json:"timestamp"`
	DurationMS      int64                     `json:"duration_ms"`
	AvgCores        float64                   `json:"avg_cores"`
	TotalAllocBytes int64                     `json:"total_alloc_bytes"`
	HeapInUseBytes  int64                     `json:"heap_inuse_bytes"`
	GoroutineCount  int64                     `json:"goroutine_count"`
	AgentName       string                    `json:"agent_name,omitempty"`
	CPUModel        string                    `json:"cpu_model,omitempty"`
	CPUCores        int                       `json:"cpu_cores,omitempty"`
	CommitID        string                    `json:"commit_id,omitempty"`
	Success         bool                      `json:"success"`
	Profiles        map[string]ProfileSummary `json:"profiles"`
}

// writeRunSummary builds summary.json from metadata and profiles and writes it.
// An existing summary is replaced so the current run always reflects its profiles.
func writeRunSummary(runDir string, meta *RunMetadata) (*RunSummary, error) {
	summary, err := buildRunSummary(runDir, meta)
	if err != nil {
		return nil, err
	}
	if err := saveRunSummary(runDir, summary); err != nil {
		return nil, err
	}
	return summary, nil
}

// loadRunSummary reads summary.json when it exists and otherwise builds it
// from profiles.zip (and writes the result). A run with neither is skipped.
func loadRunSummary(runDir string) (*RunSummary, error) {
	summary, err := readRunSummary(runDir)
	if err == nil {
		return summary, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	meta, merr := loadMetadata(runDir)
	if merr != nil {
		return nil, fmt.Errorf("run %s has no summary.json or metadata.json: %w", runDir, merr)
	}
	return writeRunSummary(runDir, meta)
}

func readRunSummary(runDir string) (*RunSummary, error) {
	data, err := os.ReadFile(filepath.Join(runDir, summaryFile))
	if err != nil {
		return nil, err
	}
	var summary RunSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("parse %s: %w", summaryFile, err)
	}
	if summary.Profiles == nil {
		summary.Profiles = map[string]ProfileSummary{}
	}
	return &summary, nil
}

func saveRunSummary(runDir string, summary *RunSummary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, summaryFile), data, 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func buildRunSummary(runDir string, meta *RunMetadata) (*RunSummary, error) {
	summary := &RunSummary{
		RunID:      meta.RunID,
		Timestamp:  meta.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
		DurationMS: meta.DurationMS,
		AgentName:  meta.AgentName,
		CPUModel:   meta.CPUModel,
		CPUCores:   meta.CPUCores,
		CommitID:   meta.ProductCommitID,
		Success:    meta.Success,
		Profiles:   map[string]ProfileSummary{},
	}
	if summary.RunID == "" {
		summary.RunID = filepath.Base(runDir)
	}

	profileDir, cleanup, err := getProfileDir(runDir)
	if err != nil {
		// Metadata-only runs still produce a summary; profiles are optional.
		return summary, nil
	}
	if cleanup {
		defer os.RemoveAll(profileDir)
	}

	if cpu, err := summarizeProfile(filepath.Join(profileDir, "cpu.pprof"), "nanoseconds", cpuSampleIndex, true); err == nil {
		summary.Profiles["cpu"] = cpu
		if meta.DurationMS > 0 {
			summary.AvgCores = float64(cpu.Total) / (float64(meta.DurationMS) * 1e6)
		}
	}
	if heap, err := summarizeProfile(filepath.Join(profileDir, "heap.pprof"), "bytes", namedSampleIndex("inuse_space", "bytes"), false); err == nil {
		summary.Profiles["heap"] = heap
		summary.HeapInUseBytes = heap.Total
	}
	if allocs, err := summarizeProfile(filepath.Join(profileDir, "allocs.pprof"), "bytes", namedSampleIndex("alloc_space", "bytes"), false); err == nil {
		summary.Profiles["allocs"] = allocs
		summary.TotalAllocBytes = allocs.Total
	}
	if gr, err := summarizeProfile(filepath.Join(profileDir, "goroutine.pprof"), "count", namedSampleIndex("goroutine", "count"), false); err == nil {
		summary.Profiles["goroutine"] = gr
		summary.GoroutineCount = gr.Total
	}
	return summary, nil
}

func cpuSampleIndex(prof *profile.Profile) int {
	for i, st := range prof.SampleType {
		if st.Type == "cpu" && st.Unit == "nanoseconds" {
			return i
		}
	}
	if len(prof.SampleType) > 0 {
		return 0
	}
	return -1
}

func namedSampleIndex(typ, unit string) func(*profile.Profile) int {
	return func(prof *profile.Profile) int {
		for i, st := range prof.SampleType {
			if st.Type == typ && (unit == "" || st.Unit == unit) {
				return i
			}
		}
		return -1
	}
}

func summarizeProfile(path, unit string, indexOf func(*profile.Profile) int, scaleByPeriod bool) (ProfileSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return ProfileSummary{}, err
	}
	defer f.Close()
	prof, err := profile.Parse(f)
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("parse %s: %w", path, err)
	}
	idx := indexOf(prof)
	if idx < 0 {
		return ProfileSummary{}, fmt.Errorf("%s: sample type not found", path)
	}

	flat := map[string]int64{}
	cum := map[string]int64{}
	var total int64
	scale := int64(1)
	if scaleByPeriod && prof.Period > 0 {
		scale = prof.Period
	}
	for _, sample := range prof.Sample {
		if idx >= len(sample.Value) {
			continue
		}
		value := sample.Value[idx] * scale
		total += value
		if len(sample.Location) > 0 && len(sample.Location[0].Line) > 0 && sample.Location[0].Line[0].Function != nil {
			flat[sample.Location[0].Line[0].Function.Name] += value
		}
		seen := map[string]bool{}
		for _, loc := range sample.Location {
			for _, line := range loc.Line {
				if line.Function == nil || seen[line.Function.Name] {
					continue
				}
				seen[line.Function.Name] = true
				cum[line.Function.Name] += value
			}
		}
	}

	type row struct {
		fn   string
		flat int64
		cum  int64
	}
	rows := make([]row, 0, len(flat))
	for fn, v := range flat {
		rows = append(rows, row{fn, v, cum[fn]})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].flat == rows[j].flat {
			return rows[i].fn < rows[j].fn
		}
		return rows[i].flat > rows[j].flat
	})
	top := make([]FunctionStat, 0, min(summaryTopN, len(rows)))
	for i := 0; i < len(rows) && i < summaryTopN; i++ {
		top = append(top, FunctionStat{Function: rows[i].fn, Flat: rows[i].flat, Cum: rows[i].cum})
	}
	return ProfileSummary{Unit: unit, Total: total, Top: top}, nil
}
