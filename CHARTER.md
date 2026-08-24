# Eval Charter

## Responsibility

Eval measures whether each Agent Toolkit module creates more value than cost on
real tasks.

It provides a bounded post-task observation and reporting contract for module
effects, defects, cost, and user friction.

## Position

The Native Agent, user, or CI owns task execution, module choice, composition,
and Eval invocation. Eval begins only after that caller identifies a terminal
task outcome and decides measurement is useful.

Eval's one intended operational side effect is an authorized append to the
external private observation log. It does not mutate the user's completed task
or any Toolkit module state.

## Invariants

- A task remains executable and completable without Eval.
- The Native Agent, user, or CI explicitly invokes Eval; Eval never
  self-activates.
- Observation requires a terminal outcome and Host write authority.
- Eval never invokes Spec, Ward, Seal, an Agent, or CI.
- Eval owns no workflow transition, execution order, retry, repair, or release
  gate.
- No module automatically invokes Eval, and Eval does not automatically invoke
  another module.
- Task effects remain separate from used-only module metrics so used and unused
  cohorts can be compared without inventing module behavior.
- Unknown and unassessed values remain `null`.
- Observation and report contracts are versioned and provider-neutral.
- Raw observations remain outside source repositories under current-user-only
  access.
- One observation file has one active writer.
- Reports disclose sample sizes, missing values, and observational limitations.
- Product and release decisions remain human-owned.

## Fixed non-goals

Eval is not:

- Agent execution or orchestration;
- module execution, retry, or repair;
- a Plugin, Skill, Hook, or automatic task-completion listener;
- a central Toolkit runtime or shared lifecycle state;
- automatic release approval or promotion;
- a real-time telemetry platform or dashboard-first product;
- repository context, Agent Memory, RAG, or prompt injection;
- prompt, transcript, Chain-of-Thought, command, source-code, secret, or raw
  per-event user behavior collection;
- proof that an observational difference is causal.

## Maturity boundary

Contract verification proves only that the scaffold is internally consistent.
A cumulative report proves only that the evidence workflow operated. Neither
state alone proves that a module is valuable or ready for release.
