package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

func TestParseCPUProfile(t *testing.T) {
	// Create synthetic CPU profile
	fn1 := &profile.Function{ID: 1, Name: "example.com/app/pkg/evidence.Collect"}
	fn2 := &profile.Function{ID: 2, Name: "example.com/app/pkg/storage.Store"}
	fn3 := &profile.Function{ID: 3, Name: "runtime.mallocgc"}
	fn4 := &profile.Function{ID: 4, Name: "example.com/app/pkg/evidence.(*Collector).Process"}

	loc1 := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fn2}}}
	loc3 := &profile.Location{ID: 3, Address: 0x3000, Line: []profile.Line{{Function: fn3}}}
	loc4 := &profile.Location{ID: 4, Address: 0x4000, Line: []profile.Line{{Function: fn4}}}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{
				Location: []*profile.Location{loc1, loc2},
				Value:    []int64{100, 100000000}, // 100 samples
			},
			{
				Location: []*profile.Location{loc3},
				Value:    []int64{50, 50000000}, // 50 samples
			},
			{
				Location: []*profile.Location{loc4, loc1},
				Value:    []int64{75, 75000000}, // 75 samples
			},
		},
		Location:      []*profile.Location{loc1, loc2, loc3, loc4},
		Function:      []*profile.Function{fn1, fn2, fn3, fn4},
		Period:        10000000,   // 10ms
		DurationNanos: 5000000000, // 5 seconds
	}

	// Write to temp file
	tmpDir := t.TempDir()
	cpuPath := filepath.Join(tmpDir, "cpu.pprof")
	f, err := os.Create(cpuPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Parse it
	insights, err := parseCPUProfile(cpuPath, 5000) // 5 second wall time
	if err != nil {
		t.Fatalf("parseCPUProfile failed: %v", err)
	}

	// Check totals
	if insights.TotalSamples != 225 {
		t.Errorf("TotalSamples: got %d, want 225", insights.TotalSamples)
	}

	if insights.CPUTimeNanos != 2250000000 {
		t.Errorf("CPUTimeNanos: got %d, want 2250000000", insights.CPUTimeNanos)
	}

	// Check utilization (2.25s CPU / 5s wall = 0.45 cores)
	expectedUtil := 0.45
	if insights.Utilization < expectedUtil-0.01 || insights.Utilization > expectedUtil+0.01 {
		t.Errorf("Utilization: got %.2f, want %.2f", insights.Utilization, expectedUtil)
	}

	// Check top flat
	if len(insights.TopFlat) == 0 {
		t.Fatal("TopFlat is empty")
	}

	// evidence.Collect should be #1 (100 flat + 75 from child = 175 total, but flat is 100)
	// Values are now converted to nanoseconds (sample_count * period)
	// period is 10000000ns, so 100 samples = 1000000000ns
	if insights.TopFlat[0].Function != "example.com/app/pkg/evidence.Collect" {
		t.Errorf("TopFlat[0]: got %s, want evidence.Collect", insights.TopFlat[0].Function)
	}
	expectedFlat := int64(100 * 10000000) // 100 samples * 10ms period = 1000000000ns
	if insights.TopFlat[0].Flat != expectedFlat {
		t.Errorf("TopFlat[0].Flat: got %d, want %d", insights.TopFlat[0].Flat, expectedFlat)
	}

	// Check cumulative (Collect should have 175 = 100 direct + 75 from Process call)
	expectedCum := int64(175 * 10000000)
	if insights.TopFlat[0].Cum != expectedCum {
		t.Errorf("TopFlat[0].Cum: got %d, want %d", insights.TopFlat[0].Cum, expectedCum)
	}

	// Check packages
	if len(insights.TopPackages) == 0 {
		t.Fatal("TopPackages is empty")
	}

	// Should aggregate example.com/app/pkg/evidence
	found := false
	for _, pkg := range insights.TopPackages {
		if pkg.Package == "example.com/app/pkg/evidence" {
			found = true
			// 100 (Collect) + 75 (Collector.Process) = 175 samples * 10ms = 1750000000ns
			expectedPkgFlat := int64(175 * 10000000)
			if pkg.Flat != expectedPkgFlat {
				t.Errorf("evidence package flat: got %d, want %d", pkg.Flat, expectedPkgFlat)
			}
		}
	}
	if !found {
		t.Error("evidence package not found in TopPackages")
	}

	// Verify all percentages are valid (<= 100%)
	for i, fn := range insights.TopFlat {
		if fn.FlatPercent > 100.0 {
			t.Errorf("TopFlat[%d] (%s) FlatPercent is %.2f%%, should be <= 100%%", i, fn.Function, fn.FlatPercent)
		}
		if fn.CumPercent > 100.0 {
			t.Errorf("TopFlat[%d] (%s) CumPercent is %.2f%%, should be <= 100%%", i, fn.Function, fn.CumPercent)
		}
	}

	for i, fn := range insights.TopCum {
		if fn.FlatPercent > 100.0 {
			t.Errorf("TopCum[%d] (%s) FlatPercent is %.2f%%, should be <= 100%%", i, fn.Function, fn.FlatPercent)
		}
		if fn.CumPercent > 100.0 {
			t.Errorf("TopCum[%d] (%s) CumPercent is %.2f%%, should be <= 100%%", i, fn.Function, fn.CumPercent)
		}
	}

	for i, pkg := range insights.TopPackages {
		if pkg.FlatPercent > 100.0 {
			t.Errorf("TopPackages[%d] (%s) FlatPercent is %.2f%%, should be <= 100%%", i, pkg.Package, pkg.FlatPercent)
		}
	}

	// Verify percentages sum to <= 100% (they should, since they're relative to total)
	var totalFlatPercent float64
	for _, fn := range insights.TopFlat {
		totalFlatPercent += fn.FlatPercent
	}
	if totalFlatPercent > 100.0 {
		t.Errorf("Sum of TopFlat percentages is %.2f%%, should be <= 100%%", totalFlatPercent)
	}
}

