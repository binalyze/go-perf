package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// ProfileInsights contains extracted insights from profiling data
type ProfileInsights struct {
	CPU        CPUInsights
	Memory     MemoryInsights
	Allocs     AllocsInsights
	Goroutines GoroutineInsights
	Headlines  HeadlineNumbers
}

type HeadlineNumbers struct {
	CPUTime         int64   // nanoseconds
	CPUCores        float64 // average CPU cores utilized
	HeapInUseBytes  int64   // heap in-use at snapshot time (not a peak)
	TotalAllocBytes int64
	AllocObjects    int64
	GoroutineCount  int64

	// Deltas vs baseline (if available)
	CPUTimeDelta         int64
	CPUCoresDelta        float64
	HeapInUseBytesDelta  int64
	TotalAllocBytesDelta int64
	AllocObjectsDelta    int64
	GoroutineCountDelta  int64

	HasBaseline bool
}

type CPUInsights struct {
	TotalSamples   int64
	SampleDuration int64 // nanoseconds
	CPUTimeNanos   int64
	Utilization    float64 // cores
	TopFlat        []FunctionSample
	TopCum         []FunctionSample
	TopPackages    []PackageSample
}

type MemoryInsights struct {
	InUseBytes    int64
	InUseObjects  int64
	TopInUseSpace []FunctionSample
}

type AllocsInsights struct {
	AllocBytes    int64
	AllocObjects  int64
	TopAllocSpace []FunctionSample
	TopAllocCount []FunctionSample
}

type GoroutineInsights struct {
	TotalCount      int64
	TopFunctions    []GoroutineSample
	LikelyLeak      bool
	LeakDescription string
}

type FunctionSample struct {
	Function    string
	Flat        int64
	Cum         int64
	FlatPercent float64
	CumPercent  float64

	// Baseline comparison
	BaselineFlat int64
	BaselineCum  int64
	FlatDelta    int64
	CumDelta     int64
	HasBaseline  bool
}

type PackageSample struct {
	Package     string
	Flat        int64
	FlatPercent float64

	BaselineFlat int64
	FlatDelta    int64
	HasBaseline  bool
}

type GoroutineSample struct {
	Function string
	Count    int64
	Percent  float64

	BaselineCount int64
	Delta         int64
	HasBaseline   bool
}

// ExtractProfileInsights analyzes profiles from profiles.zip
func ExtractProfileInsights(runDir string, wallDurationMS int64, baselineInsights *ProfileInsights) (*ProfileInsights, error) {
	// Get profile directory (handles both profiles.zip and loose *.pprof files)
	profileDir, needsCleanup, err := getProfileDir(runDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile directory: %w", err)
	}
	if needsCleanup {
		defer os.RemoveAll(profileDir)
	}

	insights := &ProfileInsights{}

	// Parse CPU profile
	cpuPath := filepath.Join(profileDir, "cpu.pprof")
	if fileExists(cpuPath) {
		cpuInsights, err := parseCPUProfile(cpuPath, wallDurationMS)
		if err == nil {
			insights.CPU = cpuInsights
			if baselineInsights != nil {
				mergeCPUBaseline(&insights.CPU, &baselineInsights.CPU)
			}
		}
	}

	// Parse heap profile
	heapPath := filepath.Join(profileDir, "heap.pprof")
	if fileExists(heapPath) {
		memInsights, err := parseHeapProfile(heapPath)
		if err == nil {
			insights.Memory = memInsights
			if baselineInsights != nil {
				mergeMemoryBaseline(&insights.Memory, &baselineInsights.Memory)
			}
		}
	}

	// Parse allocs profile
	allocsPath := filepath.Join(profileDir, "allocs.pprof")
	if fileExists(allocsPath) {
		allocsInsights, err := parseAllocsProfile(allocsPath)
		if err == nil {
			insights.Allocs = allocsInsights
			if baselineInsights != nil {
				mergeAllocsBaseline(&insights.Allocs, &baselineInsights.Allocs)
			}
		}
	}

	// Parse goroutine profile
	goroutinePath := filepath.Join(profileDir, "goroutine.pprof")
	if fileExists(goroutinePath) {
		goroutineInsights, err := parseGoroutineProfile(goroutinePath)
		if err == nil {
			insights.Goroutines = goroutineInsights
			if baselineInsights != nil {
				mergeGoroutineBaseline(&insights.Goroutines, &baselineInsights.Goroutines)
			}
		}
	}

	// Build headline numbers
	insights.Headlines = buildHeadlineNumbers(insights, baselineInsights)

	return insights, nil
}

