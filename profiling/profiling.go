// Package profiling exposes a small, loopback-only HTTP control endpoint for
// capturing runtime profile snapshots (heap, allocs, goroutine, ...) as a
// single zip archive, so production issues (e.g. OOMs) can be diagnosed
// offline: enable capture, download one file from inside the container, and
// analyze it locally with `go tool pprof`. The target host needs no Go
// tooling.
//
// The server always listens (loopback only, so it is unreachable from outside
// the host/container), and snapshot capture is gated behind an explicit
// switch. The whole flow works from a docker exec shell:
//
//	curl http://127.0.0.1:6060/start
//	curl -o profiles.zip http://127.0.0.1:6060/save
//	curl http://127.0.0.1:6060/stop
package profiling

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultAddr is loopback-only on purpose: the profiles must never be
	// reachable from outside the host unless explicitly configured.
	DefaultAddr = "127.0.0.1:6060"

	// maxCPUProfileSeconds caps the optional CPU profile duration requested
	// via /save?seconds=N so a typo cannot hold the handler for hours.
	maxCPUProfileSeconds = 300
)

// Options configures the profiling control server.
type Options struct {
	// Addr is the listen address; empty means DefaultAddr. Binding to a
	// non-loopback interface exposes runtime internals (memory contents may
	// include sensitive data), so only widen it deliberately, e.g. inside an
	// isolated container network.
	Addr string

	// Logger receives server events; nil means slog.Default().
	Logger *slog.Logger
}

// Start launches the profiling control server on opts.Addr. It serves
// /start and /stop to enable and disable capture, /save to return all runtime
// profiles as one zip archive, and /cpu/start and /cpu/stop for continuous CPU
// profiling. The server shuts down when ctx is cancelled. A listen failure is
// logged, not fatal: profiling must never take the application down.
func Start(ctx context.Context, opts Options) {
	if _, err := start(ctx, opts); err != nil {
		loggerOrDefault(opts.Logger).Error("profiling: start failed", "error", err)
	}
}

func loggerOrDefault(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

// start is the testable core of Start, returning the controller with its
// bound address.
func start(ctx context.Context, opts Options) (*controller, error) {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %q: %w", addr, err)
	}

	c := &controller{
		addr:   ln.Addr().String(),
		ctx:    ctx,
		log:    loggerOrDefault(opts.Logger),
		server: &http.Server{ReadHeaderTimeout: 10 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/start", c.handleStart)
	mux.HandleFunc("/stop", c.handleStop)
	mux.HandleFunc("/save", c.handleSave)
	mux.HandleFunc("/cpu/start", c.handleCPUStart)
	mux.HandleFunc("/cpu/stop", c.handleCPUStop)
	mux.HandleFunc("/", c.handleIndex)

	c.server.Handler = mux
	go func() {
		if serr := c.server.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			c.log.Error("profiling: server stopped", "addr", c.addr, "error", serr)
		}
	}()
	go func() {
		<-ctx.Done()
		c.stopCPUProfiling() // Clean up any running CPU profile
		_ = c.server.Close()
	}()

	c.log.Info("profiling: control endpoint listening (GET /start, /stop, /save, /cpu/start, /cpu/stop)", "addr", c.addr)
	return c, nil
}

// controller gates snapshot capture behind an explicit /start, so runtime
// internals are only exposed while someone is actively debugging.
type controller struct {
	addr    string
	log     *slog.Logger
	enabled atomic.Bool
	ctx     context.Context
	server  *http.Server

	// Continuous CPU profiling state
	cpuMu      sync.Mutex
	cpuBuf     *bytes.Buffer
	cpuRunning bool
}

func (c *controller) handleStart(w http.ResponseWriter, r *http.Request) {
	c.enabled.Store(true)
	c.log.Warn("profiling: capture enabled via /start", "remote", r.RemoteAddr)
	fmt.Fprintf(w, "profiling enabled; GET /save to download a snapshot, GET /stop when done\n")
}

func (c *controller) handleStop(w http.ResponseWriter, r *http.Request) {
	c.enabled.Store(false)
	c.log.Info("profiling: capture disabled via /stop", "remote", r.RemoteAddr)
	fmt.Fprintf(w, "profiling disabled\n")
}

// handleIndex documents the endpoint so anyone hitting the root URL gets
// usage instructions instead of a 404.
func (c *controller) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "Profiling snapshot endpoint.\n\n"+
		"GET /start            -> enable snapshot capture\n"+
		"GET /stop             -> disable snapshot capture\n"+
		"GET /save             -> zip with all runtime profiles (heap, allocs, goroutine, ...)\n"+
		"GET /save?seconds=30  -> same, plus a CPU profile sampled for 30s (max %d)\n"+
		"GET /cpu/start        -> start continuous CPU profiling\n"+
		"GET /cpu/stop         -> stop continuous CPU profiling and return cpu.pprof\n\n"+
		"Example: curl http://%s/start && curl http://%s/cpu/start && ... && curl -o cpu.pprof http://%s/cpu/stop\n",
		maxCPUProfileSeconds, r.Host, r.Host, r.Host)
}

