# Go performance audit

You are auditing the performance of a Go application from a series of pprof runs, not from a single before/after pair. The JSON below is the whole report: history statistics, hotspot functions, and the diff against the most recent previous run.

**This is a read-only audit.**
- Do not modify files, commit, push, or open a pull request.
- Read the source only to explain a hotspot and to write a fix prompt.
- The commit under test is `metadata.commit_sha`. Read that revision when it is set. Do not inspect later commits.

## How to read the numbers

- `history.metrics` compares the current run with up to 10 earlier runs.
- `label` is one of `stable`, `noisy`, `regressed`, `improved`, `creeping`.
- `regressed` means the current value is above the history median by more than the threshold **and** its robust z-score is above 3. A single spike in a noisy series is not a regression.
- `creeping` is a steady rise that has not yet crossed that bar. Call it out; it is how slow leaks show up.
- `noisy` metrics (common for network-heavy work, and for heap and goroutine snapshots) are context. Do not treat them as regressions.
- `gates: false` metrics never decide the verdict on their own.
- `env_note`, when present, means the machine or agent differs from most of the history. Say so, and do not over-claim small differences.
- Hotspots:
  - `persistent_hotspots`: in the top 10 of a profile in at least 80% of runs. These are the optimization targets.
  - `new_hotspots`: in this run's top 10 and absent from the history.
  - `growing_hotspots`: their share of the profile keeps rising.

## Performance data

```json
{{AI_INPUT_JSON}}
```

## Output

Respond with ONLY a JSON object (no markdown fences, no extra text) using this schema:

```json
{
  "verdict": "ok|warning|regression",
  "headline": "One sentence on what changed across the history, not just versus the last run",
  "confidence": "high|medium|low",
  "metric_notes": [
    { "metric": "duration_ms", "note": "What this metric's label means here" }
  ],
  "findings": [
    {
      "id": "F1",
      "severity": "high|medium|low",
      "title": "Short name of the problem",
      "evidence": "The metric names and numbers that support this",
      "location": { "file": "path/from/repo/root.go", "line": 1, "function": "full.function.Name" },
      "explanation": "Why the code behaves this way, from the source",
      "fix_prompt": "A self-contained prompt a developer can paste into an editor agent. Name the file, the function, the evidence, and the change to make. Do not assume the reader saw this report."
    }
  ],
  "recommendations": ["One action each, only if it is not already a finding"]
}
```

## Verdict

- **ok**: no gated metric is `regressed`, and nothing in the hotspots is a clear code problem.
- **warning**: a gated metric is `creeping`, or a persistent hotspot is expensive but the series is still inside the noise band.
- **regression**: a gated metric is `regressed`, or a new hotspot dominates a gated profile (CPU or allocations).

## Findings

- Only include a finding when the source shows a concrete improvement. Do not file a finding for noise.
- `location.file` and `location.line` must be a real place you opened. If you cannot find it, leave the finding out.
- `fix_prompt` must stand alone: repository-relative path, function name, the numbers, and the change. Keep it under 1500 characters so it can be opened as an editor link.
- Severity `high` is a gated regression with a known location. `medium` is creep or a persistent hotspot. `low` is optional cleanup.
- Confidence is `low` when fewer than 3 history runs exist, the environment changed, or the cause is ambiguous.

Begin the audit and output the JSON.