func parseCPUProfile(path string, wallDurationMS int64) (CPUInsights, error) {
	f, err := os.Open(path)
	if err != nil {
		return CPUInsights{}, err
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return CPUInsights{}, err
	}

	insights := CPUInsights{
		SampleDuration: prof.DurationNanos,
	}

	// Find sample type indices
	sampleTypeIdx := -1
	for i, st := range prof.SampleType {
		if st.Type == "samples" || st.Type == "cpu" {
			sampleTypeIdx = i
			break
		}
	}

	if sampleTypeIdx == -1 && len(prof.SampleType) > 0 {
		sampleTypeIdx = 0
	}

	// Build function flat and cumulative values
	flatMap := make(map[string]int64)
	cumMap := make(map[string]int64)

	for _, sample := range prof.Sample {
		if sampleTypeIdx >= len(sample.Value) {
			continue
		}
		value := sample.Value[sampleTypeIdx]
		insights.TotalSamples += value

		// Flat: only the leaf
		if len(sample.Location) > 0 {
			loc := sample.Location[0]
			if len(loc.Line) > 0 {
				fn := loc.Line[0].Function
				if fn != nil {
					flatMap[fn.Name] += value
				}
			}
		}

		// Cumulative: all functions in stack
		for _, loc := range sample.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					cumMap[line.Function.Name] += value
				}
			}
		}
	}

	// Calculate CPU time (samples * sample period)
	if prof.Period > 0 {
		insights.CPUTimeNanos = insights.TotalSamples * prof.Period
	}

	// Calculate utilization
	if wallDurationMS > 0 {
		wallNanos := wallDurationMS * 1000000
		insights.Utilization = float64(insights.CPUTimeNanos) / float64(wallNanos)
	}

	// Convert sample counts to nanoseconds for CPU profile
	if prof.Period > 0 {
		convertedFlatMap := make(map[string]int64)
		convertedCumMap := make(map[string]int64)
		for fn, val := range flatMap {
			convertedFlatMap[fn] = val * prof.Period
		}
		for fn, val := range cumMap {
			convertedCumMap[fn] = val * prof.Period
		}
		// Build top lists with converted values (pass total in nanoseconds for correct percentages)
		totalNanos := insights.TotalSamples * prof.Period
		insights.TopFlat = buildTopFunctions(convertedFlatMap, convertedCumMap, totalNanos, 10)
		insights.TopCum = buildTopFunctionsByCum(convertedFlatMap, convertedCumMap, totalNanos, 10)
		insights.TopPackages = buildTopPackages(convertedFlatMap, totalNanos, 10)
	} else {
		// Fallback if period not available
		insights.TopFlat = buildTopFunctions(flatMap, cumMap, insights.TotalSamples, 10)
		insights.TopCum = buildTopFunctionsByCum(flatMap, cumMap, insights.TotalSamples, 10)
		insights.TopPackages = buildTopPackages(flatMap, insights.TotalSamples, 10)
	}

	return insights, nil
}

