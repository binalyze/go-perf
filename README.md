# go-perf

`go-perf` compares Go pprof profiles from two runs of the same workload,
flags regressions, renders an HTML report, and can ask an AI model to
interpret the differences.

It has three parts:

| Path | What it is |
|---|---|
| `profiling/` | Go package to embed in the application under test: a loopback-only control server for capturing profiles, plus a `Client` for harnesses |
| `cmd/go-perf/` | CLI that collects, compares and diffs profiles and writes the HTML report |
| `ai/` | Node.js script that sends the comparison to an AI agent and writes a structured verdict back for the report |

## Install

```bash
go install github.com/binalyze/go-perf/cmd/go-perf@latest
```

`go-perf` shells out to `go tool pprof` for profile diffs, so the Go toolchain
must be on `PATH` where `analyze` runs.

To stamp version information into a release build:

```bash
go build -ldflags "-X main.version=v1.0.0 -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +%FT%TZ)" ./cmd/go-perf
```

## 1. Embed the profiling server

```go
import "github.com/binalyze/go-perf/profiling"

if os.Getenv("MYAPP_ENABLE_PROFILING") == "1" {
	profiling.Start(ctx, profiling.Options{
		Addr:   os.Getenv("MYAPP_PROFILING_ADDR"), // empty means 127.0.0.1:6060
		Logger: slog.Default(),
	})
}
```

The server listens on loopback only by default and exposes nothing until
capture is enabled:

| Endpoint | Effect |
|---|---|
| `GET /start` | Enable capture |
| `GET /stop` | Disable capture |
| `GET /cpu/start` | Start continuous CPU profiling |
| `GET /cpu/stop` | Stop CPU profiling and return `cpu.pprof` |
| `GET /save` | Zip of all runtime profiles (heap, allocs, goroutine, ...) |
| `GET /save?seconds=N` | Same, plus a CPU profile sampled for N seconds (max 300) |

Gate it behind an environment variable or flag: profiles can expose memory
contents.

## 2. Collect profiles from a run

Each run lives in its own directory containing:

- `metadata.json`: run ID, product name, duration, success flag, agent and CPU info (see `RunMetadata` in `cmd/go-perf/run.go`)
- `profiles.zip`: `cpu.pprof`, `heap.pprof`, `allocs.pprof`, `goroutine.pprof`, `goroutine-stacks.txt`

There are three ways to produce it.

**From your own test harness** (recommended when the test starts the
application itself):

```go
client := &profiling.Client{Addr: "127.0.0.1:6060"}
if err := client.Start(ctx); err != nil { ... }
// ... run the workload ...
result, err := client.Collect(ctx, filepath.Join(runDir, "profiles.zip"))
```

**From the CLI against a running process:**

```bash
go-perf collect start --port 6060
# ... run the workload ...
go-perf collect stop --port 6060 --output-dir ./run-current
```

**Let go-perf drive the whole run** with a product config:

```json
{
  "product_name": "my-service",
  "binary_path": "./bin/my-service",
  "binary_args": ["serve", "--listen", ":8080"],
  "env_vars": ["MYAPP_ENABLE_PROFILING=1"],
  "e2e_test_path": "./bin/e2e",
  "e2e_test_args": [],
  "profiling_port": "6060",
  "health_check_url": "http://localhost:8080/healthz",
  "test_case_name": "API e2e suite",
  "startup_wait_time_ms": 60000,
  "test_launches_product": false
}
```

```bash
go-perf run --config my-service.json --work-dir ./perf-runs
```

With `test_launches_product: true` the e2e test is expected to start the
application itself, and `binary_path` is ignored. Both processes receive
`PERF_RUN_DIR` pointing at the current run directory.

## 3. Compare and report

Pass a directory of earlier runs (one subdirectory each, newest 10 by default).
go-perf writes `summary.json` for the current run and compares duration, CPU,
allocations, heap, and goroutines against the history median, not against a
single previous run. A metric is a regression only when it is both above the
threshold and outside the usual spread, so a noisy run does not page anyone.
Heap and goroutine counts are reported but do not gate.

```bash
go-perf analyze \
  --run-dir ./run-current \
  --history-dir ./history \
  --history-size 10 \
  --output-html report.html \
  --threshold 15
```

`analyze` also diffs the current run against the newest history run and writes
`report.html`. Gated metrics are test duration, average CPU cores, and
allocations. `analyze` itself still exits 0, so a regression never fails the
build on its own; `go-perf run` does exit non-zero on regression.
`--baseline-dir` still accepts one previous run and is the old two-run mode.
Heap and goroutine profiles are end-of-run snapshots and are reported as
context only. Pass `--baseline-dir none` for a report without comparison.

Other subcommands:

| Command | Purpose |
|---|---|
| `go-perf compare --run-a A --run-b B` | Compare two runs inside a work directory |
| `go-perf report --run-id ID` | Re-render a report from stored run data |
| `go-perf profile-stats --cpu cpu.pprof` | Print CPU profile statistics as JSON |

## 4. AI analysis (optional)

`analyze` also writes `ai-input.json` (a compact summary under 60 KB) into the
run directory. Feed it to the AI script, then run `analyze` again so the
verdict is embedded in the report:

```bash
cd ai && npm ci
CURSOR_API_KEY=... CURSOR_CLOUD_ENV=owner/repo \
  node analyze.mjs ../run-current/ai-input.json ../run-current/ai-analysis.json
go-perf analyze --run-dir ../run-current --history-dir ./history --output-html report.html
```

The audit reads the hottest stacks in this run, opens the source, and only then classifies a validated cost as a regression, an improvement, or an expected cost. History is extra evidence when older runs exist. An expected cost is reported without a fix prompt.

The script uses the Cursor SDK (`@cursor/sdk`) and needs Node.js 22.13 or
newer. `CURSOR_API_KEY` and `CURSOR_CLOUD_ENV` come from the environment;
neither is stored in the repo. `CURSOR_CLOUD_ENV` is the cloud environment
that can read the source, named like `owner/repo`. Set `CURSOR_MODEL_PERF` or
`CURSOR_MODEL` to choose the model. The prompt lives in `ai/prompts/analysis.md`.

## Development

```bash
go test ./...
cd ai && npm ci && npm test
```
