package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

func TestSummaryRoundTripAndProfileFallback(t *testing.T) {
	runDir := t.TempDir()
	meta := &RunMetadata{
		RunID: "run-1", Timestamp: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		DurationMS: 30000, AgentName: "agent-a", CPUModel: "test-cpu", CPUCores: 4,
		ProductCommitID: "abc123", Success: true, ProfilesCollected: true,
	}
	if err := saveMetadata(runDir, meta); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := writeCPUProfileZip(t, filepath.Join(runDir, "profiles.zip")); err != nil {
		t.Fatalf("write profiles: %v", err)
	}

	summary, err := writeRunSummary(runDir, meta)
	if err != nil {
		t.Fatalf("writeRunSummary: %v", err)
	}
	if summary.AvgCores < 0.4 || summary.AvgCores > 0.6 {
		t.Fatalf("avg cores = %v, want about 0.5", summary.AvgCores)
	}
	cpu, ok := summary.Profiles["cpu"]
	if !ok || cpu.Total != 15_000_000_000 || len(cpu.Top) == 0 {
		t.Fatalf("cpu summary = %+v, want 15s total and a top function", cpu)
	}

	os.Remove(filepath.Join(runDir, "profiles.zip"))
	loaded, err := loadRunSummary(runDir)
	if err != nil {
		t.Fatalf("loadRunSummary: %v", err)
	}
	if loaded.CommitID != "abc123" || loaded.Profiles["cpu"].Total != cpu.Total {
		t.Fatalf("loaded summary = %+v, want the written summary", loaded)
	}
}

func TestLoadSummaryFallsBackToProfiles(t *testing.T) {
	runDir := t.TempDir()
	meta := &RunMetadata{RunID: "run-2", Timestamp: time.Now().UTC(), DurationMS: 20000, Success: true}
	if err := saveMetadata(runDir, meta); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := writeCPUProfileZip(t, filepath.Join(runDir, "profiles.zip")); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	summary, err := loadRunSummary(runDir)
	if err != nil {
		t.Fatalf("loadRunSummary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, summaryFile)); err != nil {
		t.Fatalf("summary.json was not written on fallback: %v", err)
	}
	if summary.Profiles["cpu"].Total == 0 {
		t.Fatal("fallback summary has no CPU total")
	}
}

func writeCPUProfileZip(t *testing.T, zipPath string) error {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: "example.com/app.Work"}
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Sample: []*profile.Sample{{
			Value:    []int64{15_000_000_000},
			Location: []*profile.Location{{ID: 1, Line: []profile.Line{{Function: fn, Line: 10}}}},
		}},
		DurationNanos: 30_000_000_000,
		Function:      []*profile.Function{fn},
		Location:      []*profile.Location{{ID: 1, Line: []profile.Line{{Function: fn, Line: 10}}}},
	}
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	w, err := zw.Create("cpu.pprof")
	if err != nil {
		return err
	}
	if err := prof.Write(w); err != nil {
		return err
	}
	return zw.Close()
}
