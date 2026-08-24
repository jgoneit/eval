# Eval

Eval measures whether each Agent Toolkit module creates more value than cost on
real tasks.

Eval is a post-task artifact protocol in the Evaluation plane. It is not a
Plugin, Skill, Hook, runtime, telemetry service, or workflow controller.

## Status

Eval v0.1 currently provides a charter, observation protocol, JSON Schema,
synthetic fixtures, report template, provider-neutral module manifest, and
development verification. This is a contract scaffold, not a completed
Evaluation MVP: the repository contains no real observations or cumulative
decision report.

## Position and invocation

The Native Agent, user, or CI owns the task, chooses modules, and decides
whether Eval is useful after the task reaches a terminal outcome.

```text
Native Agent / User / CI
        |
        | uses Spec, Ward, Seal, or none as needed
        v
  terminal task outcome
        |
        | selects Eval when measurement is useful
        v
 post-task observation
        |
        v
 aggregate report and human decision
```

Caller-owned invocation is not self-activation. Eval does not detect task
completion, schedule itself, modify the completed task, invoke another module,
choose workflow order, repair work, or apply a release decision. Appending an
observation requires the caller to have Host write authority for the external
private data file. A standing user or Host policy may provide that authority;
Eval adds no per-observation approval workflow.

The provider-neutral discovery contract is
[`toolkit-module.json`](toolkit-module.json). A Toolkit registry may link to
that contract, but it must not execute Eval or turn it into a central runtime.

## Observation model

One user objective is one real task. Retries, resumptions, recovery attempts,
and OS-specific reruns remain part of that task. Synthetic observations test
the contract and never count as real-task evidence.

Each observation records bounded facts about:

- task type, terminal outcome, Agent, model, and primary host OS;
- which Spec, Ward, and Seal versions were used;
- task effects that can be assessed whether a module was used or not;
- module-specific decisions, defects, cost, and friction for modules that were
  used.

Unknown or unassessed values remain `null`; they are never guessed as false or
zero. Time is either measured, a bounded estimate, or `null`.

Read [CHARTER.md](CHARTER.md), [protocol.md](protocol.md), and
[PRIVACY.md](PRIVACY.md) before appending data.

## Private data and single writer

Raw observations stay outside every source checkout:

- `$XDG_STATE_HOME/jgoneit/eval/v1/observations.jsonl` when
  `XDG_STATE_HOME` is set to an absolute path;
- `$HOME/.local/state/jgoneit/eval/v1/observations.jsonl` only when
  `XDG_STATE_HOME` is unset and `HOME` is absolute.

Fail closed for relative state roots or any resolved path inside a Git
worktree. On POSIX-like hosts, the state directory has mode `0700` and the file
has mode `0600`; other hosts use equivalent current-user-only access controls.

Eval v0.1 permits exactly one active writer for an observation file. If the
caller cannot establish exclusive single-writer access, it does not append.
There is no daemon, lock service, concurrent merge protocol, or shared Toolkit
state.

## Development verification

Eval has no Python or Go product implementation. Repository verification uses a
pinned development-only JSON Schema validator:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements-dev.txt
scripts/verify.sh
```

This verifies schemas, synthetic fixtures, privacy canaries, report structure,
and repository boundaries. It is not an installed Eval command and does not
read, validate, or mutate the user's private observation file. The designated
writer and report reviewer remain responsible for protocol-level date, bound,
count, and revision-chain relationships.

## Reports and decisions

Analysis uses the latest valid revision for each real task, discloses missing
values and sample sizes, and treats used-versus-unused results as observational
rather than causal. A private cumulative report remains outside source
checkouts. A sanitized aggregate may be published only after manual privacy
review; public cohorts with `n < 5` and inferable complementary cells are
suppressed.

Eval supplies evidence, not a universal threshold. Retention, modification,
promotion, removal, and additional experiments remain human decisions.

## Completion boundary

Passing `scripts/verify.sh` establishes only that the contract scaffold is
internally consistent. The Evaluation MVP additionally requires real Task
observations and one privacy-reviewed cumulative report. That milestone proves
the evidence workflow operated; it does not prove that a module caused an
outcome or met a release threshold.

## Future automation boundary

Only repeated malformed records, missing versions, inconsistent aggregation,
multiple writers, or repeated reporting work can justify a future Go CLI. The
only candidates are `eval validate`, `eval summarize`, and `eval compare`.

`eval run-agent`, module execution, orchestration, repair, automatic promotion,
and self-triggered recording remain prohibited.
