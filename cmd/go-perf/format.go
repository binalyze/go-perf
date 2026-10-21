package main

import (
	"fmt"
	"strings"
)

// formatBytes formats bytes into human-readable units
func formatBytes(bytes int64) string {
	if bytes < 0 {
		return "-" + formatBytes(-bytes)
	}

	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// formatNumber formats large numbers with thousands separators
func formatNumber(n int64) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}

	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(c)
	}

	return result.String()
}

// formatNanoseconds formats nanoseconds into human-readable duration
func formatNanoseconds(nanos int64) string {
	if nanos < 0 {
		return "-" + formatNanoseconds(-nanos)
	}

	const (
		Second = 1000000000
		Minute = 60 * Second
	)

	switch {
	case nanos >= Minute:
		minutes := nanos / Minute
		seconds := (nanos % Minute) / Second
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	case nanos >= Second:
		return fmt.Sprintf("%.2fs", float64(nanos)/float64(Second))
	case nanos >= 1000000:
		return fmt.Sprintf("%.1fms", float64(nanos)/1000000.0)
	case nanos >= 1000:
		return fmt.Sprintf("%.1fµs", float64(nanos)/1000.0)
	default:
		return fmt.Sprintf("%dns", nanos)
	}
}

// formatPercent formats a percentage with sign
func formatPercent(percent float64) string {
	if percent > 0 {
		return fmt.Sprintf("+%.1f%%", percent)
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// formatDelta formats a delta with appropriate units and sign
func formatDeltaBytes(delta int64) string {
	if delta == 0 {
		return "0"
	}
	prefix := ""
	if delta > 0 {
		prefix = "+"
	}
	return prefix + formatBytes(delta)
}

func formatDeltaNumber(delta int64) string {
	if delta == 0 {
		return "0"
	}
	prefix := ""
	if delta > 0 {
		prefix = "+"
	}
	return prefix + formatNumber(delta)
}

func formatDeltaCores(delta float64) string {
	if delta == 0 {
		return "0"
	}
	if delta > 0 {
		return fmt.Sprintf("+%.2f", delta)
	}
	return fmt.Sprintf("%.2f", delta)
}

// formatSampleValue formats a sample value with its unit
func formatSampleValue(value int64, unit string) string {
	switch unit {
	case "nanoseconds":
		return formatNanoseconds(value)
	case "bytes":
		return formatBytes(value)
	case "count":
		return formatNumber(value)
	default:
		return formatNumber(value)
	}
}

// formatSampleDelta formats a sample delta with its unit and sign
func formatSampleDelta(delta int64, unit string) string {
	if delta == 0 {
		return "0"
	}
	prefix := ""
	if delta > 0 {
		prefix = "+"
	}
	switch unit {
	case "nanoseconds":
		return prefix + formatNanoseconds(delta)
	case "bytes":
		return prefix + formatBytes(delta)
	case "count":
		return prefix + formatNumber(delta)
	default:
		return prefix + formatNumber(delta)
	}
}

// formatChangePercent formats a percentage with adaptive precision.
// For small percentages (< 0.1%), use 3 decimal places to show meaningful precision.
// Otherwise use 2 decimal places.
func formatChangePercent(percent float64) string {
	absPercent := percent
	if absPercent < 0 {
		absPercent = -absPercent
	}

	if absPercent < 0.1 && absPercent > 0 {
		return fmt.Sprintf("%.3f", percent)
	}
	return fmt.Sprintf("%.2f", percent)
}
