package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/urfave/cli/v2"
)

//go:embed templates/*.html
var templatesFS embed.FS

type ReportData struct {
	RunID                string
	Timestamp            string
	ProductName          string
	ProductVersion       string
	ProductCommit        string
	TestCase             string
	TestCount            int64
	TestPassCount        int64
	DurationMS           int64
	ProfilesCollected    bool
	Success              bool
	ErrorMessage         string
	Comparison           *ComparisonResult
	ProfilesPath         string
	MetadataPath         string
	CPUProfileDuration   int
	CPUProfileDurationMS int64
	BuildURL             string
	AIAnalysis           *AIAnalysis
	ExecutiveSummary     ExecutiveSummary
	Insights             *ProfileInsights
	History              *HistoryAnalysis
	Findings             []FindingView
	ProfileFiles         []string
	AgentName            string
	CPUCores             int
	Diagnostics          DiagnosticsData
}

type DiagnosticsData struct {
	BaselineBuild         string
	BaselineAgent         string
	BaselineSkipped       []string
	ArtifactFormat        string
	CPUProfileError       string
	HeapProfileError      string
	AllocsProfileError    string
	GoroutineProfileError string
	AIStatus              string
	AIUnavailableReason   string
}

type ExecutiveSummary struct {
	Verdict             string
	Headline            string
	KeyNumbers          []ExecutiveKeyNumber
	NotableChanges      []ExecutiveNotableChange
	Recommendations     []string
	Confidence          string
	AIUnavailableReason string
}

type ExecutiveKeyNumber struct {
	Label string
	Value string
}

type ExecutiveNotableChange struct {
	Function    string
	Profile     string
	Change      string
	LikelyCause string
	SourceRef   string
}

func reportCommand(c *cli.Context) error {
	workDir := c.String(flagWorkDir)
	runID := c.String("run-id")

	if runID == "" {
		var err error
		runID, err = findLatestRun(workDir)
		if err != nil {
			return fmt.Errorf("failed to find latest run: %w", err)
		}
		if runID == "" {
			return fmt.Errorf("no runs found in %s", workDir)
		}
	}

	runDir := filepath.Join(workDir, runID)
	htmlPath := filepath.Join(workDir, c.String(flagOutputHTML))

	return generateReport(runDir, htmlPath, nil, "", nil)
}

