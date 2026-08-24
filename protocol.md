# Eval Observation Protocol v1

## 1. Task unit

One real task is one user objective that reaches a terminal outcome.

- Retries, resumptions, recovery attempts, and OS-specific reruns remain part
  of the same task.
- A genuinely new objective receives a new random `task_id`.
- Terminal status is `completed`, `failed`, or `abandoned`.
- Synthetic fixtures use `population: "synthetic"` and never count as real-task
  evidence.

## 2. Invocation boundary

The Native Agent, user, or CI may invoke Eval when all of these conditions hold:

1. a terminal task outcome exists;
2. the caller decides post-task measurement is useful; and
3. the caller has Host authority to append to the external private observation
   file.

This is caller-owned module invocation, not Eval self-activation. Eval does not
watch for task completion, run during the task, mutate task artifacts, invoke a
module, retry or repair work, choose workflow order, or apply a product or
release decision.

If the authority or single-writer condition is not satisfied, do not append.
Absence of an observation does not change the task outcome.

A standing user or Host policy that authorizes this external state write
satisfies the authority condition. Eval does not introduce a per-observation
confirmation or a separate approval workflow.

## 3. Observation values

Each JSONL row conforms to `schemas/observation-v1.schema.json` and records:

- schema version, random observation and task IDs, append-only revision data,
  `terminal_on`, and `recorded_on`;
- real or synthetic population, task type, Agent, model, and primary host OS;
- whether Spec, Ward, and Seal were used and their exact public versions;
- common terminal outcome, user intervention, module interaction time, and
  rework facts;
- `task_effects` assessable whether a module was used or not;
- `module_metrics` for the behavior and friction of modules that were used.

`terminal_on` is the date the terminal outcome was established. `recorded_on`
is the append date and must not precede `terminal_on`.

Module use means material use in the task, not installation or availability:

- Spec was used when a versioned Spec artifact shaped planning,
  implementation, or verification.
- Ward was used when that version's protection or decision boundary was active
  for at least part of the task.
- Seal was used when that version evaluated completion Evidence or materially
  governed the terminal completion attempt.

`used: true` requires an exact public version and that module's metric object.
`used: false` requires `version: null` and that module's metric entry to be
`null`. If a used module's exact version cannot be recorded as a public
identifier, do not append the observation; never record that module as unused.

A boolean is true or false only when assessed. A count is zero only when it was
measured and no event occurred. Otherwise the value is `null`. Reports never
combine `null` with false or zero. Counts are limited to `0..1,000,000`.

Late Material Decision `total` is the number of distinct decisions. Its six
category counts are multi-label: do not sum them to derive `total`, and each
known category count must not exceed a known `total`.

General time is one of:

- `null`;
- `{"method":"measured","seconds":N}`; or
- `{"method":"bounded-estimate","lower_seconds":N,"upper_seconds":N}`.

General time values are limited to `0..31,536,000` seconds, and a bounded
estimate must have `lower_seconds <= upper_seconds`.

Ward Hook latency is `null` or
`{"median_ms":N,"sample_count":N}`. Reports keep measured and bounded
estimates separate and do not invent a point estimate from a range.

When both values are known, Seal `added_user_interventions` must not exceed
the outcome's total `user_interventions`.

The JSON Schema enforces row shape, bounded scalar values, and module coupling.
Cross-value and cross-row relationships stated in this protocol are checked by
the designated writer and again during manual report preparation. Eval v0.1
does not include an installed or external-state data validator.

## 4. Raw storage and single writer

Append exactly one compact JSON object per non-empty line to:

- `$XDG_STATE_HOME/jgoneit/eval/v1/observations.jsonl` when
  `XDG_STATE_HOME` is set and absolute; or
- `$HOME/.local/state/jgoneit/eval/v1/observations.jsonl` only when
  `XDG_STATE_HOME` is unset and `HOME` is absolute.

Fail closed for a relative state root. Resolve the path physically and reject a
location inside any Git worktree. On POSIX-like hosts, use directory mode
`0700`, file mode `0600`, and `umask 077`; on other hosts use equivalent
current-user-only access controls.

There is exactly one active writer per observation file. External Host policy
or coordination designates that writer; allowing Native Agent, user, and CI as
invocation owners does not permit simultaneous writers. The writer completes
one append before another begins. Eval v0.1 has no lock service, concurrent
writer protocol, or automatic merge. Raw observations are never stored in this
repository.

## 5. Identifiers and corrections

- Generate `task_id` and `observation_id` independently as canonical lowercase
  random UUIDv4 values. Never hash work content to create an ID.
- The first record for a task has `revision: 1` and `supersedes: null`.
- A correction appends a new observation with the same `task_id`, the next
  contiguous revision, a new `observation_id`, and `supersedes` equal to the
  immediately preceding observation ID.
- Never edit or delete an earlier JSONL row as a correction.
- A valid single-writer chain is physically ordered and linear, with no
  duplicate ID, self-reference, fork, forward link, or multiple initial rows.

Analysis uses only the latest record from each valid task chain and discloses
invalid or superseded rows.

## 6. Analysis

Aggregate latest-valid real observations by task type, module use, and public
module version. Report:

- real tasks included and synthetic rows excluded;
- terminal outcomes, false-positive and false-negative indicators, missed
  defects, intervention, rework, and time;
- task effects for used and unused cohorts;
- used-only module decisions and friction;
- known and unavailable counts for every rate or aggregate;
- repeated metric categories and measurement limitations.

Do not present `null` as false or zero. Do not compare a cohort without its
sample size. Caller-selected observations are not a randomized experiment, so
used-versus-unused differences are observational and do not establish
causality.

## 7. Decision report

Use `templates/decision-report-v1.md`. A report may support a human decision to
retain, promote to stable, keep experimental, modify, remove, stop, or run
another experiment.

Eval never applies that decision, mutates a release, starts implementation,
repairs work, or invokes another module.

## 8. Completion boundary

Repository verification establishes only that the contract scaffold is
internally consistent. The Evaluation MVP additionally requires real Task
observations and one privacy-reviewed cumulative report. A report demonstrates
operation of the evidence workflow, not causal proof or automatic release
authority.
