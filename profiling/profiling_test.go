package profiling

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// newTestServer starts the control server on an ephemeral port and returns
// its controller.
func newTestServer(t *testing.T) *controller {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	c, err := start(ctx, Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return c
}

func get(t *testing.T, addr, path string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s%s", addr, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s response: %v", path, err)
	}
	return resp.StatusCode, body
}

// TestSaveRequiresStart asserts the capture gate: /save must refuse until
// /start enables it, and refuse again after /stop.
func TestSaveRequiresStart(t *testing.T) {
	c := newTestServer(t)

	if code, body := get(t, c.addr, "/save"); code != http.StatusForbidden {
		t.Fatalf("save before start: code %d body %q, want %d", code, body, http.StatusForbidden)
	}

	if code, _ := get(t, c.addr, "/start"); code != http.StatusOK {
		t.Fatalf("start: code %d, want %d", code, http.StatusOK)
	}
	if code, _ := get(t, c.addr, "/save"); code != http.StatusOK {
		t.Fatalf("save after start: code %d, want %d", code, http.StatusOK)
	}

	if code, _ := get(t, c.addr, "/stop"); code != http.StatusOK {
		t.Fatalf("stop: code %d, want %d", code, http.StatusOK)
	}
	if code, _ := get(t, c.addr, "/save"); code != http.StatusForbidden {
		t.Fatalf("save after stop: code %d, want %d", code, http.StatusForbidden)
	}
}

// TestSaveSnapshotContents verifies the zip contains the core profiles plus
// the metadata and human-readable stack dump.
func TestSaveSnapshotContents(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")

	code, body := get(t, c.addr, "/save")
	if code != http.StatusOK {
		t.Fatalf("save: code %d, want %d", code, http.StatusOK)
	}
	assertZipContains(t, body, "heap.pprof", "allocs.pprof", "goroutine.pprof",
		"goroutine-stacks.txt", "meta.txt")
}

// TestSaveWithCPUProfile asserts that ?seconds=N adds a CPU profile.
func TestSaveWithCPUProfile(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")

	code, body := get(t, c.addr, "/save?seconds=1")
	if code != http.StatusOK {
		t.Fatalf("save with cpu: code %d, want %d", code, http.StatusOK)
	}
	assertZipContains(t, body, "cpu.pprof", "heap.pprof")
}

func TestSaveRejectsInvalidSeconds(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")

	for _, q := range []string{"?seconds=-1", "?seconds=abc", fmt.Sprintf("?seconds=%d", maxCPUProfileSeconds+1)} {
		if code, _ := get(t, c.addr, "/save"+q); code != http.StatusBadRequest {
			t.Errorf("save%s: code %d, want %d", q, code, http.StatusBadRequest)
		}
	}
}

// TestIndexShowsUsage asserts the root path documents the endpoints.
func TestIndexShowsUsage(t *testing.T) {
	c := newTestServer(t)

	code, body := get(t, c.addr, "/")
	if code != http.StatusOK {
		t.Fatalf("index: code %d, want %d", code, http.StatusOK)
	}
	for _, want := range []string{"/start", "/stop", "/save"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("index page does not mention %s", want)
		}
	}
}

// TestServerStopsOnContextCancel asserts the server shuts down with the
// application context.
func TestServerStopsOnContextCancel(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	c, err := start(ctx, Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	get(t, c.addr, "/start") // server is up

	cancel()

	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		resp, gerr := client.Get(fmt.Sprintf("http://%s/", c.addr))
		if gerr != nil {
			return // connection refused: server is down
		}
		_ = resp.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server still reachable after context cancel")
}

// assertZipContains parses body as a zip archive and asserts the named
// entries exist and are non-empty.
func assertZipContains(t *testing.T, body []byte, names ...string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}
	entries := make(map[string]uint64, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f.UncompressedSize64
	}
	for _, name := range names {
		size, ok := entries[name]
		if !ok {
			t.Errorf("zip missing %q (has %v)", name, keys(entries))
			continue
		}
		if size == 0 {
			t.Errorf("zip entry %q is empty", name)
		}
	}
}