func TestParseHeapProfile(t *testing.T) {
	fn1 := &profile.Function{ID: 1, Name: "example.com/app/pkg/storage.Allocate"}
	fn2 := &profile.Function{ID: 2, Name: "example.com/app/pkg/buffer.New"}

	loc1 := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fn2}}}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
			{Type: "inuse_objects", Unit: "count"},
			{Type: "inuse_space", Unit: "bytes"},
		},
		Sample: []*profile.Sample{
			{
				Location: []*profile.Location{loc1},
				Value:    []int64{1000, 10485760, 500, 5242880}, // 10MB alloc, 5MB inuse
			},
			{
				Location: []*profile.Location{loc2},
				Value:    []int64{500, 5242880, 250, 2621440}, // 5MB alloc, 2.5MB inuse
			},
		},
		Location: []*profile.Location{loc1, loc2},
		Function: []*profile.Function{fn1, fn2},
	}

	tmpDir := t.TempDir()
	heapPath := filepath.Join(tmpDir, "heap.pprof")
	f, err := os.Create(heapPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	insights, err := parseHeapProfile(heapPath)
	if err != nil {
		t.Fatalf("parseHeapProfile failed: %v", err)
	}

	// Check inuse totals
	if insights.InUseBytes != 7864320 { // 5MB + 2.5MB
		t.Errorf("InUseBytes: got %d, want 7864320", insights.InUseBytes)
	}

	if insights.InUseObjects != 750 {
		t.Errorf("InUseObjects: got %d, want 750", insights.InUseObjects)
	}

	// Check top list
	if len(insights.TopInUseSpace) == 0 {
		t.Fatal("TopInUseSpace is empty")
	}

	if insights.TopInUseSpace[0].Function != "example.com/app/pkg/storage.Allocate" {
		t.Errorf("TopInUseSpace[0]: got %s, want storage.Allocate", insights.TopInUseSpace[0].Function)
	}

	if insights.TopInUseSpace[0].Flat != 5242880 {
		t.Errorf("TopInUseSpace[0].Flat: got %d, want 5242880", insights.TopInUseSpace[0].Flat)
	}
}

func TestParseAllocsProfile(t *testing.T) {
	fn1 := &profile.Function{ID: 1, Name: "example.com/app/pkg/network.Send"}
	fn2 := &profile.Function{ID: 2, Name: "encoding/json.Marshal"}

	loc1 := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fn2}}}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
		Sample: []*profile.Sample{
			{
				Location: []*profile.Location{loc1},
				Value:    []int64{10000, 104857600}, // 100MB, 10k objects
			},
			{
				Location: []*profile.Location{loc2},
				Value:    []int64{5000, 52428800}, // 50MB, 5k objects
			},
		},
		Location: []*profile.Location{loc1, loc2},
		Function: []*profile.Function{fn1, fn2},
	}

	tmpDir := t.TempDir()
	allocsPath := filepath.Join(tmpDir, "allocs.pprof")
	f, err := os.Create(allocsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	insights, err := parseAllocsProfile(allocsPath)
	if err != nil {
		t.Fatalf("parseAllocsProfile failed: %v", err)
	}

	if insights.AllocBytes != 157286400 { // 100MB + 50MB
		t.Errorf("AllocBytes: got %d, want 157286400", insights.AllocBytes)
	}

	if insights.AllocObjects != 15000 {
		t.Errorf("AllocObjects: got %d, want 15000", insights.AllocObjects)
	}

	if len(insights.TopAllocSpace) == 0 {
		t.Fatal("TopAllocSpace is empty")
	}

	if insights.TopAllocSpace[0].Function != "example.com/app/pkg/network.Send" {
		t.Errorf("TopAllocSpace[0]: got %s", insights.TopAllocSpace[0].Function)
	}
}