func parseHeapProfile(path string) (MemoryInsights, error) {
	f, err := os.Open(path)
	if err != nil {
		return MemoryInsights{}, err
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return MemoryInsights{}, err
	}

	insights := MemoryInsights{}

	// Find inuse_space and inuse_objects indices
	inuseSpaceIdx := -1
	inuseObjectsIdx := -1

	for i, st := range prof.SampleType {
		if st.Type == "inuse_space" {
			inuseSpaceIdx = i
		}
		if st.Type == "inuse_objects" {
			inuseObjectsIdx = i
		}
	}

	if inuseSpaceIdx == -1 {
		return insights, fmt.Errorf("inuse_space not found in heap profile")
	}

	// Build function maps
	flatMap := make(map[string]int64)
	cumMap := make(map[string]int64)

	for _, sample := range prof.Sample {
		if inuseSpaceIdx >= len(sample.Value) {
			continue
		}
		value := sample.Value[inuseSpaceIdx]
		insights.InUseBytes += value

		// Flat
		if len(sample.Location) > 0 {
			loc := sample.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				flatMap[loc.Line[0].Function.Name] += value
			}
		}

		// Cum
		for _, loc := range sample.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					cumMap[line.Function.Name] += value
				}
			}
		}
	}

	// Count objects
	if inuseObjectsIdx >= 0 {
		for _, sample := range prof.Sample {
			if inuseObjectsIdx < len(sample.Value) {
				insights.InUseObjects += sample.Value[inuseObjectsIdx]
			}
		}
	}

	insights.TopInUseSpace = buildTopFunctions(flatMap, cumMap, insights.InUseBytes, 10)

	return insights, nil
}

func parseAllocsProfile(path string) (AllocsInsights, error) {
	f, err := os.Open(path)
	if err != nil {
		return AllocsInsights{}, err
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return AllocsInsights{}, err
	}

	insights := AllocsInsights{}

	// Find alloc_space and alloc_objects indices
	allocSpaceIdx := -1
	allocObjectsIdx := -1

	for i, st := range prof.SampleType {
		if st.Type == "alloc_space" {
			allocSpaceIdx = i
		}
		if st.Type == "alloc_objects" {
			allocObjectsIdx = i
		}
	}

	if allocSpaceIdx == -1 {
		return insights, fmt.Errorf("alloc_space not found")
	}

	// Build maps for space
	spaceFlatMap := make(map[string]int64)
	spaceCumMap := make(map[string]int64)

	for _, sample := range prof.Sample {
		if allocSpaceIdx >= len(sample.Value) {
			continue
		}
		value := sample.Value[allocSpaceIdx]
		insights.AllocBytes += value

		if len(sample.Location) > 0 {
			loc := sample.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				spaceFlatMap[loc.Line[0].Function.Name] += value
			}
		}

		for _, loc := range sample.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					spaceCumMap[line.Function.Name] += value
				}
			}
		}
	}

	insights.TopAllocSpace = buildTopFunctions(spaceFlatMap, spaceCumMap, insights.AllocBytes, 10)

	// Build maps for objects
	if allocObjectsIdx >= 0 {
		objectsFlatMap := make(map[string]int64)
		objectsCumMap := make(map[string]int64)

		for _, sample := range prof.Sample {
			if allocObjectsIdx >= len(sample.Value) {
				continue
			}
			value := sample.Value[allocObjectsIdx]
			insights.AllocObjects += value

			if len(sample.Location) > 0 {
				loc := sample.Location[0]
				if len(loc.Line) > 0 && loc.Line[0].Function != nil {
					objectsFlatMap[loc.Line[0].Function.Name] += value
				}
			}

			for _, loc := range sample.Location {
				for _, line := range loc.Line {
					if line.Function != nil {
						objectsCumMap[line.Function.Name] += value
					}
				}
			}
		}

		insights.TopAllocCount = buildTopFunctions(objectsFlatMap, objectsCumMap, insights.AllocObjects, 10)
	}

	return insights, nil
}

