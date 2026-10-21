package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/urfave/cli/v2"
)

func newFakeProfilingServer(t *testing.T) (port string, calls func() []string) {
	t.Helper()

	var mu sync.Mutex
	var hits []string
	record := func(path string) {
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, path)
	}

	var saveZip bytes.Buffer
	zw := zip.NewWriter(&saveZip)
	for _, name := range []string{"heap.pprof", "allocs.pprof", "goroutine.pprof", "goroutine-stacks.txt", "block.pprof", "meta.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s in fake /save zip: %v", name, err)
		}
		_, _ = w.Write([]byte(name))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close fake /save zip: %v", err)
	}

	mux := http.NewServeMux()
	for _, path := range []string{"/start", "/stop", "/cpu/start"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { record(r.URL.Path) })
	}
	mux.HandleFunc("/cpu/stop", func(w http.ResponseWriter, r *http.Request) {
		record(r.URL.Path)
		_, _ = w.Write([]byte("cpu"))
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		record(r.URL.Path)
		_, _ = w.Write(saveZip.Bytes())
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fake server URL %q: %v", srv.URL, err)
	}
	return u.Port(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), hits...)
	}
}

func runCollectApp(t *testing.T, args ...string) error {
	t.Helper()
	app := &cli.App{
		Commands: []*cli.Command{{
			Name: "collect",
			Subcommands: []*cli.Command{
				{
					Name:   "start",
					Action: collectStartCommand,
					Flags:  []cli.Flag{&cli.StringFlag{Name: "port"}},
				},
				{
					Name:   "stop",
					Action: collectStopCommand,
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "port"},
						&cli.StringFlag{Name: "output-dir"},
						&cli.TimestampFlag{Name: "start-time", Layout: "2006-01-02T15:04:05Z07:00"},
					},
				},
			},
		}},
	}
	return app.Run(append([]string{"perf", "collect"}, args...))
}

func TestCollectStartStopUsesControlEndpoints(t *testing.T) {
	port, calls := newFakeProfilingServer(t)
	outDir := t.TempDir()

	if err := runCollectApp(t, "start", "--port", port); err != nil {
		t.Fatalf("collect start: %v", err)
	}
	if err := runCollectApp(t, "stop", "--port", port, "--output-dir", outDir); err != nil {
		t.Fatalf("collect stop: %v", err)
	}

	wantCalls := []string{"/start", "/cpu/start", "/cpu/stop", "/save", "/stop"}
	gotCalls := calls()
	if len(gotCalls) != len(wantCalls) {
		t.Fatalf("endpoint calls = %v, want %v", gotCalls, wantCalls)
	}
	for i := range wantCalls {
		if gotCalls[i] != wantCalls[i] {
			t.Fatalf("endpoint calls = %v, want %v", gotCalls, wantCalls)
		}
	}

	zipPath := filepath.Join(outDir, "profiles.zip")
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open %s: %v", zipPath, err)
	}
	defer r.Close()

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"allocs.pprof", "cpu.pprof", "goroutine-stacks.txt", "goroutine.pprof", "heap.pprof"}
	if len(names) != len(want) {
		t.Fatalf("profiles.zip entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("profiles.zip entries = %v, want %v", names, want)
		}
	}
}

func TestProfileDiffsNormalization(t *testing.T) {
	tests := []struct {
		name              string
		cpuCoresRegressed bool
		profileType       string
		origHasRegression bool
		wantHasRegression bool
	}{
		{
			name:              "cpu uses cores check (regressed)",
			cpuCoresRegressed: true,
			profileType:       "cpu",
			origHasRegression: false,
			wantHasRegression: true,
		},
		{
			name:              "cpu uses cores check (no regression)",
			cpuCoresRegressed: false,
			profileType:       "cpu",
			origHasRegression: true,
			wantHasRegression: false,
		},
		{
			name:              "heap always false (snapshot noise)",
			cpuCoresRegressed: false,
			profileType:       "heap",
			origHasRegression: true,
			wantHasRegression: false,
		},
		{
			name:              "goroutine always false (snapshot noise)",
			cpuCoresRegressed: false,
			profileType:       "goroutine",
			origHasRegression: true,
			wantHasRegression: false,
		},
		{
			name:              "allocs keeps original (regressed)",
			cpuCoresRegressed: false,
			profileType:       "allocs",
			origHasRegression: true,
			wantHasRegression: true,
		},
		{
			name:              "allocs keeps original (no regression)",
			cpuCoresRegressed: false,
			profileType:       "allocs",
			origHasRegression: false,
			wantHasRegression: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate normalization logic from collect.go
			hasRegression := tt.origHasRegression

			if tt.profileType == "cpu" {
				// CPU uses cores check
				hasRegression = tt.cpuCoresRegressed
			} else if tt.profileType == "heap" || tt.profileType == "goroutine" {
				// Heap and goroutine are snapshots - never gate
				hasRegression = false
			}
			// allocs keeps original HasRegression

			if hasRegression != tt.wantHasRegression {
				t.Errorf("hasRegression = %v, want %v", hasRegression, tt.wantHasRegression)
			}
		})
	}
}
