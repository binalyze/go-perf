package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	historyFileName     = "history.json"
	defaultHistorySize  = 10
	noisyCV             = 0.25
	regressionZScore    = 3.0
	persistentFraction  = 0.8
	maxHotspotsReported = 10
)

// MetricStat describes one metric across the history window plus the current run.
// History values are oldest first and do not include Current.
type MetricStat struct {
	Name                  string    `json:"name"`
	Unit                  string    `json:"unit"`
	Median                float64   `json:"median"`
	MAD                   float64   `json:"mad"`
	Min                   float64   `json:"min"`
	Max                   float64   `json:"max"`
	P90                   float64   `json:"p90"`
	CV                    float64   `json:"cv"`
	ZScore                float64   `json:"z_score"`
	Slope                 float64   `json:"slope"`
	Current               float64   `json:"current"`
	ChangeVsMedianPercent float64   `json:"change_vs_median_percent"`
	Label                 string    `json:"label"`
	Gates                 bool      `json:"gates"`
	History               []float64 `json:"history"`
}

// Hotspot is a function that stands out across the history window.
type Hotspot struct {
	Function     string  `json:"function"`
	Profile      string  `json:"profile"`
	Appearances  int     `json:"appearances"`
	Runs         int     `json:"runs"`
	CurrentShare float64 `json:"current_share"`
	MedianShare  float64 `json:"median_share"`
}

// HistoryAnalysis is the noise-aware view of the last N runs.
type HistoryAnalysis struct {
	RunCount           int          `json:"run_count"`
	Metrics            []MetricStat `json:"metrics"`
	PersistentHotspots []Hotspot    `json:"persistent_hotspots"`
	NewHotspots        []Hotspot    `json:"new_hotspots"`
	GrowingHotspots    []Hotspot    `json:"growing_hotspots"`
	EnvNote            string       `json:"env_note,omitempty"`
	LatestRunID        string       `json:"latest_run_id,omitempty"`
	LatestDir          string       `json:"-"`
}

type historyRun struct {
	dir     string
	summary *RunSummary
}

// listHistoryDirs returns run directories, newest first, capped at size.
// A directory that itself holds metadata.json or summary.json is one run
// (the deprecated --baseline-dir shape). Otherwise each child directory is a run.
func listHistoryDirs(dir string, size int) ([]string, error) {
	if dir == "" || dir == "none" {
		return nil, nil
	}
	if size <= 0 {
		size = defaultHistorySize
	}
	if isRunDir(dir) {
		return []string{dir}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read history dir %s: %w", dir, err)
	}
	var runs []historyRun
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(dir, entry.Name())
		if !isRunDir(child) {
			continue
		}
		summary, err := loadRunSummary(child)
		if err != nil {
			fmt.Printf("[ANALYSIS] WARNING: skipping history run %s: %v\n", child, err)
			continue
		}
		if !summary.Success && summary.DurationMS == 0 && len(summary.Profiles) == 0 {
			continue
		}
		runs = append(runs, historyRun{dir: child, summary: summary})
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].summary.Timestamp > runs[j].summary.Timestamp
	})
	if len(runs) > size {
		runs = runs[:size]
	}
	dirs := make([]string, len(runs))
	for i, run := range runs {
		dirs[i] = run.dir
	}
	return dirs, nil
}

func isRunDir(dir string) bool {
	if fileExists(filepath.Join(dir, "metadata.json")) || fileExists(filepath.Join(dir, summaryFile)) {
		return true
	}
	return false
}

// buildHistory compares current against history run directories (newest first).
func buildHistory(dirs []string, current *RunSummary, threshold float64) (*HistoryAnalysis, error) {
	if current == nil {
		return nil, fmt.Errorf("current run summary is required")
	}
	analysis := &HistoryAnalysis{
		PersistentHotspots: []Hotspot{},
		NewHotspots:        []Hotspot{},
		GrowingHotspots:    []Hotspot{},
		Metrics:            []MetricStat{},
	}
	var past []historyRun
	for _, dir := range dirs {
		summary, err := loadRunSummary(dir)
		if err != nil {
			fmt.Printf("[ANALYSIS] WARNING: skipping history run %s: %v\n", dir, err)
			continue
		}
		past = append(past, historyRun{dir: dir, summary: summary})
	}
	sort.Slice(past, func(i, j int) bool {
		return past[i].summary.Timestamp > past[j].summary.Timestamp
	})
	analysis.RunCount = len(past)
	if len(past) > 0 {
		analysis.LatestDir = past[0].dir
		analysis.LatestRunID = past[0].summary.RunID
	}

	// Oldest first for slopes and sparklines.
	ordered := append([]historyRun(nil), past...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].summary.Timestamp < ordered[j].summary.Timestamp
	})

	type metricSpec struct {
		name  string
		unit  string
		gates bool
		value func(*RunSummary) float64
	}
	specs := []metricSpec{
		{"duration_ms", "ms", true, func(s *RunSummary) float64 { return float64(s.DurationMS) }},
		{"avg_cores", "cores", true, func(s *RunSummary) float64 { return s.AvgCores }},
		{"total_alloc_bytes", "bytes", true, func(s *RunSummary) float64 { return float64(s.TotalAllocBytes) }},
		{"heap_inuse_bytes", "bytes", false, func(s *RunSummary) float64 { return float64(s.HeapInUseBytes) }},
		{"goroutine_count", "count", false, func(s *RunSummary) float64 { return float64(s.GoroutineCount) }},
	}
	for _, spec := range specs {
		history := make([]float64, 0, len(ordered))
		for _, run := range ordered {
			history = append(history, spec.value(run.summary))
		}
		stat := robustMetric(spec.name, spec.unit, spec.gates, history, spec.value(current), threshold)
		analysis.Metrics = append(analysis.Metrics, stat)
	}

	analysis.PersistentHotspots, analysis.NewHotspots, analysis.GrowingHotspots = hotspots(ordered, current)
	analysis.EnvNote = environmentNote(ordered, current)
	return analysis, nil
}