func parseGoroutineProfile(path string) (GoroutineInsights, error) {
	f, err := os.Open(path)
	if err != nil {
		return GoroutineInsights{}, err
	}
	defer f.Close()

	prof, err := profile.Parse(f)
	if err != nil {
		return GoroutineInsights{}, err
	}

	insights := GoroutineInsights{}

	// Find goroutine count index
	goroutineIdx := -1
	for i, st := range prof.SampleType {
		if st.Type == "goroutine" || st.Type == "goroutines" {
			goroutineIdx = i
			break
		}
	}

	if goroutineIdx == -1 && len(prof.SampleType) > 0 {
		goroutineIdx = 0
	}

	// Group by top-of-stack function
	funcCounts := make(map[string]int64)

	for _, sample := range prof.Sample {
		if goroutineIdx >= len(sample.Value) {
			continue
		}
		count := sample.Value[goroutineIdx]
		insights.TotalCount += count

		// Get top function (leaf)
		if len(sample.Location) > 0 {
			loc := sample.Location[0]
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				funcCounts[loc.Line[0].Function.Name] += count
			}
		}
	}

	// Build top list
	type kv struct {
		fn    string
		count int64
	}
	var sorted []kv
	for fn, count := range funcCounts {
		sorted = append(sorted, kv{fn, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	for i := 0; i < len(sorted) && i < 10; i++ {
		insights.TopFunctions = append(insights.TopFunctions, GoroutineSample{
			Function: sorted[i].fn,
			Count:    sorted[i].count,
			Percent:  float64(sorted[i].count) * 100.0 / float64(insights.TotalCount),
		})
	}

	return insights, nil
}

func buildTopFunctions(flatMap, cumMap map[string]int64, total int64, limit int) []FunctionSample {
	type kv struct {
		fn   string
		flat int64
		cum  int64
	}

	var sorted []kv
	for fn, flat := range flatMap {
		sorted = append(sorted, kv{fn, flat, cumMap[fn]})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].flat > sorted[j].flat
	})

	var result []FunctionSample
	for i := 0; i < len(sorted) && i < limit; i++ {
		result = append(result, FunctionSample{
			Function:    sorted[i].fn,
			Flat:        sorted[i].flat,
			Cum:         sorted[i].cum,
			FlatPercent: float64(sorted[i].flat) * 100.0 / float64(total),
			CumPercent:  float64(sorted[i].cum) * 100.0 / float64(total),
		})
	}

	return result
}

func buildTopFunctionsByCum(flatMap, cumMap map[string]int64, total int64, limit int) []FunctionSample {
	type kv struct {
		fn   string
		flat int64
		cum  int64
	}

	var sorted []kv
	for fn, cum := range cumMap {
		sorted = append(sorted, kv{fn, flatMap[fn], cum})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].cum > sorted[j].cum
	})

	var result []FunctionSample
	for i := 0; i < len(sorted) && i < limit; i++ {
		result = append(result, FunctionSample{
			Function:    sorted[i].fn,
			Flat:        sorted[i].flat,
			Cum:         sorted[i].cum,
			FlatPercent: float64(sorted[i].flat) * 100.0 / float64(total),
			CumPercent:  float64(sorted[i].cum) * 100.0 / float64(total),
		})
	}

	return result
}

func buildTopPackages(flatMap map[string]int64, total int64, limit int) []PackageSample {
	pkgMap := make(map[string]int64)

	for fn, flat := range flatMap {
		pkg := extractPackage(fn)
		pkgMap[pkg] += flat
	}

	type kv struct {
		pkg  string
		flat int64
	}

	var sorted []kv
	for pkg, flat := range pkgMap {
		sorted = append(sorted, kv{pkg, flat})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].flat > sorted[j].flat
	})

	var result []PackageSample
	for i := 0; i < len(sorted) && i < limit; i++ {
		result = append(result, PackageSample{
			Package:     sorted[i].pkg,
			Flat:        sorted[i].flat,
			FlatPercent: float64(sorted[i].flat) * 100.0 / float64(total),
		})
	}

	return result
}