func generateReport(runDir, htmlPath string, comparison *ComparisonResult, buildURL string, insights *ProfileInsights) error {
	metadata, err := loadMetadata(runDir)
	if err != nil {
		return fmt.Errorf("failed to load metadata: %w", err)
	}

	// Extract CPU profile duration from profiles.zip if available
	cpuDurationMS := extractCPUProfileDuration(runDir)

	// Load AI analysis if available
	aiAnalysis, aiErr := loadAIAnalysis(runDir)
	if aiErr != nil {
		logger.Warnf("Failed to load AI analysis: %v", aiErr)
		aiAnalysis = nil
	}

	// Build executive summary
	execSummary := buildExecutiveSummary(comparison, aiAnalysis, metadata, insights)

	// List profile files from profiles.zip
	profileFiles := listProfileFiles(runDir)

	// Build diagnostics
	diagnostics := DiagnosticsData{
		ArtifactFormat: "profiles.zip + metadata.json",
	}
	if comparison != nil && comparison.BaselineBuildID != "" {
		diagnostics.BaselineBuild = fmt.Sprintf("Build %s (%s)", comparison.BaselineBuildID, comparison.BaselineTimestamp)
		diagnostics.BaselineAgent = comparison.BaselineAgentName
	}

	if aiAnalysis != nil {
		diagnostics.AIStatus = "Available"
	} else {
		diagnostics.AIStatus = "Unavailable"
		if aiErr != nil {
			diagnostics.AIUnavailableReason = aiErr.Error()
		} else {
			diagnostics.AIUnavailableReason = "AI analysis not generated"
		}
	}

	history, herr := loadHistoryFile(runDir)
	if herr != nil {
		logger.Warnf("Failed to load history: %v", herr)
	}
	findings := findingViews(aiAnalysis)

	data := ReportData{
		RunID:                metadata.RunID,
		Timestamp:            metadata.Timestamp.Format("2006-01-02 15:04:05 MST"),
		ProductName:          metadata.ProductName,
		ProductVersion:       metadata.ProductVersion,
		ProductCommit:        metadata.ProductCommitID,
		TestCase:             metadata.TestCase,
		TestCount:            metadata.TestCount,
		TestPassCount:        metadata.TestPassCount,
		DurationMS:           metadata.DurationMS,
		ProfilesCollected:    metadata.ProfilesCollected,
		Success:              metadata.Success,
		ErrorMessage:         metadata.ErrorMessage,
		Comparison:           comparison,
		ProfilesPath:         filepath.Join(metadata.RunID, "profiles.zip"),
		MetadataPath:         filepath.Join(metadata.RunID, "metadata.json"),
		CPUProfileDuration:   metadata.CPUProfileDuration,
		CPUProfileDurationMS: cpuDurationMS,
		BuildURL:             buildURL,
		AIAnalysis:           aiAnalysis,
		ExecutiveSummary:     execSummary,
		Insights:             insights,
		History:              history,
		Findings:             findings,
		ProfileFiles:         profileFiles,
		AgentName:            metadata.AgentName,
		CPUCores:             metadata.CPUCores,
		Diagnostics:          diagnostics,
	}

	tmplContent, err := templatesFS.ReadFile("templates/report.html")
	if err != nil {
		return fmt.Errorf("failed to read template: %w", err)
	}

	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"ToUpper":             strings.ToUpper,
		"toUpper":             strings.ToUpper,
		"humanDuration":       humanDuration,
		"formatBytes":         formatBytes,
		"formatNumber":        formatNumber,
		"formatNanos":         formatNanoseconds,
		"formatPercent":       formatPercent,
		"formatDeltaBytes":    formatDeltaBytes,
		"formatDeltaNum":      formatDeltaNumber,
		"formatDeltaCores":    formatDeltaCores,
		"formatSampleValue":   formatSampleValue,
		"formatSampleDelta":   formatSampleDelta,
		"formatChangePercent": formatChangePercent,
		"trimFunc":            TrimFunctionName,
		"sparkline":           sparklineSVG,
		"labelClass":          metricLabelClass,
		"metricNote": func(name string) string {
			if aiAnalysis == nil {
				return ""
			}
			for _, note := range aiAnalysis.MetricNotes {
				if note.Metric == name {
					return note.Note
				}
			}
			return ""
		},
		"findingBadge": func(function string) template.HTML {
			id := findingIDFor(findings, function)
			if id == "" {
				return ""
			}
			return template.HTML(` <span class="tag">` + template.HTMLEscapeString(id) + `</span>`)
		},
		"contains": strings.Contains,
		"slice": func(s string, start, end int) string {
			if end > len(s) {
				end = len(s)
			}
			return s[start:end]
		},
		"divf": func(a, b interface{}) float64 {
			var af, bf float64
			switch v := a.(type) {
			case int:
				af = float64(v)
			case int64:
				af = float64(v)
			case float64:
				af = v
			default:
				return 0
			}
			switch v := b.(type) {
			case int:
				bf = float64(v)
			case int64:
				bf = float64(v)
			case float64:
				bf = v
			default:
				return 0
			}
			if bf == 0 {
				return 0
			}
			return af / bf
		},
		"printf": func(format string, v interface{}) string {
			return fmt.Sprintf(format, v)
		},
		"formatProfileDelta": func(profileType string, delta int64) string {
			switch profileType {
			case "cpu":
				return humanDuration(delta / 1000000) // ns to ms
			case "heap", "allocs":
				return formatBytes(delta)
			case "goroutine":
				return formatNumber(delta)
			default:
				return formatNumber(delta)
			}
		},
		"funcWithTitle": func(fullName string) template.HTML {
			trimmed := TrimFunctionName(fullName)
			if trimmed != fullName {
				// Escape for both attribute and content contexts
				escapedFull := template.HTMLEscapeString(fullName)
				// Additional escaping for attribute context (quotes)
				escapedFull = strings.ReplaceAll(escapedFull, `"`, `&quot;`)
				escapedTrimmed := template.HTMLEscapeString(trimmed)
				return template.HTML(fmt.Sprintf(`<span title="%s">%s</span>`, escapedFull, escapedTrimmed))
			}
			return template.HTML(template.HTMLEscapeString(fullName))
		},
		"now": func() time.Time {
			return time.Now()
		},
		"sub": func(a, b int64) int64 {
			return a - b
		},
	}).Parse(string(tmplContent))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to parse report template: %v\n", err)
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to execute report template: %v\n", err)
		return fmt.Errorf("failed to execute template: %w", err)
	}

	if err := os.WriteFile(htmlPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	logger.Infof("Report generated: %s", htmlPath)
	return nil
}

