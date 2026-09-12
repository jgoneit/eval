# Local collection and human review

The workflow has five commands. All accept an optional `--state-root ABS`, with
the same state-root resolution as `observe`. Exit 0 means successful execution;
`collect` exits 2 for a persisted but incomplete scan or unconfirmed durability.
Other new-command failures exit 1, CLI usage errors exit 64. Failures are visible
structured JSON codes; raw source errors are never printed. `observe` retains
its original success/skip contract.

## Register once

Create a private configuration outside a Git checkout:

```json
{
  "schema_version": 1,
  "repositories": ["/absolute/repos/project", "/absolute/repos/eval"],
  "session_dirs": ["/absolute/codex/sessions", "/absolute/codex/archived_sessions"],
  "ward_dirs": ["/absolute/state/ward/diagnostics"],
  "seal_binary": "/absolute/bin/seal",
  "self_repository": "/absolute/repos/eval",
  "export_timeout_seconds": 30
}
```

Use exact confirmed local Git checkouts. Paths must be absolute, clean and
non-root; duplicate entries and unknown JSON fields are rejected. Choose a Seal
binary supporting `seal run export --format json`. Sources may be inaccessible
at registration; collection then reports them as incomplete, rather than
claiming that an empty result means success. A missing `.seal` in an accessible
repository is an unused source, not an installation failure.

```sh
evalctl experiment init --config /absolute/private/config.json
evalctl collect --experiment EXPERIMENT_UUID
evalctl collect --experiment EXPERIMENT_UUID
```

The start time is the actual initialization time and cannot be backdated in
configuration. Validate both collection receipts and actual persisted state.
Repeat collection must add no new facts for unchanged sources. Register a new
experiment to change the population or input configuration. Never merge the old
20-task records into it or relabel earlier runs as new samples.

## What is collected

Codex session envelopes are scanned only until creation metadata and the first
turn context have been projected. Conversation, reasoning, command bodies and
later turns are not stored. Root/child metadata, creation time, repository alias,
public model and first profile are retained. `managed` does not prove a named
`ward` profile. Missing first metadata is unknown; later availability appends a
metadata revision under the same task ID. Final responses are never completion
callbacks. Child and Eval-development exclusions remain in the roster. Pure Q&A,
other eligibility and terminal outcomes require batch review.

Ward uses `ward-diagnostic-record/v1`: collector generation and sequence identify
the invocation. Only public version, bounded decision/stage/error codes and
finite timing are retained. Session IDs are private correlation hints, not
authoritative task membership. A `deny` is Ward's blocking verdict; `defer`
does not prove final tool execution. File disappearance, sequence gaps,
retention exposure and malformed data appear in receipts.

Seal is invoked directly with an argument array, in the exact repository, with
bounded output and timeout. Eval never opens internal Evidence. The export
projects validated IDs, digest, mechanical results, scope/source stability,
check times and historical Completion presence. A later Completion appends an
event revision even when Evidence digest stays unchanged. It does not turn a
historical Run into a new postactivation invocation or current Acceptance.
Canonical Evidence has no run-producing Seal version, so `run_version: null`
becomes an unknown execution-version cohort. The reader
`exporter_version` is never substituted as the producing version. Version-qualified
Seal comparisons remain unavailable until independent provenance is supplied by
a compatible source; upgrading only the reader does not add an execution event.

Inputs are bounded to 4,096 source files, 128 MiB read per scan, 4 MiB per line,
and 16 MiB Seal output per repository. Hitting a limit is an explicit incomplete
scan. Inputs are read through rooted capabilities; symlink entries are rejected.
Exact configured or confirmed nested checkouts are distinct repository aliases.
Nearby timestamps or directories never establish task links.

## Storage and recovery

Each experiment has two private append-only JSONL files beneath
`jgoneit/eval-experiment/v2/EXPERIMENT_UUID/`:

- `private.jsonl`: fixed registration and random-ID mappings to original local
  task, repository, collector and Run identifiers and evidence references.
- `journal.jsonl`: versioned task facts, event snapshots, scan receipts and
  human review amendments. It contains no original paths or task IDs.