func extractPackage(functionName string) string {
	// Extract package path from fully qualified function name
	// e.g., "example.com/app/pkg/evidence.Collect" -> "example.com/app/pkg/evidence"
	// e.g., "example.com/app/pkg/storage.(*Writer).Flush" -> "example.com/app/pkg/storage"

	lastSlash := strings.LastIndex(functionName, "/")
	if lastSlash == -1 {
		// No slash, try to find package before last dot (skip method receivers)
		// Handle "pkg.(*Type).Method" or "pkg.Func"
		if idx := strings.Index(functionName, ".(*"); idx != -1 {
			return functionName[:idx]
		}
		lastDot := strings.LastIndex(functionName, ".")
		if lastDot == -1 {
			return functionName
		}
		return functionName[:lastDot]
	}

	// Find package part after last slash
	afterSlash := functionName[lastSlash+1:]

	// Handle method receivers: pkg.(*Type).Method -> pkg
	if idx := strings.Index(afterSlash, ".(*"); idx != -1 {
		return functionName[:lastSlash+1+idx]
	}

	// Find last dot (for simple functions)
	lastDot := strings.LastIndex(afterSlash, ".")
	if lastDot == -1 {
		return functionName
	}

	return functionName[:lastSlash+1+lastDot]
}

// TrimFunctionName returns a shortened version suitable for display
func TrimFunctionName(fullName string) string {
	// First, abbreviate generic type parameters [...] if present
	abbreviated := fullName
	if strings.Contains(fullName, "[") && strings.Contains(fullName, "]") {
		// Find the last '[' before the last ']'
		start := strings.Index(fullName, "[")
		end := strings.LastIndex(fullName, "]")
		if start < end && start >= 0 {
			// Replace the generic part with […]
			abbreviated = fullName[:start] + "[…]" + fullName[end+1:]
		}
	}

	// Keep the package and function name, trim the module path
	parts := strings.Split(abbreviated, "/")
	if len(parts) <= 2 {
		return abbreviated
	}

	// Take last 2-3 parts
	if len(parts) > 3 {
		return ".../" + strings.Join(parts[len(parts)-2:], "/")
	}

	return abbreviated
}

func buildHeadlineNumbers(current *ProfileInsights, baseline *ProfileInsights) HeadlineNumbers {
	hn := HeadlineNumbers{
		CPUTime:         current.CPU.CPUTimeNanos,
		CPUCores:        current.CPU.Utilization,
		HeapInUseBytes:  current.Memory.InUseBytes,
		TotalAllocBytes: current.Allocs.AllocBytes,
		AllocObjects:    current.Allocs.AllocObjects,
		GoroutineCount:  current.Goroutines.TotalCount,
	}

	if baseline != nil {
		hn.HasBaseline = true
		hn.CPUTimeDelta = current.CPU.CPUTimeNanos - baseline.CPU.CPUTimeNanos
		hn.CPUCoresDelta = current.CPU.Utilization - baseline.CPU.Utilization
		hn.HeapInUseBytesDelta = current.Memory.InUseBytes - baseline.Memory.InUseBytes
		hn.TotalAllocBytesDelta = current.Allocs.AllocBytes - baseline.Allocs.AllocBytes
		hn.AllocObjectsDelta = current.Allocs.AllocObjects - baseline.Allocs.AllocObjects
		hn.GoroutineCountDelta = current.Goroutines.TotalCount - baseline.Goroutines.TotalCount
	}

	return hn
}