func findLatestRun(workDir string) (string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", err
	}

	var runs []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[:4] == "run-" {
			runs = append(runs, entry.Name())
		}
	}

	if len(runs) == 0 {
		return "", nil
	}

	sort.Strings(runs)
	return runs[len(runs)-1], nil
}

// humanDuration converts milliseconds to a human-readable duration string.
// Examples: "41m 03s", "2.4s", "850ms", "0ms", "-2.4s"
// Negative values are formatted as the absolute value with a '-' prefix.
func humanDuration(ms int64) string {
	if ms == 0 {
		return "0ms"
	}

	// Handle negative values
	negative := ms < 0
	if negative {
		ms = -ms
	}

	d := time.Duration(ms) * time.Millisecond
	var result string

	// For durations >= 1 minute, show minutes and seconds
	if d >= time.Minute {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		result = fmt.Sprintf("%dm %02ds", minutes, seconds)
	} else if d >= time.Second {
		// For durations >= 1 second, show seconds with decimal
		seconds := float64(ms) / 1000.0
		result = fmt.Sprintf("%.1fs", seconds)
	} else {
		// For durations < 1 second, show milliseconds
		result = fmt.Sprintf("%dms", ms)
	}

	if negative {
		result = "-" + result
	}

	return result
}

// extractCPUProfileDuration extracts the CPU profile duration from profiles.zip
// by parsing cpu.pprof with github.com/google/pprof/profile.
// Returns duration in milliseconds, or 0 if extraction fails.
func extractCPUProfileDuration(runDir string) int64 {
	profilesPath := filepath.Join(runDir, "profiles.zip")
	if !fileExists(profilesPath) {
		return 0
	}

	// Extract cpu.pprof from the zip
	extractDir := filepath.Join(runDir, "profiles_extracted_temp")
	defer os.RemoveAll(extractDir)

	if err := extractZip(profilesPath, extractDir); err != nil {
		logger.Warnf("Failed to extract profiles.zip for CPU duration: %v", err)
		return 0
	}

	cpuProfilePath := filepath.Join(extractDir, "cpu.pprof")
	if !fileExists(cpuProfilePath) {
		return 0
	}

	// Parse the CPU profile
	f, err := os.Open(cpuProfilePath)
	if err != nil {
		logger.Warnf("Failed to open cpu.pprof: %v", err)
		return 0
	}
	defer f.Close()

	// Use github.com/google/pprof/profile to parse
	prof, err := parsePprofProfile(f)
	if err != nil {
		logger.Warnf("Failed to parse cpu.pprof: %v", err)
		return 0
	}

	if prof.DurationNanos == 0 {
		return 0
	}

	// Convert nanoseconds to milliseconds
	durationMS := prof.DurationNanos / 1000000
	return durationMS
}

// parsePprofProfile parses a pprof profile from a reader.
func parsePprofProfile(r io.Reader) (*profile.Profile, error) {
	return profile.Parse(r)
}

