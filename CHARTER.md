# Eval Charter

## Responsibility

Eval measures whether each Agent Toolkit module creates more value than cost on
real tasks. It records bounded post-task facts and produces deterministic,
observational aggregates for human review.

Eval owns a thin execution surface: the independent `evalctl` CLI and the
`eval` Plugin Skill. It does not delegate execution to Harness or any other
central runtime.

## Position

The Native Agent, user, or CI owns the primary task, module selection,
composition, terminal outcome, and optional Eval selection. Eval begins only
after that caller has identified the task as completed, failed, or abandoned.

Eval's only intended mutating product operation is an authorized, atomic append
to the external private v2 observation log. Validation and analysis are
read-only. Eval does not mutate the completed task or any Toolkit module state.

A caller may select Eval explicitly or under a standing Agent or Host policy.
That caller-owned selection is not self-activation: Eval has no task-completion
listener, scheduler, hook, daemon, or background process.

## Invariants

- A task remains executable, completable, and reportable without Eval.
- Eval runs only after a terminal outcome; it never changes that outcome.
- An implicit Native Agent attempt occurs at most once and only under existing
  Host authority. It asks for no additional approval and does not retry.
- Observation failure, including an unavailable CLI, denied write, invalid
  data, or lock failure, cannot fail or revise the primary task.
- Eval never invokes Spec, Ward, Seal, another module, an Agent, Harness, or CI.
- Eval owns no workflow transition, execution order, retry, repair, release
  gate, or module selection.
- Observation never changes how the primary Agent gathers evidence, chooses
  tools, or completes its work.
- Observation v1 is read-only compatibility data. New writes are v2 and v1/v2
  rows cannot share a correction chain.
- Module absence means unassessed. Only explicit `used: false` enters an unused
  cohort.
- A used module with an unavailable exact public version remains a used
  observation and is excluded only from version comparison.
- Metrics and task effects use closed, versioned, locally registered
  extensions. Unknown or unassessed facts remain absent or `null`.
- Core validates row shape, cross-value semantics, and revision chains before
  writing or analyzing observations.
- The private writer permits one exclusive writer at a time and replaces the
  complete log atomically; it never exposes a partial append.
- Raw observations remain outside source repositories under current-user-only
  access.
- Analysis is deterministic, discloses sample and exclusion counts, and keeps
  measured, bounded, and unavailable values distinct.
- Analysis never states causality or emits a product, retention, promotion,
  removal, or release decision.
- Publication requires manual privacy review, and final product and release
  decisions remain human-owned.

## Fixed non-goals

Eval is not:

- Agent execution or orchestration;
- module execution, version probing, retry, or repair;
- a central Toolkit runtime, Harness lifecycle, or shared workflow state;
- a task-completion listener, autonomous scheduler, or self-triggering service;
- automatic release approval, promotion, retention, or removal;
- a real-time telemetry platform or dashboard-first product;
- repository context, Agent Memory, RAG, or prompt injection;
- prompt, transcript, Chain-of-Thought, command, source-code, secret, or raw
  per-event user behavior collection;
- automatic publication, retention, deletion, synchronization, or transmission
  of private observations; or
- proof that an observational difference is causal.

## Maturity boundary

Eval `0.2.0-dev.0` is a development product. Product verification can establish
that the CLI, contracts, writer, analysis, Plugin, and Skill follow their
defined boundaries. It cannot establish module value.

This repository contains no real-task observation set and no cumulative
decision report. A future privacy-reviewed cumulative report would show only
that the evidence workflow operated; it would not prove causality or authorize
a release.
