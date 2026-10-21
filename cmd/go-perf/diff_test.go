package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
)

func TestParseValue(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		// Time units
		{"53.70s", 53700000000, false},
		{"-12.41s", -12410000000, false},
		{"+25.5s", 25500000000, false},
		{"1500ms", 1500000000, false},
		{"-500ms", -500000000, false},
		{"100us", 100000, false},
		{"100µs", 100000, false},
		{"5ns", 5, false},
		{"2min", 120000000000, false},
		{"1.5hrs", 5400000000000, false},

		// Byte units
		{"1.20GB", 1288490188, false}, // 1.20 * 1024^3
		{"-512kB", -524288, false},    // -512 * 1024
		{"100MB", 104857600, false},   // 100 * 1024^2
		{"1024B", 1024, false},
		{"+2TB", 2199023255552, false}, // 2 * 1024^4

		// Bare numbers
		{"100", 100, false},
		{"-50", -50, false},
		{"+75", 75, false},
		{"1234567", 1234567, false},

		// Decimal bare numbers
		{"100.5", 100, false},
		{"-50.75", -50, false},

		// Invalid inputs
		{"", 0, true},
		{"invalid", 0, true},
		{"s", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseValue(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseValue(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != tt.expected {
				t.Errorf("parseValue(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseDiffOutput_CPU(t *testing.T) {
	// Real pprof output for CPU profile with time units
	output := `Main binary filename not available.
Type: cpu
Duration: 10s, Total samples = 150s (1500%)
Showing nodes accounting for 45s, 30.00% of 150s total
Dropped 5 nodes (cum <= 0.75s)
      flat  flat%   sum%        cum   cum%
    +5.50s  +3.67%  3.67%    +8.25s  +5.50%  runtime.mallocgc
    -2.30s  -1.53%  2.14%    -4.10s  -2.73%  runtime.scanobject
    +1.75s  +1.17%  3.31%    +1.75s  +1.17%  runtime.memmove (inline)
`

	tmpDir := t.TempDir()
	diffPath := filepath.Join(tmpDir, "cpu-diff.txt")
	if err := os.WriteFile(diffPath, []byte(output), 0644); err != nil {
		t.Fatal(err)
	}

	diff := &ProfileDiff{
		ProfileType:    "cpu",
		DiffOutputPath: diffPath,
	}

	if err := parseDiffOutput(diff); err != nil {
		t.Fatalf("parseDiffOutput failed: %v", err)
	}

	if len(diff.TopIncrease) != 2 {
		t.Errorf("Expected 2 increases, got %d", len(diff.TopIncrease))
	}

	if len(diff.TopDecrease) != 1 {
		t.Errorf("Expected 1 decrease, got %d", len(diff.TopDecrease))
	}

	// Check first increase
	if len(diff.TopIncrease) > 0 {
		inc := diff.TopIncrease[0]
		if inc.Function != "runtime.mallocgc" {
			t.Errorf("Expected function 'runtime.mallocgc', got '%s'", inc.Function)
		}
		if inc.FlatDelta != 5500000000 { // 5.50s in nanoseconds
			t.Errorf("Expected flatDelta 5500000000, got %d", inc.FlatDelta)
		}
		if inc.CumDelta != 8250000000 { // 8.25s in nanoseconds
			t.Errorf("Expected cumDelta 8250000000, got %d", inc.CumDelta)
		}
	}

	// Check decrease
	if len(diff.TopDecrease) > 0 {
		dec := diff.TopDecrease[0]
		if dec.Function != "runtime.scanobject" {
			t.Errorf("Expected function 'runtime.scanobject', got '%s'", dec.Function)
		}
		if dec.FlatDelta != -2300000000 { // -2.30s in nanoseconds
			t.Errorf("Expected flatDelta -2300000000, got %d", dec.FlatDelta)
		}
	}

	// Check inline suffix was stripped
	if len(diff.TopIncrease) > 1 {
		inc := diff.TopIncrease[1]
		if inc.Function != "runtime.memmove" {
			t.Errorf("Expected function 'runtime.memmove' (inline stripped), got '%s'", inc.Function)
		}
	}
}

func TestParseDiffOutput_Heap(t *testing.T) {
	// Real pprof output for heap profile with byte units
	output := `Main binary filename not available.
Type: inuse_space
Showing nodes accounting for 1.50GB, 30.00% of 5GB total
      flat  flat%   sum%        cum   cum%
  +512MB +10.24% 10.24%   +512MB +10.24%  main.allocateLargeBuffer
  -256kB  -0.01%  10.23%   -256kB  -0.01%  runtime.allocm
   +10MB  +0.20% 10.43%    +10MB  +0.20%  encoding/json.Marshal
`

	tmpDir := t.TempDir()
	diffPath := filepath.Join(tmpDir, "heap-diff.txt")
	if err := os.WriteFile(diffPath, []byte(output), 0644); err != nil {
		t.Fatal(err)
	}

	diff := &ProfileDiff{
		ProfileType:    "heap",
		DiffOutputPath: diffPath,
	}

	if err := parseDiffOutput(diff); err != nil {
		t.Fatalf("parseDiffOutput failed: %v", err)
	}

	if len(diff.TopIncrease) != 2 {
		t.Errorf("Expected 2 increases, got %d", len(diff.TopIncrease))
	}

	if len(diff.TopDecrease) != 1 {
		t.Errorf("Expected 1 decrease, got %d", len(diff.TopDecrease))
	}

	// Check first increase (512MB)
	if len(diff.TopIncrease) > 0 {
		inc := diff.TopIncrease[0]
		if inc.Function != "main.allocateLargeBuffer" {
			t.Errorf("Expected function 'main.allocateLargeBuffer', got '%s'", inc.Function)
		}
		expectedBytes := int64(512 * 1024 * 1024)
		if inc.FlatDelta != expectedBytes {
			t.Errorf("Expected flatDelta %d (512MB), got %d", expectedBytes, inc.FlatDelta)
		}
	}

	// Check decrease (-256kB)
	if len(diff.TopDecrease) > 0 {
		dec := diff.TopDecrease[0]
		if dec.Function != "runtime.allocm" {
			t.Errorf("Expected function 'runtime.allocm', got '%s'", dec.Function)
		}
		expectedBytes := int64(-256 * 1024)
		if dec.FlatDelta != expectedBytes {
			t.Errorf("Expected flatDelta %d (-256kB), got %d", expectedBytes, dec.FlatDelta)
		}
	}
}

func TestParseDiffOutput_Goroutine(t *testing.T) {
	// Real pprof output for goroutine profile with bare counts
	output := `Main binary filename not available.
Type: goroutine
Showing nodes accounting for 30, 30.00% of 100 total
      flat  flat%   sum%        cum   cum%
       +15 +15.00% 15.00%        +15 +15.00%  runtime.gopark
       -10 -10.00%  5.00%        -10 -10.00%  runtime.chansend
        +5  +5.00% 10.00%         +5  +5.00%  net/http.(*conn).serve
`

	tmpDir := t.TempDir()
	diffPath := filepath.Join(tmpDir, "goroutine-diff.txt")
	if err := os.WriteFile(diffPath, []byte(output), 0644); err != nil {
		t.Fatal(err)
	}

	diff := &ProfileDiff{
		ProfileType:    "goroutine",
		DiffOutputPath: diffPath,
	}

	if err := parseDiffOutput(diff); err != nil {
		t.Fatalf("parseDiffOutput failed: %v", err)
	}

	if len(diff.TopIncrease) != 2 {
		t.Errorf("Expected 2 increases, got %d", len(diff.TopIncrease))
	}

	if len(diff.TopDecrease) != 1 {
		t.Errorf("Expected 1 decrease, got %d", len(diff.TopDecrease))
	}

	// Check bare count values
	if len(diff.TopIncrease) > 0 {
		inc := diff.TopIncrease[0]
		if inc.FlatDelta != 15 {
			t.Errorf("Expected flatDelta 15, got %d", inc.FlatDelta)
		}
	}

	if len(diff.TopDecrease) > 0 {
		dec := diff.TopDecrease[0]
		if dec.FlatDelta != -10 {
			t.Errorf("Expected flatDelta -10, got %d", dec.FlatDelta)
		}
	}
}

func TestParseDiffOutput_EmptyOutput(t *testing.T) {
	output := `Type: cpu
      flat  flat%   sum%        cum   cum%
`

	tmpDir := t.TempDir()
	diffPath := filepath.Join(tmpDir, "empty-diff.txt")
	if err := os.WriteFile(diffPath, []byte(output), 0644); err != nil {
		t.Fatal(err)
	}

	diff := &ProfileDiff{
		ProfileType:    "cpu",
		DiffOutputPath: diffPath,
	}

	if err := parseDiffOutput(diff); err != nil {
		t.Fatalf("parseDiffOutput failed: %v", err)
	}

	if len(diff.TopIncrease) != 0 {
		t.Errorf("Expected 0 increases, got %d", len(diff.TopIncrease))
	}

	if len(diff.TopDecrease) != 0 {
		t.Errorf("Expected 0 decreases, got %d", len(diff.TopDecrease))
	}
}

func TestGetTotalSamples_CPUProfile(t *testing.T) {
	// Create a synthetic CPU profile with known total
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-cpu.pprof")

	// Create a minimal valid pprof profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: "cpu", Unit: "nanoseconds"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{1, 5000000000}},  // 5 seconds
			{Value: []int64{1, 10000000000}}, // 10 seconds
			{Value: []int64{1, 3500000000}},  // 3.5 seconds
		},
		TimeNanos:     1234567890000000000,
		DurationNanos: 10000000000,
	}

	f, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Test getTotalSamples
	total, unit, err := getTotalSamples(profilePath, "cpu")
	if err != nil {
		t.Fatalf("getTotalSamples failed: %v", err)
	}

	expectedTotal := int64(5000000000 + 10000000000 + 3500000000) // 18.5 seconds = 18,500,000,000 ns
	if total != expectedTotal {
		t.Errorf("Expected total %d nanoseconds (%.2fs), got %d nanoseconds (%.2fs)",
			expectedTotal, float64(expectedTotal)/1e9, total, float64(total)/1e9)
	}

	if unit != "nanoseconds" {
		t.Errorf("Expected unit 'nanoseconds', got '%s'", unit)
	}
}

func TestGetTotalSamples_HeapProfile(t *testing.T) {
	// Create a synthetic heap profile with known total
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-heap.pprof")

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
			{Type: "inuse_objects", Unit: "count"},
			{Type: "inuse_space", Unit: "bytes"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{10, 1024 * 1024, 5, 512 * 1024}},   // 512 KB in-use
			{Value: []int64{20, 2048 * 1024, 10, 1024 * 1024}}, // 1 MB in-use
			{Value: []int64{5, 512 * 1024, 2, 256 * 1024}},     // 256 KB in-use
		},
		TimeNanos:     1234567890000000000,
		DurationNanos: 10000000000,
	}

	f, err := os.Create(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Test getTotalSamples for heap (inuse_space)
	total, unit, err := getTotalSamples(profilePath, "heap")
	if err != nil {
		t.Fatalf("getTotalSamples failed: %v", err)
	}

	expectedTotal := int64(512*1024 + 1024*1024 + 256*1024) // 1.75 MB
	if total != expectedTotal {
		t.Errorf("Expected total %d bytes (%.2f MB), got %d bytes (%.2f MB)",
			expectedTotal, float64(expectedTotal)/(1024*1024), total, float64(total)/(1024*1024))
	}

	if unit != "bytes" {
		t.Errorf("Expected unit 'bytes', got '%s'", unit)
	}
}
