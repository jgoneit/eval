# Eval 20-Task Experiment

Eval is a bounded recorder experiment, not a Toolkit product. It records a
small set of facts after eligible Codex tasks terminate so a person can decide
whether a larger evaluation tool is justified.

Version: `evalctl 0.2.0-experiment.1`

## Public surface

```text
evalctl --version
evalctl observe [--state-root ABS]
```

`observe` reads exactly one JSON object from standard input. Only malformed CLI
arguments return exit 64. Input, permission, lock, or storage failures are
best-effort skips with exit 0.

```json
{"status":"skipped","reason":"invalid-observation"}
```

A successful atomic replacement returns its immutable slot. If the replacement
completed but final directory durability could not be confirmed, the row is
still recorded and reported honestly.

```json
{"status":"recorded","slot":1,"durability":"confirmed"}
{"status":"recorded","slot":1,"durability":"unconfirmed"}
```

## Observation contract

```json
{
  "outcome": "completed",
  "rework_required": false,
  "ward": {
    "used": true,
    "version": null,
    "defects_caught_before_terminal": 1,
    "added_user_interventions": 0,
    "interaction_seconds": 2,
    "normal_work_blocked": false
  },
  "seal": {
    "used": false,
    "version": null
  }
}
```

`outcome`, `ward`, `seal`, and each module's `used` and `version` are required.
Effects and `rework_required` are optional and should be supplied only when the
host already knows them. An unused module requires `version: null` and forbids
effects. A used module may have `version: null`; that row belongs to the usage
cohort but cannot support an exact-version comparison.

The recorder rejects unknown or duplicate keys, non-integral count or duration
representations, negative values, free text, and input-supplied identity or
dates. It generates only `schema_version`, `slot`, and `recorded_at`.

## Private journal

The default journal is:

```text
$XDG_STATE_HOME/jgoneit/eval-experiment/v1/journal.jsonl
$HOME/.local/state/jgoneit/eval-experiment/v1/journal.jsonl
```

The journal contains successful observations only and stops after 20 contiguous
slots. Existing rows are immutable. Before each append, the writer validates
the complete journal while holding a kernel-backed exclusive lock. It uses a
private same-directory temporary file, file sync, atomic replacement, and a
directory durability attempt. State traversal is anchored with Go `os.Root`;
unsafe ownership, permissions, symlinks, reparse points, and regular-file hard
links fail closed on Darwin, Linux, and Windows.

Legacy Eval v1/v2 state is neither read nor migrated.

## Host experiment policy

The experiment population is the next 20 eligible root Codex tasks after the
managed host policy becomes active. Codex loads global `~/.codex/AGENTS.md`
guidance at session start, so only fresh tasks are eligible after installation.
See the [OpenAI AGENTS.md documentation](https://learn.chatgpt.com/docs/agent-configuration/agents-md).

Eligible terminal outcomes are `completed`, `failed`, and `abandoned`. Eval
development, pure Q&A, and subagent child tasks are excluded in advance. Ward
or Seal usage and the quality of the result never exclude a task afterward.

For an eligible task, the primary Agent makes one silent `evalctl observe`
attempt after the task outcome is fixed. It does not:

- run Ward or Seal to discover a metric or version;
- ask a question or request approval;
- retry, emit progress, or add an Eval message;
- mutate the task result or turn an observation failure into a task failure.

The Host task history supplies the population denominator and missing-attempt
count. The journal intentionally does not store skipped rows or task identity.
The managed host block is removed after the twentieth eligible task.

## Decision boundary

After 20 eligible tasks, a person performs the privacy review and manual report.
Eval does not infer causality, recommend a release, or retain, promote, or remove
another module.

- Fewer than 10 successful rows: do not promote; reduce input or stop Eval.
- Provisioning failures: report separately from observation burden.
- Ward or Seal used/unused cohort below 5: do not compare that module.
- Reintroduce `validate`, `summarize`, `compare`, Plugin packaging, or Harness
  registration only after the corresponding repeated need is observed.

## Development verification

```sh
gofmt -w cmd internal
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/evalctl
```

GitHub Actions runs formatting, vet, tests, and builds on macOS, Linux, and
Windows, plus the race detector on Linux.