// handleCPUStart begins continuous CPU profiling.
func (c *controller) handleCPUStart(w http.ResponseWriter, r *http.Request) {
	if !c.enabled.Load() {
		http.Error(w, "profiling not enabled: GET /start first", http.StatusForbidden)
		return
	}

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	if c.cpuRunning {
		http.Error(w, "CPU profiling already running", http.StatusConflict)
		return
	}

	c.cpuBuf = &bytes.Buffer{}
	if err := pprof.StartCPUProfile(c.cpuBuf); err != nil {
		http.Error(w, fmt.Sprintf("failed to start CPU profile: %v", err), http.StatusInternalServerError)
		return
	}

	c.cpuRunning = true
	c.log.Info("profiling: continuous CPU profiling started via /cpu/start", "remote", r.RemoteAddr)
	fmt.Fprintf(w, "continuous CPU profiling started; GET /cpu/stop to download cpu.pprof\n")
}

// handleCPUStop stops continuous CPU profiling and returns the profile.
func (c *controller) handleCPUStop(w http.ResponseWriter, r *http.Request) {
	if !c.enabled.Load() {
		http.Error(w, "profiling not enabled: GET /start first", http.StatusForbidden)
		return
	}

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	if !c.cpuRunning {
		http.Error(w, "CPU profiling not running", http.StatusConflict)
		return
	}

	pprof.StopCPUProfile()
	c.cpuRunning = false

	profileData := c.cpuBuf.Bytes()
	c.cpuBuf = nil

	c.log.Info("profiling: continuous CPU profiling stopped via /cpu/stop",
		"remote", r.RemoteAddr, "bytes", len(profileData))

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="cpu-%s.pprof"`, time.Now().UTC().Format("20060102-150405")))
	w.Write(profileData)
}

// stopCPUProfiling stops any running CPU profile (called on shutdown).
func (c *controller) stopCPUProfiling() {
	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	if c.cpuRunning {
		pprof.StopCPUProfile()
		c.cpuRunning = false
		c.log.Info("profiling: stopped CPU profiling on shutdown")
	}
}

// handleSave captures every runtime profile and streams them as one zip
// archive. With ?seconds=N it additionally samples a CPU profile for N
// seconds before responding. Capture is best-effort: a failing profile is
// reported inside the archive (errors.txt) rather than aborting the download.
func (c *controller) handleSave(w http.ResponseWriter, r *http.Request) {
	if !c.enabled.Load() {
		http.Error(w, "profiling not enabled: GET /start first", http.StatusForbidden)
		return
	}

	seconds, err := parseSeconds(r.URL.Query().Get("seconds"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c.log.Info("profiling: snapshot requested", "remote", r.RemoteAddr, "cpu_seconds", seconds)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="profiles-%s.zip"`, time.Now().UTC().Format("20060102-150405")))

	zw := zip.NewWriter(w)
	var errs []string

	addEntry := func(name string, write func(w *zip.Writer) error) {
		if werr := write(zw); werr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, werr))
			c.log.Error("profiling: capture failed", "profile", name, "error", werr)
		}
	}

	// Run a GC first so the heap profile reflects live objects right now
	// rather than garbage from before the last collection (same as the
	// standard pprof handler's ?gc=1).
	runtime.GC()

	// All registered runtime profiles (heap, allocs, goroutine, threadcreate,
	// block, mutex) in the binary format `go tool pprof` consumes.
	for _, p := range pprof.Profiles() {
		prof := p
		addEntry(prof.Name(), func(zw *zip.Writer) error {
			f, cerr := zw.Create(prof.Name() + ".pprof")
			if cerr != nil {
				return cerr
			}
			return prof.WriteTo(f, 0)
		})
	}

	// Human-readable goroutine stacks: invaluable for spotting leaks/stalls
	// without any tooling at all.
	addEntry("goroutine-stacks", func(zw *zip.Writer) error {
		f, cerr := zw.Create("goroutine-stacks.txt")
		if cerr != nil {
			return cerr
		}
		return pprof.Lookup("goroutine").WriteTo(f, 2)
	})

	addEntry("meta", func(zw *zip.Writer) error {
		f, cerr := zw.Create("meta.txt")
		if cerr != nil {
			return cerr
		}
		return writeMeta(f)
	})

	if seconds > 0 {
		// Check if continuous CPU profiling is running
		c.cpuMu.Lock()
		continuousRunning := c.cpuRunning
		c.cpuMu.Unlock()

		if continuousRunning {
			errs = append(errs, "cpu: cannot sample CPU profile via /save?seconds=N while continuous CPU profiling is running (use /cpu/stop instead)")
		} else {
			addEntry("cpu", func(zw *zip.Writer) error {
				f, cerr := zw.Create("cpu.pprof")
				if cerr != nil {
					return cerr
				}
				if perr := pprof.StartCPUProfile(f); perr != nil {
					// E.g. another CPU profile is already running.
					return perr
				}
				select {
				case <-time.After(time.Duration(seconds) * time.Second):
				case <-r.Context().Done():
				}
				pprof.StopCPUProfile()
				return r.Context().Err()
			})
		}
	}

	if len(errs) > 0 {
		if f, cerr := zw.Create("errors.txt"); cerr == nil {
			_, _ = fmt.Fprintln(f, strings.Join(errs, "\n"))
		}
	}

	if cerr := zw.Close(); cerr != nil {
		c.log.Error("profiling: finalize snapshot zip", "error", cerr)
	}
}

