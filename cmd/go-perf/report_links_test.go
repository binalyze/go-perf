package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCursorPromptLinksAndLongPrompt(t *testing.T) {
	app, web, ok := cursorPromptLinks("speed up pkg.Work in a.go")
	if !ok || !strings.HasPrefix(app, "cursor://anysphere.cursor-deeplink/prompt?text=") {
		t.Fatalf("app link = %q ok=%v", app, ok)
	}
	if !strings.HasPrefix(web, "https://cursor.com/link/prompt?text=") {
		t.Fatalf("web link = %q", web)
	}
	if strings.Contains(app, " ") {
		t.Fatal("prompt was not escaped")
	}

	views := findingViews(&AIAnalysis{
		Verdict: "warning", Confidence: "medium",
		Findings: []AIFinding{{
			ID: "F1", Severity: "medium", Title: "Hot path",
			FixPrompt: strings.Repeat("x", maxPromptLinkLen),
			Location:  AILocation{Function: "example.com/app.Work"},
		}},
	})
	if len(views) != 1 || !views[0].PromptTooLong || views[0].CursorURL != "" {
		t.Fatalf("long prompt view = %+v, want copy-only", views)
	}
	if findingIDFor(views, "example.com/app.Work") != "F1" {
		t.Fatal("finding badge did not match the function")
	}
}

func TestReportRendersTrendsAndFindings(t *testing.T) {
	runDir := t.TempDir()
	meta := &RunMetadata{
		RunID: "run-1", Timestamp: time.Now().UTC(), ProductName: "example-app",
		DurationMS: 1000, Success: true, TestCase: "suite",
	}
	if err := saveMetadata(runDir, meta); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	history := &HistoryAnalysis{
		RunCount: 4,
		Metrics: []MetricStat{{
			Name: "duration_ms", Unit: "ms", Median: 900, MAD: 20, Current: 1000,
			Label: "stable", History: []float64{880, 900, 910, 920},
		}},
	}
	if err := saveHistory(runDir, history); err != nil {
		t.Fatalf("save history: %v", err)
	}
	analysis := &AIAnalysis{
		Verdict: "warning", Headline: "One hotspot is growing", Confidence: "medium",
		Findings: []AIFinding{{
			ID: "F1", Class: "improvement", Severity: "medium", Title: "Busy worker",
			Evidence: "cpu share 30%", Explanation: "The loop allocates per item.",
			FixPrompt: "In worker.go, reuse the buffer.",
			Location:  AILocation{File: "worker.go", Line: 40, Function: "app.Work"},
		}},
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		t.Fatalf("marshal analysis: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "ai-analysis.json"), data, 0o644); err != nil {
		t.Fatalf("write analysis: %v", err)
	}

	htmlPath := filepath.Join(t.TempDir(), "report.html")
	if err := generateReport(runDir, htmlPath, nil, "", nil); err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	html := string(body)
	for _, want := range []string{"Trends (last 4 runs)", "Audit findings", "Fix in Cursor", "cursor://anysphere.cursor-deeplink/prompt?text=", "Open in Claude Code", "Busy worker", "<svg class=\"spark\""} {
		if !strings.Contains(html, want) {
			t.Errorf("report missing %q", want)
		}
	}
}