func TestParseGoroutineProfile(t *testing.T) {
	fn1 := &profile.Function{ID: 1, Name: "example.com/app/pkg/worker.(*Pool).Run"}
	fn2 := &profile.Function{ID: 2, Name: "runtime.gopark"}
	fn3 := &profile.Function{ID: 3, Name: "net/http.(*conn).serve"}

	loc1 := &profile.Location{ID: 1, Address: 0x1000, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Address: 0x2000, Line: []profile.Line{{Function: fn2}}}
	loc3 := &profile.Location{ID: 3, Address: 0x3000, Line: []profile.Line{{Function: fn3}}}

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "goroutine", Unit: "count"},
		},
		Sample: []*profile.Sample{
			{
				Location: []*profile.Location{loc1},
				Value:    []int64{100},
			},
			{
				Location: []*profile.Location{loc2},
				Value:    []int64{50},
			},
			{
				Location: []*profile.Location{loc3},
				Value:    []int64{25},
			},
		},
		Location: []*profile.Location{loc1, loc2, loc3},
		Function: []*profile.Function{fn1, fn2, fn3},
	}

	tmpDir := t.TempDir()
	goroutinePath := filepath.Join(tmpDir, "goroutine.pprof")
	f, err := os.Create(goroutinePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	insights, err := parseGoroutineProfile(goroutinePath)
	if err != nil {
		t.Fatalf("parseGoroutineProfile failed: %v", err)
	}

	if insights.TotalCount != 175 {
		t.Errorf("TotalCount: got %d, want 175", insights.TotalCount)
	}

	if len(insights.TopFunctions) == 0 {
		t.Fatal("TopFunctions is empty")
	}

	if insights.TopFunctions[0].Function != "example.com/app/pkg/worker.(*Pool).Run" {
		t.Errorf("TopFunctions[0]: got %s", insights.TopFunctions[0].Function)
	}

	if insights.TopFunctions[0].Count != 100 {
		t.Errorf("TopFunctions[0].Count: got %d, want 100", insights.TopFunctions[0].Count)
	}
}

func TestGoroutineLeakDetection(t *testing.T) {
	baseline := &GoroutineInsights{
		TotalCount: 100,
	}

	current := &GoroutineInsights{
		TotalCount: 250, // 150% increase
	}

	mergeGoroutineBaseline(current, baseline)

	if !current.LikelyLeak {
		t.Error("Should detect leak: 150% increase from 100 to 250")
	}

	if current.LeakDescription == "" {
		t.Error("LeakDescription should be set")
	}
}

func TestExtractPackage(t *testing.T) {
	tests := []struct {
		function string
		want     string
	}{
		{
			"example.com/app/pkg/evidence.Collect",
			"example.com/app/pkg/evidence",
		},
		{
			"example.com/app/pkg/storage.(*Writer).Flush",
			"example.com/app/pkg/storage",
		},
		{
			"github.com/aws/aws-sdk-go-v2/service/s3.(*Client).PutObject",
			"github.com/aws/aws-sdk-go-v2/service/s3",
		},
		{
			"runtime.mallocgc",
			"runtime",
		},
		{
			"main.main",
			"main",
		},
	}

	for _, tt := range tests {
		got := extractPackage(tt.function)
		if got != tt.want {
			t.Errorf("extractPackage(%q) = %q, want %q", tt.function, got, tt.want)
		}
	}
}

func TestTrimFunctionName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"example.com/app/pkg/evidence.Collect",
			".../pkg/evidence.Collect",
		},
		{
			"example.com/app/pkg/storage.(*Writer).Flush",
			".../pkg/storage.(*Writer).Flush",
		},
		{
			"runtime.mallocgc",
			"runtime.mallocgc",
		},
		{
			"short/path.Func",
			"short/path.Func",
		},
	}

	for _, tt := range tests {
		got := TrimFunctionName(tt.input)
		if got != tt.want {
			t.Errorf("TrimFunctionName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBaselineMerge(t *testing.T) {
	baseline := &CPUInsights{
		TopFlat: []FunctionSample{
			{Function: "func1", Flat: 100, Cum: 150},
			{Function: "func2", Flat: 50, Cum: 75},
		},
		TopPackages: []PackageSample{
			{Package: "pkg1", Flat: 200},
		},
	}

	current := &CPUInsights{
		TopFlat: []FunctionSample{
			{Function: "func1", Flat: 120, Cum: 180},
			{Function: "func2", Flat: 40, Cum: 60},
		},
		TopPackages: []PackageSample{
			{Package: "pkg1", Flat: 250},
		},
	}

	mergeCPUBaseline(current, baseline)

	// Check func1 deltas
	if !current.TopFlat[0].HasBaseline {
		t.Error("func1 should have baseline")
	}
	if current.TopFlat[0].FlatDelta != 20 {
		t.Errorf("func1 FlatDelta: got %d, want 20", current.TopFlat[0].FlatDelta)
	}
	if current.TopFlat[0].CumDelta != 30 {
		t.Errorf("func1 CumDelta: got %d, want 30", current.TopFlat[0].CumDelta)
	}

	// Check func2 deltas (negative)
	if current.TopFlat[1].FlatDelta != -10 {
		t.Errorf("func2 FlatDelta: got %d, want -10", current.TopFlat[1].FlatDelta)
	}

	// Check package deltas
	if !current.TopPackages[0].HasBaseline {
		t.Error("pkg1 should have baseline")
	}
	if current.TopPackages[0].FlatDelta != 50 {
		t.Errorf("pkg1 FlatDelta: got %d, want 50", current.TopPackages[0].FlatDelta)
	}
}