func mergeCPUBaseline(current, baseline *CPUInsights) {
	// Build baseline maps
	baselineFlat := make(map[string]int64)
	baselineCum := make(map[string]int64)
	baselinePkgFlat := make(map[string]int64)

	for _, fn := range baseline.TopFlat {
		baselineFlat[fn.Function] = fn.Flat
		baselineCum[fn.Function] = fn.Cum
	}
	for _, fn := range baseline.TopCum {
		if _, ok := baselineCum[fn.Function]; !ok {
			baselineCum[fn.Function] = fn.Cum
		}
	}
	for _, pkg := range baseline.TopPackages {
		baselinePkgFlat[pkg.Package] = pkg.Flat
	}

	// Merge into current
	for i := range current.TopFlat {
		fn := &current.TopFlat[i]
		if baseFlat, ok := baselineFlat[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineFlat = baseFlat
			fn.FlatDelta = fn.Flat - baseFlat
		}
		if baseCum, ok := baselineCum[fn.Function]; ok {
			fn.BaselineCum = baseCum
			fn.CumDelta = fn.Cum - baseCum
		}
	}

	for i := range current.TopCum {
		fn := &current.TopCum[i]
		if baseCum, ok := baselineCum[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineCum = baseCum
			fn.CumDelta = fn.Cum - baseCum
		}
		if baseFlat, ok := baselineFlat[fn.Function]; ok {
			fn.BaselineFlat = baseFlat
			fn.FlatDelta = fn.Flat - baseFlat
		}
	}

	for i := range current.TopPackages {
		pkg := &current.TopPackages[i]
		if baseFlat, ok := baselinePkgFlat[pkg.Package]; ok {
			pkg.HasBaseline = true
			pkg.BaselineFlat = baseFlat
			pkg.FlatDelta = pkg.Flat - baseFlat
		}
	}
}

func mergeMemoryBaseline(current, baseline *MemoryInsights) {
	baselineMap := make(map[string]int64)
	for _, fn := range baseline.TopInUseSpace {
		baselineMap[fn.Function] = fn.Flat
	}

	for i := range current.TopInUseSpace {
		fn := &current.TopInUseSpace[i]
		if baseFlat, ok := baselineMap[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineFlat = baseFlat
			fn.FlatDelta = fn.Flat - baseFlat
		}
	}
}

func mergeAllocsBaseline(current, baseline *AllocsInsights) {
	baselineSpaceMap := make(map[string]int64)
	baselineCountMap := make(map[string]int64)

	for _, fn := range baseline.TopAllocSpace {
		baselineSpaceMap[fn.Function] = fn.Flat
	}
	for _, fn := range baseline.TopAllocCount {
		baselineCountMap[fn.Function] = fn.Flat
	}

	for i := range current.TopAllocSpace {
		fn := &current.TopAllocSpace[i]
		if baseFlat, ok := baselineSpaceMap[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineFlat = baseFlat
			fn.FlatDelta = fn.Flat - baseFlat
		}
	}

	for i := range current.TopAllocCount {
		fn := &current.TopAllocCount[i]
		if baseFlat, ok := baselineCountMap[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineFlat = baseFlat
			fn.FlatDelta = fn.Flat - baseFlat
		}
	}
}

func mergeGoroutineBaseline(current, baseline *GoroutineInsights) {
	baselineMap := make(map[string]int64)
	for _, fn := range baseline.TopFunctions {
		baselineMap[fn.Function] = fn.Count
	}

	for i := range current.TopFunctions {
		fn := &current.TopFunctions[i]
		if baseCount, ok := baselineMap[fn.Function]; ok {
			fn.HasBaseline = true
			fn.BaselineCount = baseCount
			fn.Delta = fn.Count - baseCount
		}
	}

	// Check for leak: goroutine count increased significantly
	if baseline.TotalCount > 0 {
		increasePercent := float64(current.TotalCount-baseline.TotalCount) * 100.0 / float64(baseline.TotalCount)
		if increasePercent > 50 && current.TotalCount-baseline.TotalCount > 100 {
			current.LikelyLeak = true
			current.LeakDescription = fmt.Sprintf("Goroutine count increased by %.0f%% (%d -> %d)",
				increasePercent, baseline.TotalCount, current.TotalCount)
		}
	}
}
