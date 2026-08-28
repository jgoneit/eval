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
$XDG_STATE_HOME/jgoneit/eval-experiment/v1/journal.jsonl             (all platforms when set)
$HOME/.local/state/jgoneit/eval-experiment/v1/journal.jsonl          (Darwin/Linux fallback)
%USERPROFILE%\.local\state\jgoneit\eval-experiment\v1\journal.jsonl (Windows fallback)
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
managed host policy becomes active. The Host launches every candidate with the
preflighted `ward` permission profile. Launching a candidate with another
profile is a provisioning failure that aborts the experiment run; it is not a
reason to exclude that task afterward. Codex loads global `~/.codex/AGENTS.md`
guidance at session start, so only fresh tasks are eligible after installation.
See the [OpenAI AGENTS.md documentation](https://learn.chatgpt.com/docs/agent-configuration/agents-md).

Before activation, the Host adds only the Eval state directory
`$XDG_STATE_HOME/jgoneit/eval-experiment` (or the platform fallback shown
above) as an explicit writable workspace root for the selected permission
profile, creates its private directories with mode `0700`, ensures any files it
creates use mode `0600` on Darwin/Linux, and verifies writes against a separate
preflight state root. For a managed permission profile, this workspace-root
rule is authoritative and must not be combined with legacy
`sandbox_workspace_write` settings. See the [OpenAI Permissions documentation](https://learn.chatgpt.com/docs/permissions).

A preflight always passes a disposable absolute child path beneath that granted
Eval state directory through `--state-root` and confirms that its journal path
differs from the production journal before it runs. It must not create or
modify the production journal. If state provisioning or lifecycle calibration
fails, the Host marks that experiment run aborted and restarts with a new
activation time; it does not silently reuse the failed run or migrate its
state. If an aborted run already contains rows, the Host quarantines that exact
journal subtree before starting the replacement run rather than appending new
samples to it.

The end of an assistant turn is not by itself a terminal task outcome. The
same primary objective remains nonterminal only while an identified, in-scope
continuation can resume it after user input, approval, configuration, or
permission becomes available. A recoverable Ward or Seal stop, clarification
request, or resumable handoff is therefore nonterminal and must not be mapped
to `failed` or `abandoned`.

Eligible terminal outcomes have these narrower meanings:

- `completed`: the requested objective is achieved and no required work remains;
- `failed`: the objective cannot be completed within the current scope and
  granted authority, no identified in-scope continuation remains pending, and
  the task is being closed;
- `abandoned`: the Host explicitly supplies the outcome; the Agent never infers
  abandonment from user silence.

Without an explicit Host terminal callback, an abandoned task contributes only
to the Host denominator and missing-attempt count; it cannot produce an Agent
observation.

Eval development, pure Q&A, and subagent child tasks are excluded in advance.
Ward or Seal usage and the quality of the result never exclude a task afterward.

For an eligible task, the primary Agent makes one silent `evalctl observe`
attempt only after the eventual terminal outcome is fixed. It makes no attempt
during a nonterminal turn; if the same task resumes, it preserves eligibility
for the single terminal attempt. The attempt pipes one complete JSON object to
standard input in the same shell command; bare `evalctl observe` is invalid and
must never be called. It does not:

- run Ward or Seal to discover a metric or version;
- ask a question or request approval;
- retry, emit progress, or add an Eval message;
- mutate the task result or turn an observation failure into a task failure.

The Host task history supplies the population denominator and missing-attempt
count. The journal intentionally does not store skipped rows or task identity.
The managed host block is removed after the twentieth eligible task. If the
same objective reopens after a terminal attempt, the Agent makes no second
attempt. The Host treats that reopen as a lifecycle-calibration failure, aborts
the experiment run, and restarts with a new activation time rather than
continuing with the stale row. This minimal experiment does not correct or
supersede recorded rows.

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
