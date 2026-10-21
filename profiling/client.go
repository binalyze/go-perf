package profiling

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// archivedProfiles lists the /save archive entries kept by Client.Collect,
// in addition to cpu.pprof from /cpu/stop.
var archivedProfiles = map[string]bool{
	"heap.pprof":           true,
	"allocs.pprof":         true,
	"goroutine.pprof":      true,
	"goroutine-stacks.txt": true,
}

// Client drives a profiling control server (see Start) from a test harness
// or CLI: Start enables capture and continuous CPU profiling, Collect stops it
// and writes the profiles.zip layout consumed by go-perf.
type Client struct {
	// Addr is the control server address, e.g. "127.0.0.1:6060".
	Addr string

	// HTTPClient is used for all requests; nil means a client with a 30s
	// timeout.
	HTTPClient *http.Client
}

// CollectResult describes the archive written by Collect.
type CollectResult struct {
	// Files lists the archive entries, sorted by name.
	Files []string

	// CPUError explains why cpu.pprof is missing; empty when it is present.
	CPUError string

	// Bytes is the total uncompressed size of all entries.
	Bytes int
}

// HasAll reports whether the CPU, heap, allocs and goroutine profiles were
// all collected.
func (r *CollectResult) HasAll() bool {
	want := []string{"cpu.pprof", "heap.pprof", "allocs.pprof", "goroutine.pprof"}
	for _, name := range want {
		if !r.Has(name) {
			return false
		}
	}
	return true
}

// Has reports whether the archive contains the named entry.
func (r *CollectResult) Has(name string) bool {
	i := sort.SearchStrings(r.Files, name)
	return i < len(r.Files) && r.Files[i] == name
}

// Start enables snapshot capture and starts continuous CPU profiling.
func (c *Client) Start(ctx context.Context) error {
	if _, err := c.get(ctx, "/start"); err != nil {
		return fmt.Errorf("enable capture: %w", err)
	}
	if _, err := c.get(ctx, "/cpu/start"); err != nil {
		return fmt.Errorf("start continuous CPU profiling: %w", err)
	}
	return nil
}

// Collect stops continuous CPU profiling, fetches the snapshot profiles,
// disables capture and writes everything to zipPath. A missing CPU profile is
// reported in CollectResult.CPUError rather than failing the collection.
func (c *Client) Collect(ctx context.Context, zipPath string) (*CollectResult, error) {
	files := make(map[string][]byte)
	result := &CollectResult{}

	cpuData, err := c.get(ctx, "/cpu/stop")
	switch {
	case err != nil:
		result.CPUError = err.Error()
	case len(cpuData) == 0:
		result.CPUError = "GET /cpu/stop returned an empty profile"
	default:
		files["cpu.pprof"] = cpuData
	}

	saveData, err := c.get(ctx, "/save")
	if err != nil {
		return nil, fmt.Errorf("fetch snapshot profiles: %w", err)
	}
	snapshot, err := zip.NewReader(bytes.NewReader(saveData), int64(len(saveData)))
	if err != nil {
		return nil, fmt.Errorf("parse /save archive (%d bytes): %w", len(saveData), err)
	}
	for _, entry := range snapshot.File {
		if !archivedProfiles[entry.Name] {
			continue
		}
		data, err := readZipEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("read %s from /save archive: %w", entry.Name, err)
		}
		files[entry.Name] = data
	}

	// Disabling capture is best-effort: the profiles are already in hand.
	_, _ = c.get(ctx, "/stop")

	if err := writeArchive(zipPath, files); err != nil {
		return nil, err
	}

	for name, data := range files {
		result.Files = append(result.Files, name)
		result.Bytes += len(data)
	}
	sort.Strings(result.Files)
	return result, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	url := "http://" + c.Addr + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read body: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func writeArchive(zipPath string, files map[string][]byte) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", zipPath, err)
	}
	defer f.Close()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	zw := zip.NewWriter(f)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create %s in %s: %w", name, zipPath, err)
		}
		if _, err := w.Write(files[name]); err != nil {
			return fmt.Errorf("write %s to %s: %w", name, zipPath, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize %s: %w", zipPath, err)
	}
	return f.Close()
}