func saveHistory(runDir string, analysis *HistoryAnalysis) error {
	if analysis == nil {
		return nil
	}
	data, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, historyFileName), data, 0o644); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

func loadHistoryFile(runDir string) (*HistoryAnalysis, error) {
	data, err := os.ReadFile(filepath.Join(runDir, historyFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var analysis HistoryAnalysis
	if err := json.Unmarshal(data, &analysis); err != nil {
		return nil, fmt.Errorf("parse history: %w", err)
	}
	return &analysis, nil
}

// robustMetric classifies current against history.
// Labels: regressed, improved, creeping, noisy, stable.
// Fewer than 3 history points cannot support a robust label; those are "stable"
// unless the single-point change already exceeds the threshold, which is
// reported as regressed/improved only for gating metrics so a one-run baseline
// still surfaces an obvious jump.
func robustMetric(name, unit string, gates bool, history []float64, current, threshold float64) MetricStat {
	stat := MetricStat{
		Name:    name,
		Unit:    unit,
		Current: current,
		Gates:   gates,
		History: append([]float64(nil), history...),
		Label:   "stable",
	}
	if len(history) == 0 {
		return stat
	}
	stat.Median = median(history)
	stat.MAD = mad(history, stat.Median)
	stat.Min, stat.Max = minMax(history)
	stat.P90 = percentile(history, 0.9)
	stat.CV = robustCV(stat.Median, stat.MAD, history)
	stat.Slope = theilSenSlope(history)
	if stat.Median != 0 {
		stat.ChangeVsMedianPercent = (current - stat.Median) / math.Abs(stat.Median) * 100
	}
	denom := 1.4826 * stat.MAD
	switch {
	case denom > 0:
		stat.ZScore = (current - stat.Median) / denom
	case current > stat.Median:
		// A history with no spread makes any increase extreme.
		stat.ZScore = regressionZScore + 1
	case current < stat.Median:
		stat.ZScore = -(regressionZScore + 1)
	}

	if len(history) < 3 {
		if gates && stat.ChangeVsMedianPercent > threshold {
			stat.Label = "regressed"
		} else if gates && stat.ChangeVsMedianPercent < -threshold {
			stat.Label = "improved"
		}
		return stat
	}

	regressed := gates && stat.ChangeVsMedianPercent > threshold && stat.ZScore > regressionZScore
	improved := gates && stat.ChangeVsMedianPercent < -threshold && stat.ZScore < -regressionZScore
	switch {
	case regressed:
		stat.Label = "regressed"
	case improved:
		stat.Label = "improved"
	case isCreeping(stat, threshold):
		stat.Label = "creeping"
	case stat.CV > noisyCV:
		stat.Label = "noisy"
	default:
		stat.Label = "stable"
	}
	return stat
}

// isCreeping reports a steady drift that the z-score has not yet called a
// regression: the Theil-Sen slope, projected across the window, moves the
// metric by more than the threshold relative to the median.
func isCreeping(stat MetricStat, threshold float64) bool {
	n := len(stat.History)
	if n < 3 || stat.Median == 0 || stat.Slope <= 0 {
		return false
	}
	projected := stat.Slope * float64(n-1)
	return projected/math.Abs(stat.Median)*100 > threshold && stat.ZScore < regressionZScore
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func mad(values []float64, med float64) float64 {
	devs := make([]float64, len(values))
	for i, v := range values {
		devs[i] = math.Abs(v - med)
	}
	return median(devs)
}

func minMax(values []float64) (float64, float64) {
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	return lo, hi
}

func percentile(values []float64, p float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func robustCV(med, madValue float64, values []float64) float64 {
	if med != 0 {
		return madValue / math.Abs(med)
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	if mean == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum/float64(len(values))) / math.Abs(mean)
}

// theilSenSlope is the median of pairwise slopes. One point has slope 0.
func theilSenSlope(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	slopes := make([]float64, 0, len(values)*(len(values)-1)/2)
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			slopes = append(slopes, (values[j]-values[i])/float64(j-i))
		}
	}
	return median(slopes)
}

type sharePoint struct {
	fn      string
	profile string
	shares  []float64 // aligned with runs, 0 if absent
	inTop10 int
	inSeen  bool
}

func hotspots(ordered []historyRun, current *RunSummary) (persistent, fresh, growing []Hotspot) {
	runs := make([]*RunSummary, 0, len(ordered)+1)
	for _, run := range ordered {
		runs = append(runs, run.summary)
	}
	runs = append(runs, current)
	n := len(runs)
	if n == 0 {
		return []Hotspot{}, []Hotspot{}, []Hotspot{}
	}

	type key struct{ profile, fn string }
	acc := map[key]*sharePoint{}
	for i, run := range runs {
		for _, profileName := range []string{"cpu", "heap", "allocs", "goroutine"} {
			prof, ok := run.Profiles[profileName]
			if !ok || prof.Total <= 0 {
				continue
			}
			limit := hotspotTopN
			if limit > len(prof.Top) {
				limit = len(prof.Top)
			}
			seenLimit := historySeenTop
			if seenLimit > len(prof.Top) {
				seenLimit = len(prof.Top)
			}
			for rank, fn := range prof.Top {
				k := key{profileName, fn.Function}
				pt, ok := acc[k]
				if !ok {
					pt = &sharePoint{fn: fn.Function, profile: profileName, shares: make([]float64, n)}
					acc[k] = pt
				}
				pt.shares[i] = float64(fn.Flat) / float64(prof.Total)
				if rank < hotspotTopN {
					pt.inTop10++
				}
				if i < len(ordered) && rank < seenLimit {
					pt.inSeen = true
				}
			}
		}
	}

	currentIdx := n - 1
	for _, pt := range acc {
		historyShares := pt.shares[:currentIdx]
		currentShare := pt.shares[currentIdx]
		appearances := 0
		for _, share := range pt.shares {
			if share > 0 {
				appearances++
			}
		}
		h := Hotspot{
			Function:     pt.fn,
			Profile:      pt.profile,
			Appearances:  appearances,
			Runs:         n,
			CurrentShare: currentShare,
			MedianShare:  median(nonZero(historyShares)),
		}
		if n > 0 && float64(pt.inTop10)/float64(n) >= persistentFraction && currentShare > 0 {
			persistent = append(persistent, h)
		}
		if currentShare > 0 && !pt.inSeen && len(ordered) > 0 {
			fresh = append(fresh, h)
		}
		if len(ordered) >= 3 && currentShare > 0 {
			series := append([]float64(nil), historyShares...)
			slope := theilSenSlope(series)
			med := median(nonZero(series))
			if slope > 0 && med > 0 && currentShare > med*1.15 {
				growing = append(growing, h)
			}
		}
	}
	sortHotspots(persistent)
	sortHotspots(fresh)
	sortHotspots(growing)
	return capHotspots(persistent), capHotspots(fresh), capHotspots(growing)
}

func nonZero(values []float64) []float64 {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func sortHotspots(items []Hotspot) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CurrentShare == items[j].CurrentShare {
			return items[i].Function < items[j].Function
		}
		return items[i].CurrentShare > items[j].CurrentShare
	})
}

