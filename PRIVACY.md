# Eval Privacy Contract

Eval collects only bounded structured facts needed to measure module value and
cost. The observation contract has no free-text field.

## Prohibited observation data

Never store any of the following in an observation:

- repository, organization, customer, product, project, or internal system
  names;
- actual file or directory paths;
- task descriptions, business content, source code, patches, or artifacts;
- prompts, transcripts, Chain-of-Thought, or raw Agent output;
- commands, command arguments, URLs, hostnames, or infrastructure identifiers;
- credentials, tokens, keys, secrets, personal data, or raw/per-event user
  behavior traces;
- free-text notes, explanations, rationales, copied errors, or task narratives.

Unknown properties are rejected. `task_id` and each `observation_id` are
independently generated random UUIDv4 values and must never be derived from
work content. `supersedes` is either `null` for revision 1 or the immediately
preceding `observation_id`; it is never generated from work content. Repository
identity is not recorded, even as a hash.

Module versions and model identifiers contain only bounded public identifiers.
The schema constrains their syntax; it does not prove that an identifier is
public. A model identifier is `null` unless public availability is known, and
whenever the value is unavailable or sensitive. A used module requires its
exact public version; if that version cannot be recorded safely, do not append
the observation and do not misstate the module as unused.

## External private storage

Raw observations stay outside every source checkout at the path defined in
[protocol.md](protocol.md). A set `XDG_STATE_HOME` must be absolute. Only an
unset `XDG_STATE_HOME` falls back to `$HOME/.local/state`, and `HOME` must be
absolute. Resolve the path physically and reject any location inside a Git
worktree.

On POSIX-like hosts, the state directory has mode `0700` and
`observations.jsonl` has mode `0600`. Other hosts use equivalent
current-user-only access controls. The file must be a regular, non-symlink file
owned by the current user where the Host exposes those concepts.

The caller must hold Host write authority and be designated as the sole active
writer for that file by external Host policy or coordination. Multiple allowed
invocation owners do not imply simultaneous writers. If sole-writer status
cannot be established, do not append. Eval v0.1 does not lock, encrypt,
transmit, retain, delete, synchronize, or merge the file automatically. The
user owns backup and retention policy.

## Reports

Private reports remain outside source checkouts. They may contain aggregate
counts but never raw rows, task IDs, observation IDs, task narratives, or other
prohibited content.

A report copied into the repository or otherwise published must:

1. disclose real and excluded synthetic counts, superseded rows, missing values,
   and cohort sizes;
2. omit raw observations, individual rows, and private identifiers;
3. suppress every public cohort cell with `n < 5` using `<5`;
4. suppress complementary cells when another displayed value could reveal a
   hidden count;
5. use only aggregate, non-sensitive language;
6. label sampling, missing-data, measurement, and causal limitations;
7. confirm that it records a human decision and triggers no workflow or release
   action.

Publication requires an explicit manual privacy review. Schema or repository
verification is necessary but does not constitute that review.
