# Eval Privacy Contract

Eval collects only bounded structured facts needed to measure module value and
cost. Observation v2 and every registered extension are closed contracts with
no free-text field.

## Prohibited observation data

Never store any of the following in an observation or extension:

- repository, organization, customer, product, project, or internal system
  names;
- actual file or directory paths;
- task descriptions, business content, source code, patches, or artifacts;
- prompts, transcripts, Chain-of-Thought, or raw Agent output;
- commands, command arguments, URLs, hostnames, or infrastructure identifiers;
- credentials, tokens, keys, secrets, personal data, or raw or per-event user
  behavior traces; or
- free-text notes, explanations, rationales, copied errors, or task narratives.

Unknown properties and unregistered extension payloads are rejected.
`task_id` and each `observation_id` are independently generated random UUIDv4
values and must never be derived from work content. `supersedes` identifies
only the immediately preceding random observation ID. Repository identity is
not recorded, even as a hash.

Module versions and model identifiers contain only bounded public identifiers.
The schema constrains their syntax; it does not prove that an identifier is
public. Use `model: null` whenever public availability is unknown or the value
is sensitive.

A used module whose exact public version is known uses version state
`known-public`. When the exact public version cannot be learned from existing
task context or cannot be recorded safely, keep `used: true` and use
`unavailable` with `value: null`. Do not discard the observation, probe the
module, or misstate the module as unused. Such a record remains in the usage
cohort and is excluded only from version comparison.

Module absence means usage is unassessed. `used: false` is recorded only when
non-use is known and requires `not-applicable`, `value: null`, and
`metrics: null`. Unknown metrics use `metrics: null`; an unknown task-effect
key is absent. Unknown values inside an assessed extension remain `null`; they
are never guessed as false or zero.

## External private storage

New raw observations stay outside every source checkout at:

- `$XDG_STATE_HOME/jgoneit/eval/v2/observations.jsonl` when
  `XDG_STATE_HOME` is set and absolute; or
- `$HOME/.local/state/jgoneit/eval/v2/observations.jsonl` only when
  `XDG_STATE_HOME` is unset and `HOME` is absolute.

The corresponding v1 file remains a read-only compatibility source. Eval does
not append v1 rows or move raw rows between versions. `--state-root ABS`
selects another absolute base with the same `jgoneit/eval/v1` and
`jgoneit/eval/v2` separation.

Relative roots, filesystem roots, paths inside a Git worktree,
caller-controlled symlinks, and non-regular data or lock files are rejected.
On Darwin, an immutable root-owned top-level alias such as `/tmp` may be
resolved to its physical root-owned sticky directory before the private child
checks run. On POSIX-like hosts, an existing XDG or HOME state root must be
owned by the current user and must not be group- or other-writable; mode
`0755` is therefore permitted for that root.
Eval-managed descendants are owned by the current user with mode `0700`, and
the data, lock, and temporary files are owned by the current user with mode
`0600`. Other hosts require equivalent current-user-only access for
Eval-managed state through Host ACLs. In `0.2.0-dev.0`, Windows Store access
fails closed with a permission error because owner and DACL verification is
not yet implemented; the Windows build does not infer privacy from opaque
`FileMode` metadata.

The caller must already hold Host write authority. The Native Agent Skill does
not request additional permission, retry, or change the task result when that
authority is absent. `observe --best-effort` returns only a bounded, path-free
skip reason; the implicit Skill suppresses that output. A successful implicit
observation reports no identifier or raw value.

## Writer boundary

Core inspects private path metadata, takes a kernel-backed exclusive lock, and
rechecks caller-controlled paths while holding the lock. It validates the
complete existing v2 log and prospective row before replacement. It then
writes the existing bytes plus one canonical row to a same-directory temporary
file, syncs the file, atomically replaces the live file, and syncs the
directory.

This is a crash-atomic logical append: after a failure, the live path contains
either the previous complete log or the complete prospective log, never a
partial row. The lock serializes writers; it is not a daemon, network lock,
merge protocol, or shared Toolkit runtime.

Eval does not encrypt, transmit, upload, synchronize, back up, retain, redact,
delete, or merge the observation store automatically. The user owns storage,
backup, retention, and deletion policy. Filesystem privacy is not a substitute
for full-disk encryption or correct Host account security.

## Analysis and reports

Validation may identify row and chain failures but must not print raw rows in a
public response. Summaries and comparisons contain aggregate facts, exclusion
counts, and bounded cohort labels; they contain no task IDs, observation IDs,
task narratives, or source paths.

Private reports remain outside source checkouts. A report copied into the
repository or otherwise published must:

1. disclose real and excluded synthetic counts, superseded rows, invalid rows
   or chains, missing values, and cohort sizes;
2. omit raw observations, individual rows, private identifiers, and source
   paths;
3. suppress every public cohort cell with `n < 5` using `<5`;
4. suppress complementary cells when another displayed value could reveal a
   hidden count;
5. use only aggregate, non-sensitive language;
6. label sampling, missing-data, measurement, and causal limitations; and
7. confirm that it records a human decision and triggers no workflow or release
   action.

Publication always requires an explicit manual privacy review. Schema,
semantic, repository, Plugin, Skill, or E2E verification does not constitute
that review. Retention, promotion, removal, and release decisions remain human
actions.