func capHotspots(items []Hotspot) []Hotspot {
	if items == nil {
		return []Hotspot{}
	}
	if len(items) > maxHotspotsReported {
		return items[:maxHotspotsReported]
	}
	return items
}

func environmentNote(ordered []historyRun, current *RunSummary) string {
	if len(ordered) == 0 || current == nil {
		return ""
	}
	var agents, models []string
	for _, run := range ordered {
		if run.summary.AgentName != "" {
			agents = append(agents, run.summary.AgentName)
		}
		if run.summary.CPUModel != "" {
			models = append(models, run.summary.CPUModel)
		}
	}
	var notes []string
	if agent := modeOf(agents); agent != "" && current.AgentName != "" && current.AgentName != agent {
		notes = append(notes, fmt.Sprintf("agent changed from %s to %s", agent, current.AgentName))
	}
	if model := modeOf(models); model != "" && current.CPUModel != "" && current.CPUModel != model {
		notes = append(notes, fmt.Sprintf("CPU model changed from %s to %s", model, current.CPUModel))
	}
	if len(notes) == 0 {
		return ""
	}
	out := "Environment differs from most of the history: " + notes[0]
	for _, note := range notes[1:] {
		out += "; " + note
	}
	return out + ". Treat small differences as possible noise."
}

func modeOf(values []string) string {
	counts := map[string]int{}
	var best string
	bestN := 0
	for _, v := range values {
		counts[v]++
		if counts[v] > bestN {
			best = v
			bestN = counts[v]
		}
	}
	return best
}
