package main

import (
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1572864, "1.50 MB"},
		{1073741824, "1.00 GB"},
		{2147483648, "2.00 GB"},
		{-1024, "-1.00 KB"},
		{-1073741824, "-1.00 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{12, "12"},
		{123, "123"},
		{1234, "1,234"},
		{12345, "12,345"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{12345678, "12,345,678"},
		{1234567890, "1,234,567,890"},
		{-1234, "-1,234"},
		{-1234567, "-1,234,567"},
	}

	for _, tt := range tests {
		got := formatNumber(tt.n)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatNanoseconds(t *testing.T) {
	tests := []struct {
		nanos int64
		want  string
	}{
		{0, "0ns"},
		{500, "500ns"},
		{1000, "1.0µs"},
		{1500, "1.5µs"},
		{1000000, "1.0ms"},
		{1500000, "1.5ms"},
		{1000000000, "1.00s"},
		{2500000000, "2.50s"},
		{60000000000, "1m 00s"},
		{125000000000, "2m 05s"},
		{3661000000000, "61m 01s"},
		{-1000000000, "-1.00s"},
		{-125000000000, "-2m 05s"},
	}

	for _, tt := range tests {
		got := formatNanoseconds(tt.nanos)
		if got != tt.want {
			t.Errorf("formatNanoseconds(%d) = %q, want %q", tt.nanos, got, tt.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		percent float64
		want    string
	}{
		{0, "0.0%"},
		{5.5, "+5.5%"},
		{-5.5, "-5.5%"},
		{10.123, "+10.1%"},
		{-10.456, "-10.5%"},
	}

	for _, tt := range tests {
		got := formatPercent(tt.percent)
		if got != tt.want {
			t.Errorf("formatPercent(%.3f) = %q, want %q", tt.percent, got, tt.want)
		}
	}
}

func TestFormatDeltaBytes(t *testing.T) {
	tests := []struct {
		delta int64
		want  string
	}{
		{0, "0"},
		{1024, "+1.00 KB"},
		{-1024, "-1.00 KB"},
		{1048576, "+1.00 MB"},
		{-1073741824, "-1.00 GB"},
	}

	for _, tt := range tests {
		got := formatDeltaBytes(tt.delta)
		if got != tt.want {
			t.Errorf("formatDeltaBytes(%d) = %q, want %q", tt.delta, got, tt.want)
		}
	}
}

func TestFormatDeltaNumber(t *testing.T) {
	tests := []struct {
		delta int64
		want  string
	}{
		{0, "0"},
		{1234, "+1,234"},
		{-1234, "-1,234"},
		{1234567, "+1,234,567"},
		{-9876543, "-9,876,543"},
	}

	for _, tt := range tests {
		got := formatDeltaNumber(tt.delta)
		if got != tt.want {
			t.Errorf("formatDeltaNumber(%d) = %q, want %q", tt.delta, got, tt.want)
		}
	}
}

func TestFormatDeltaCores(t *testing.T) {
	tests := []struct {
		delta float64
		want  string
	}{
		{0, "0"},
		{0.5, "+0.50"},
		{-0.5, "-0.50"},
		{1.234, "+1.23"},
		{-2.567, "-2.57"},
	}

	for _, tt := range tests {
		got := formatDeltaCores(tt.delta)
		if got != tt.want {
			t.Errorf("formatDeltaCores(%.3f) = %q, want %q", tt.delta, got, tt.want)
		}
	}
}
