package profiling

import (
	"archive/zip"
	"context"
	"path/filepath"
	"testing"
)

func TestClientStartCollect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := start(ctx, Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("start profiling server: %v", err)
	}
	client := &Client{Addr: srv.addr}

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client start: %v", err)
	}

	zipPath := filepath.Join(t.TempDir(), "profiles.zip")
	result, err := client.Collect(ctx, zipPath)
	if err != nil {
		t.Fatalf("client collect: %v", err)
	}
	if result.CPUError != "" {
		t.Fatalf("unexpected CPU error: %s", result.CPUError)
	}
	if !result.HasAll() || !result.Has("goroutine-stacks.txt") {
		t.Fatalf("collected files = %v, want cpu, heap, allocs, goroutine and goroutine-stacks", result.Files)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open %s: %v", zipPath, err)
	}
	defer r.Close()
	if len(r.File) != len(result.Files) {
		t.Fatalf("archive has %d entries, result reports %v", len(r.File), result.Files)
	}

	if srv.enabled.Load() {
		t.Fatal("capture still enabled after Collect; want /stop to be called")
	}
}

func TestClientCollectWithoutCPU(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := start(ctx, Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("start profiling server: %v", err)
	}
	client := &Client{Addr: srv.addr}
	if _, err := client.get(ctx, "/start"); err != nil {
		t.Fatalf("enable capture: %v", err)
	}

	result, err := client.Collect(ctx, filepath.Join(t.TempDir(), "profiles.zip"))
	if err != nil {
		t.Fatalf("client collect: %v", err)
	}
	if result.CPUError == "" || result.Has("cpu.pprof") {
		t.Fatalf("want missing cpu.pprof with CPUError set, got files=%v cpuError=%q", result.Files, result.CPUError)
	}
	if !result.Has("heap.pprof") {
		t.Fatalf("collected files = %v, want heap.pprof", result.Files)
	}
}
