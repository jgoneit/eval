# Eval

Eval records bounded post-task observations and produces deterministic evidence
about whether Agent Toolkit modules create more value than cost on real tasks.

Eval `0.2.0-dev.0` is a development product with two thin, repository-owned
surfaces:

- `evalctl`, an independent Go CLI that owns observation writes, validation,
  summaries, and comparisons; and
- the `eval` Plugin Skill, which lets a Native Agent select `evalctl` after a
  task reaches a terminal outcome.

Harness does not execute or own Eval. Eval is not a central runtime, task
listener, telemetry service, or release gate. This repository still contains
no real observations and no cumulative decision report.

## Public CLI

```text
evalctl --version
evalctl observe --input - [--best-effort] [--state-root ABS]
evalctl validate [--file PATH | --state-root ABS]
evalctl summarize --as-of DATE [--from DATE] [--through DATE] [--format json|markdown]
evalctl compare --module ID --by usage|version [--left VERSION --right VERSION] --as-of DATE
```

Exit codes are stable: `0` for success, `1` for invalid data, `2` for a
storage, permission, or lock failure, and `64` for invalid command usage.
`observe --best-effort` converts any record failure after argument parsing into
a path-free `{"status":"skipped","reason":"..."}` response with exit `0`.
Invalid command syntax remains exit `64`.

`observe` accepts a draft on standard input. Core, not the caller, generates
the v2 schema version, random UUIDv4 identifiers, revision, predecessor link,
and recording date.

## Position and selection

```text
Native Agent / User / CI
        |
        | uses any modules needed by the primary task
        v
 completed / failed / abandoned
        |
        | optionally selects Eval once
        v
 authorized best-effort observation
        |
        v
 deterministic aggregate and human decision
```

Terminal selection is caller-owned, including selection by a Native Agent
under an existing Host policy. Eval does not detect completion, schedule
itself, modify the completed task, invoke another module, choose workflow
order, retry or repair work, or apply a product or release decision.

The Skill performs at most one best-effort observation after the primary task
outcome and artifacts are fixed. It does not ask questions or execute another
module to fill missing metrics or versions. A successful implicit write adds
only `Eval: observation recorded.` to the final response. A missing CLI,
unsupported version, denied authority, or record failure is silent and cannot
change the primary task result.

The provider-neutral discovery contract is
[`toolkit-module.json`](toolkit-module.json). A registry may link to that
contract but must not execute Eval or become its runtime.

## Observation versions

Observation v1 remains a read-only compatibility input. New observations are
v2 only and use a module-neutral map:

- an absent module is unassessed;
- only explicit `used: false` enters the unused cohort;
- `used: true` permits `known-public` with an exact public version or
  `unavailable` with `null`;
- a used observation with an unavailable version remains in usage analysis and
  is excluded only from version comparisons; and
- `not-applicable` with `null` is the only valid version state for a known
  unused module.

Module metrics and task effects use closed, versioned extension envelopes.
The current allowlist includes Spec, Ward, and Seal metric v1 extensions and
completion, security, and requirements effect v1 extensions. Unknown modules
can be represented without metrics; unregistered extension payloads are
rejected. Unknown facts remain absent or `null`, never guessed as false or
zero.

Read [CHARTER.md](CHARTER.md), [protocol.md](protocol.md), and
[PRIVACY.md](PRIVACY.md) before recording or publishing Eval data.

## Private state and atomic writer

New observations are written outside source checkouts to:

- `$XDG_STATE_HOME/jgoneit/eval/v2/observations.jsonl` when
  `XDG_STATE_HOME` is absolute; or
- `$HOME/.local/state/jgoneit/eval/v2/observations.jsonl` when
  `XDG_STATE_HOME` is unset and `HOME` is absolute.

The legacy v1 path at the corresponding `v1/observations.jsonl` location is
read but never written. An explicit `--state-root ABS` changes only the state
root; v1 and v2 remain separate beneath it.

Eval fails closed for relative or source-worktree state, caller-controlled
symlinks, unsafe file types, wrong ownership, or unsafe permissions. On
Darwin, an immutable root-owned top-level alias such as `/tmp` is first
resolved to its physical sticky directory; every descendant remains subject
to ownership and permission checks. On POSIX-like hosts, an existing XDG or
HOME state root may be owner-controlled and non-group/other-writable (for
example, mode `0755`). Eval-managed descendants are owner-only mode `0700`,
and their data, lock, and temporary files are mode `0600`. The writer
takes a kernel-backed exclusive lock, revalidates the complete existing log
and the prospective append, writes and syncs a same-directory temporary file,
atomically replaces the live file, and syncs the directory. A failed write
leaves either the previous log or the complete prospective log, never a
partial JSON row.

Darwin and Linux currently support the private Store at runtime. The Windows
lock, replace, and directory-sync implementation is cross-built, but
`0.2.0-dev.0` fails closed before Store access because owner and DACL
verification is not yet implemented. A Windows Store operation therefore
returns the stable permission-error boundary instead of assuming that Go
`FileMode` metadata proves current-user-only access.

Raw observations and private reports stay outside source repositories under
current-user-only access. Eval does not transmit, synchronize, retain, delete,
or publish them automatically.

## Validation and deterministic analysis

Schema and semantic validation cover bounded values, date ordering,
count/total relationships, intervention bounds, unique identifiers, and linear
revision chains. A chain has one root, contiguous revisions, immediate
predecessor links, monotonically nondecreasing recording dates, and no
duplicate, self, fork, or forward references. v1 and v2 rows cannot form one
correction chain. `observe` refuses to append to an invalid existing log.

`summarize` and `compare` read compatible v1 and v2 data, normalize it to one
internal model, use the latest valid real revision per task, exclude invalid
rows or chains, and disclose invalid-row and invalid-chain diagnostics plus
the actual `invalid_or_chain_rows` excluded count. Latest synthetic,
superseded, outside-window, and included rows are counted without overlap.
Canonical JSON fixes key, cohort, module, and version ordering. Boolean aggregates retain exact
numerators and denominators; counts retain known and unavailable sample sizes
and rational means; measured time remains separate from bounded estimates.
Markdown rounds displayed ratios to two decimal places only.

The output is observational. Eval does not generate causal conclusions,
product decisions, release recommendations, or workflow actions.

## Development verification

Repository checks are available through:

```sh
scripts/verify.sh
```

The development boundary also includes Go tests and race tests, vetting,
formatting, cross-platform builds, schema and fixture checks, privacy canaries,
and Plugin/Skill validation. These checks verify implementation contracts; they
do not create real evidence or substitute for privacy review.

## Reports and decisions

A sanitized aggregate may be published only after manual privacy review.
Public cohorts with `n < 5` and inferable complementary cells are suppressed.
Retention, modification, promotion, removal, release, and further experiments
remain human decisions.

The product MVP and the Evaluation evidence milestone are distinct. A working
CLI and Skill do not prove module value. Real-task observations and a
privacy-reviewed cumulative report would demonstrate that the evidence
workflow operated, not that a module caused an outcome.