func keys(m map[string]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCPUStartStop verifies continuous CPU profiling endpoints.
func TestCPUStartStop(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")

	// Start continuous CPU profiling
	code, _ := get(t, c.addr, "/cpu/start")
	if code != http.StatusOK {
		t.Fatalf("/cpu/start: code %d, want %d", code, http.StatusOK)
	}

	// Do some CPU work to generate profile data
	time.Sleep(100 * time.Millisecond)
	_ = doWork()

	// Stop and retrieve profile
	code, body := get(t, c.addr, "/cpu/stop")
	if code != http.StatusOK {
		t.Fatalf("/cpu/stop: code %d, want %d", code, http.StatusOK)
	}

	// Verify we got a non-empty profile
	if len(body) == 0 {
		t.Fatal("cpu profile is empty")
	}

	// Profile should be parseable (basic sanity check - it's a binary format)
	if len(body) < 100 {
		t.Errorf("cpu profile suspiciously small: %d bytes", len(body))
	}
}

// TestCPUStartRequiresEnabled verifies /cpu/start requires /start first.
func TestCPUStartRequiresEnabled(t *testing.T) {
	c := newTestServer(t)

	code, _ := get(t, c.addr, "/cpu/start")
	if code != http.StatusForbidden {
		t.Fatalf("/cpu/start without /start: code %d, want %d", code, http.StatusForbidden)
	}
}

// TestCPUDoubleStart verifies starting CPU profiling twice fails.
func TestCPUDoubleStart(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")
	get(t, c.addr, "/cpu/start")

	code, _ := get(t, c.addr, "/cpu/start")
	if code != http.StatusConflict {
		t.Fatalf("second /cpu/start: code %d, want %d", code, http.StatusConflict)
	}
}

// TestCPUStopWithoutStart verifies stopping when not running fails.
func TestCPUStopWithoutStart(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")

	code, _ := get(t, c.addr, "/cpu/stop")
	if code != http.StatusConflict {
		t.Fatalf("/cpu/stop without /cpu/start: code %d, want %d", code, http.StatusConflict)
	}
}

// TestCPUStopRequiresEnabled verifies /cpu/stop requires /start first.
func TestCPUStopRequiresEnabled(t *testing.T) {
	c := newTestServer(t)

	code, _ := get(t, c.addr, "/cpu/stop")
	if code != http.StatusForbidden {
		t.Fatalf("/cpu/stop without /start: code %d, want %d", code, http.StatusForbidden)
	}
}

// TestSaveConflictsWithContinuousCPU verifies /save?seconds=N fails gracefully
// when continuous CPU profiling is running.
func TestSaveConflictsWithContinuousCPU(t *testing.T) {
	c := newTestServer(t)
	get(t, c.addr, "/start")
	get(t, c.addr, "/cpu/start")

	// /save?seconds=N should succeed but include an error about CPU
	code, body := get(t, c.addr, "/save?seconds=1")
	if code != http.StatusOK {
		t.Fatalf("/save?seconds=1 with continuous CPU: code %d, want %d", code, http.StatusOK)
	}

	// Check that the zip contains an errors.txt mentioning CPU conflict
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}

	var errorsFound bool
	for _, f := range zr.File {
		if f.Name == "errors.txt" {
			errorsFound = true
			rc, rerr := f.Open()
			if rerr != nil {
				t.Fatalf("open errors.txt: %v", rerr)
			}
			errText, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(errText), "continuous CPU profiling") {
				t.Errorf("errors.txt does not mention continuous CPU profiling: %s", errText)
			}
		}
	}

	if !errorsFound {
		t.Error("errors.txt not found in zip when continuous CPU profiling is running")
	}

	// Stop continuous profiling
	get(t, c.addr, "/cpu/stop")
}

// TestCPUProfilingStopsOnShutdown verifies CPU profiling is cleaned up on context cancel.
func TestCPUProfilingStopsOnShutdown(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	c, err := start(ctx, Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	get(t, c.addr, "/start")
	get(t, c.addr, "/cpu/start")

	// Verify CPU profiling is running
	c.cpuMu.Lock()
	running := c.cpuRunning
	c.cpuMu.Unlock()
	if !running {
		t.Fatal("CPU profiling not running after /cpu/start")
	}

	// Cancel context (shutdown)
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Verify CPU profiling was stopped
	c.cpuMu.Lock()
	running = c.cpuRunning
	c.cpuMu.Unlock()
	if running {
		t.Error("CPU profiling still running after shutdown")
	}
}

// doWork performs some CPU work to generate profile samples.
func doWork() int {
	sum := 0
	for i := 0; i < 1000000; i++ {
		sum += i * i
	}
	return sum
}