The combined limit is 64 MiB. An over-limit transaction stops before publication;
there is no retention deletion or silent loss. The existing exclusive kernel
lock, owned private files, same-directory atomic replacement and sync boundary
are reused. The journal lock serializes collection and review. Private mappings
publish before facts: an interrupted collection may leave an unused mapping,
which the next collection reuses. It cannot acknowledge unstored facts. Source
progress is derived from committed facts; no cursor is advanced on failure.
Temporary atomic-replacement bytes are additional transient disk use.

Receipts separate publication from confirmed directory durability. Persisted
receipts conservatively retain `durability: unconfirmed`; the command result
can confirm durability only after the final directory sync. Read/report operations
create no locks, directories or state and can report a concurrent identity race.

## Review in batches

```sh
evalctl review export --experiment EXPERIMENT_UUID --out /absolute/private/review-01
evalctl review apply --experiment EXPERIMENT_UUID --file /absolute/private/review-01/review.json
evalctl report --experiment EXPERIMENT_UUID --format json
evalctl report --experiment EXPERIMENT_UUID --format markdown
```

Exports are private (0700 directories and 0600 files on Unix), outside Git
worktrees, and require an absent destination directory. Both files are completed
and synced in sibling staging, then the directory is published atomically without
replacement. Failed pre-publication exports leave no partial visible bundle. `evidence.md` is the only
rendered output containing private source links; `review.json` is the editable
judgement packet. Keep both private. Aggregate reports contain no mappings,
original task/run identifiers, paths, command text or logs.

Confirm explicit task membership, merge repeated calls concerning the same
problem into one active incident, and deactivate superseded incidents in the
same amendment. A source cannot belong to two active incidents. Do not classify
each retry as a new effect or a new independent task. Task and incident revisions
must be consecutive; corrections and reopened objectives append history.

Excluded tasks require a population reason: `child`, `eval_self`, `pure_qa`, or
`out_of_scope`. Module non-use, poor outcomes, and failed observation are not
exclusion reasons. Source exclusions remain visible without a human amendment.

Use `confirmed`, `denied`, `unknown`, or `not_applicable` independently for
correctness, additional value, unnecessary intervention and rework. Set an
evidence category only after human inspection (`human_review`, `reproduction`,
`independent_test`). `reviewer: human` is an attestation, not cryptographic proof
of identity. An Agent must not apply its own performance claims as human review.
Changed source snapshots invalidate old incident judgements until reviewed again.

Comparison entries explicitly identify two distinct task aliases and paired
success/time observations under comparable conditions. They require compatible
public model, profile, task type and tool-version cohorts. One task cannot inflate
multiple comparable pairs for the same module. Comparisons remain observational:
selection bias and baseline quality require human judgement.

## Quantitative interpretation

Reports order collection status, observed facts, independent review, paired
comparisons and limitations. Every rate exposes numerator, denominator, unknowns,
not-applicable cases, unit and evidence level. Zero denominator gives `null`,
never 0%. Tool effects use unique tasks; verdict accuracy uses adjudicated
problem incidents. Ward false-positive rate requires independently identified
normal requests, including nonblocking Ward verdicts. Seal completion accuracy
requires reviewed completion-attempt evidence; mechanical pass/fail or historical
Completion presence alone does not supply that denominator.

Task coverage is limited by the available metadata and human terminal
attestations; it does not claim authoritative Host lifecycle coverage. Public
version, model, first profile and task type form separate cohorts. Observed
duration is cost observation, never saved time. Paired differences are tool minus
baseline, with matched endpoints only. There is no overall 100-point score or
automatic keep/remove verdict.

## Hourly Host activation

After real-source preflight, storage confirmation and repeat-collection
idempotency, configure a Codex heartbeat to run the exact tested binary and
experiment ID every hour. Keep successful unchanged scans quiet. Notify only on
collection stoppage, storage quota, a new persistent source failure or required
human action. The scheduler does not approve reviews, reconfigure inputs, retry
`observe`, repair tool installations or modify source evidence.

Code and CLI tests can prove import/report mechanics; they cannot substitute for
a human-reviewed real case or an actual scheduled run. Activation evidence must
state these gates separately. Rollback disables the heartbeat and uses the
previous executable; retain this experiment's private directory for analysis.
The legacy recorder and old journals are unaffected.

Receipt issue arrays contain distinct source/code pairs, not a count of every
malformed input row. Failed precommit collection attempts are not journal receipts;
reporting never uses receipt count as the total attempt denominator.
