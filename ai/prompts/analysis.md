# Go performance audit

You are auditing one Go program from its latest pprof run. History, when it exists, tells you whether a cost is new. The source tells you whether the cost is real and what kind of cost it is.

**This is a read-only audit.**
- Do not modify files, commit, push, or open a pull request.
- Read the source before you classify a hotspot. A profile name is not enough.
- The commit under test is `metadata.commit_sha`. Read that revision when it is set. Do not inspect later commits.

## Procedure

Do this in order for each candidate. Stop that candidate when a step fails.

1. Start from this run. Take the hottest functions in the CPU, allocation, and heap profiles (`profile_insights` and, when present, the history hotspots). A function that appears only as a tiny delta against the previous run is not a candidate.
2. Open the source for that function at the commit under test. Confirm the stack is this program doing that work.
3. Validate. A hotspot is valid when the source shows why that stack is on the profile. Drop it when you cannot open the code, the stack is a runtime or measurement artifact, or the numbers are a snapshot swing the report already marks as noise (`gates: false`, heap, goroutines) and the source does not explain a real cost.
4. Classify only a validated hotspot:
   - `regression`: older runs exist, a gated metric is `regressed` or the function's share has clearly grown, and the source explains the extra cost. Fewer than 3 history runs cannot support this class.
   - `improvement`: the source shows avoidable waste, even when the series is stable. The waste must be a substantial share of that profile: several percent, or one of the dominant stacks. A delta under about one percent of a large profile is not an improvement.
   - `expected`: the cost is valid and large, and the source shows the program doing the work it exists to do. Report it so the dominant cost stays visible. Do not attach a fix.

History is extra evidence for the class. It is not required to report an `improvement` or an `expected` cost.

## How to read the numbers

- `history.metrics` compares the current run with up to 10 earlier runs. With no earlier runs, classify from this run's profiles and the source only.
- `label` is one of `stable`, `noisy`, `regressed`, `improved`, `creeping`.
- `regressed` means the current value is above the history median by more than the threshold and its robust z-score is above 3. A single spike in a noisy series is not a regression.
- `creeping` is a steady rise that has not yet crossed that bar. Use it as evidence, not as a class by itself.
- `noisy` metrics, and heap and goroutine snapshots, are context. Do not treat them as regressions.
- `gates: false` metrics never decide a `regression` on their own.
- `env_note`, when present, means the machine or agent differs from most of the history. Say so, and do not over-claim small differences.
- Hotspots:
  - `persistent_hotspots`: in the top 10 of a profile in at least 80% of runs.
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
  "headline": "One sentence on this run's dominant costs, plus the comparison when older runs exist",
  "confidence": "high|medium|low",
  "metric_notes": [
    { "metric": "duration_ms", "note": "What this metric's label means here" }
  ],
  "findings": [
    {
      "id": "F1",
      "class": "regression|improvement|expected",
      "severity": "high|medium|low",
      "title": "Short name of the validated cost",
      "evidence": "The profile names and numbers that support this",
      "location": { "file": "path/from/repo/root.go", "line": 1, "function": "full.function.Name" },
      "explanation": "Why the source makes this class true",
      "fix_prompt": "Only for regression and improvement. A self-contained prompt a developer can paste into an editor agent. Name the file, the function, the evidence, and the change. Do not assume the reader saw this report."
    }
  ],
  "recommendations": ["One action each, only if it is not already a finding"]
}
```

## Verdict

- **regression**: at least one finding is class `regression`.
- **warning**: there is no regression, and at least one finding is an `improvement` or an `expected` cost that dominates a profile.
- **ok**: no validated finding is worth reporting.

## Findings

- `location.file` and `location.line` must be a place you opened. If you cannot find it, leave the finding out.
- `explanation` must say what you saw in the source and why that makes the class true.
- `fix_prompt` is required for `regression` and `improvement`, and must be empty for `expected`. Keep a fix prompt under 1500 characters so it can be opened as an editor link.
- Severity `high` is only for `regression`. `medium` is an `improvement`. `low` is `expected`.
- Confidence is `low` when fewer than 3 history runs exist, the environment changed, or the cause is ambiguous. Still report validated `improvement` and `expected` findings.

Begin the audit and output the JSON.