// buildExecutiveSummary creates an executive summary from AI analysis or deterministic data.
func buildExecutiveSummary(comparison *ComparisonResult, aiAnalysis *AIAnalysis, metadata *RunMetadata, insights *ProfileInsights) ExecutiveSummary {
	if aiAnalysis != nil {
		// Use AI-generated summary
		summary := ExecutiveSummary{
			Verdict:    aiAnalysis.Verdict,
			Headline:   aiAnalysis.Headline,
			Confidence: aiAnalysis.Confidence,
		}

		// Convert AI key numbers
		for _, kn := range aiAnalysis.KeyNumbers {
			summary.KeyNumbers = append(summary.KeyNumbers, ExecutiveKeyNumber{
				Label: kn.Label,
				Value: kn.Value,
			})
		}

		// Convert AI notable changes
		for _, nc := range aiAnalysis.NotableChanges {
			summary.NotableChanges = append(summary.NotableChanges, ExecutiveNotableChange{
				Function:    nc.Function,
				Profile:     nc.Profile,
				Change:      nc.Change,
				LikelyCause: nc.LikelyCause,
				SourceRef:   nc.SourceRef,
			})
		}

		summary.Recommendations = aiAnalysis.Recommendations
		if len(aiAnalysis.Findings) > 0 {
			summary.KeyNumbers = append(summary.KeyNumbers, ExecutiveKeyNumber{
				Label: "Audit findings",
				Value: fmt.Sprintf("%d", len(aiAnalysis.Findings)),
			})
		}

		return summary
	}

	// Fallback: build deterministic summary
	summary := ExecutiveSummary{
		AIUnavailableReason: "AI analysis was not performed for this run",
	}

	if comparison != nil && comparison.BaselineAvailable {
		// Determine verdict based on regression flags
		if comparison.HasRegression {
			summary.Verdict = "regression"
			summary.Headline = fmt.Sprintf("Performance regression detected: %.1f%% increase in total duration", comparison.DurationChangePercent)
		} else if comparison.DurationChangePercent < -5.0 {
			summary.Verdict = "ok"
			summary.Headline = fmt.Sprintf("Performance improved: %.1f%% decrease in total duration", -comparison.DurationChangePercent)
		} else {
			summary.Verdict = "ok"
			summary.Headline = fmt.Sprintf("Performance stable: %.1f%% change in total duration", comparison.DurationChangePercent)
		}

		// Add key numbers
		summary.KeyNumbers = []ExecutiveKeyNumber{
			{
				Label: "Total Duration",
				Value: humanDuration(comparison.CurrentDurationMS),
			},
			{
				Label: "Baseline Duration",
				Value: humanDuration(comparison.BaselineDurationMS),
			},
			{
				Label: "Change",
				Value: fmt.Sprintf("%s (%.1f%%)", humanDuration(comparison.DurationChangeMS), comparison.DurationChangePercent),
			},
		}

		// Add insights if available
		if insights != nil {
			summary.KeyNumbers = append(summary.KeyNumbers, ExecutiveKeyNumber{
				Label: "CPU Utilization",
				Value: fmt.Sprintf("%.2f cores", insights.Headlines.CPUCores),
			})
			if insights.Headlines.HeapInUseBytes > 0 {
				summary.KeyNumbers = append(summary.KeyNumbers, ExecutiveKeyNumber{
					Label: "Heap In-Use",
					Value: formatBytes(insights.Headlines.HeapInUseBytes),
				})
			}
			if insights.Headlines.GoroutineCount > 0 {
				summary.KeyNumbers = append(summary.KeyNumbers, ExecutiveKeyNumber{
					Label: "Goroutines",
					Value: formatNumber(insights.Headlines.GoroutineCount),
				})
			}
		}
	} else {
		// No baseline
		summary.Verdict = "ok"
		summary.Headline = "Initial performance baseline established"
		summary.KeyNumbers = []ExecutiveKeyNumber{
			{
				Label: "Total Duration",
				Value: humanDuration(metadata.DurationMS),
			},
		}
	}

	return summary
}

// listProfileFiles returns the list of files in profiles.zip
func listProfileFiles(runDir string) []string {
	// Check if profiles.zip exists
	profilesZip := filepath.Join(runDir, "profiles.zip")
	if _, err := os.Stat(profilesZip); err == nil {
		// List files from zip
		r, err := zip.OpenReader(profilesZip)
		if err != nil {
			return nil
		}
		defer r.Close()

		var files []string
		for _, f := range r.File {
			if !f.FileInfo().IsDir() {
				files = append(files, f.Name)
			}
		}
		return files
	}

	// Check for loose *.pprof files and JSONs
	var files []string
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".pprof") ||
			name == "metadata.json" ||
			name == "test-timing.json" ||
			name == "ai-input.json" ||
			name == "ai-analysis.json" {
			files = append(files, name)
		}
	}

	return files
}
