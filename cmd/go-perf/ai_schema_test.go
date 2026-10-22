package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAIAnalysis(t *testing.T) {
	err := validateAIAnalysis(&AIAnalysis{Verdict: "nope", Confidence: "high"})
	if err == nil || !strings.Contains(err.Error(), "verdict") {
		t.Fatalf("validate = %v, want a verdict error", err)
	}

	analysis := &AIAnalysis{
		Verdict: "regression", Headline: "slower", Confidence: "high",
		Findings: []AIFinding{{
			ID: "F1", Class: "regression", Severity: "high", Title: "allocs",
			FixPrompt: "Reuse the buffer in alloc.go.",
			Location:  AILocation{File: "alloc.go", Line: 12},
		}},
	}
	if err := validateAIAnalysis(analysis); err != nil {
		t.Fatalf("validate: %v", err)
	}

	expected := &AIAnalysis{
		Verdict: "warning", Confidence: "low",
		Findings: []AIFinding{{
			ID: "F2", Class: "expected", Severity: "low", Title: "parser",
			Location: AILocation{File: "parse.go", Line: 4},
		}},
	}
	if err := validateAIAnalysis(expected); err != nil {
		t.Fatalf("expected finding: %v", err)
	}
	improvement := &AIAnalysis{
		Verdict: "warning", Confidence: "medium",
		Findings: []AIFinding{{
			ID: "F3", Class: "improvement", Severity: "medium", Title: "cache",
			Location: AILocation{File: "cache.go", Line: 8},
		}},
	}
	if err := validateAIAnalysis(improvement); err == nil || !strings.Contains(err.Error(), "fix prompt") {
		t.Fatalf("improvement without a fix = %v", err)
	}
}

func TestFitAIInputTruncates(t *testing.T) {
	change := AIProfileChange{ProfileType: "cpu"}
	for i := 0; i < 40; i++ {
		change.TopIncreases = append(change.TopIncreases, AIFunctionDelta{
			Function: strings.Repeat("example.com/app.Func", 80), ChangePercent: 10,
		})
		change.TopDecreases = append(change.TopDecreases, AIFunctionDelta{
			Function: strings.Repeat("example.com/app.Other", 80), ChangePercent: -10,
		})
	}
	in := &AIInput{
		Metadata:       AIMetadata{CurrentRunID: "r", TestCase: "suite"},
		ProfileChanges: map[string]AIProfileChange{"cpu": change},
	}
	// Force the budget down for this test by using a payload that exceeds it
	// only after we temporarily... the default budget is 60KB, so pad history too.
	in.History = &HistoryAnalysis{RunCount: 10}
	for i := 0; i < 30; i++ {
		in.History.PersistentHotspots = append(in.History.PersistentHotspots, Hotspot{
			Function: strings.Repeat("example.com/app.Hot", 100), Profile: "cpu", CurrentShare: 0.2,
		})
	}
	data, err := fitAIInput(in)
	if err != nil {
		t.Fatalf("fitAIInput: %v", err)
	}
	if len(data) > maxAIInputSizeBytes {
		t.Fatalf("fitted size = %d, budget %d", len(data), maxAIInputSizeBytes)
	}
	if len(in.ProfileChanges["cpu"].TopDecreases) > 5 {
		t.Fatalf("decreases were not trimmed: %d", len(in.ProfileChanges["cpu"].TopDecreases))
	}
	var decoded AIInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal fitted input: %v", err)
	}
}