// parseSeconds validates the optional CPU profile duration; "" means no CPU
// profile.
func parseSeconds(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("invalid seconds %q: want an integer in [0, %d]", raw, maxCPUProfileSeconds)
	}
	if seconds > maxCPUProfileSeconds {
		return 0, fmt.Errorf("seconds %d exceeds the maximum of %d", seconds, maxCPUProfileSeconds)
	}
	return seconds, nil
}

// writeMeta records the runtime context the profiles were taken in, which is
// needed to interpret them (and to confirm which build is running).
func writeMeta(f io.Writer) error {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	_, err := fmt.Fprintf(f,
		"captured_at: %s\n"+
			"go_version: %s\n"+
			"goos_goarch: %s/%s\n"+
			"num_cpu: %d\n"+
			"gomaxprocs: %d\n"+
			"num_goroutine: %d\n"+
			"heap_alloc_bytes: %d\n"+
			"heap_sys_bytes: %d\n"+
			"heap_objects: %d\n"+
			"total_alloc_bytes: %d\n"+
			"sys_bytes: %d\n"+
			"num_gc: %d\n"+
			"last_gc: %s\n",
		time.Now().UTC().Format(time.RFC3339),
		runtime.Version(),
		runtime.GOOS, runtime.GOARCH,
		runtime.NumCPU(),
		runtime.GOMAXPROCS(0),
		runtime.NumGoroutine(),
		ms.HeapAlloc,
		ms.HeapSys,
		ms.HeapObjects,
		ms.TotalAlloc,
		ms.Sys,
		ms.NumGC,
		time.Unix(0, int64(ms.LastGC)).UTC().Format(time.RFC3339), //nolint:gosec // LastGC fits int64 until year 2262
	)
	return err
}
