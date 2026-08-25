---
name: eval
description: Record one private, best-effort Eval observation after a Native Agent task reaches a completed, failed, or abandoned outcome, or handle an explicit $eval request to observe, validate, summarize, or compare Eval data. For implicit selection, read and apply this Skill silently from the outset without announcing its name, selection, preflight, or progress; only its exact final success line may mention Eval. Do not use during active work, for discussion-only requests, or to influence workflow and tool choices.
---

# Eval

Eval is a post-task observer and deterministic analysis surface. The independent `evalctl`
binary on `PATH` owns all reads and writes; this Skill does not implement storage or analysis.

## Select the mode

- For a request that only discusses or explains Eval, answer it without running `evalctl` or
  reading private observations.
- For an explicit operational `$eval` request, run only the requested `observe`, `validate`,
  `summarize`, or `compare` operation.
- When selected implicitly, use only the terminal observation procedure below and only after the
  primary task has settled as `completed`, `failed`, or `abandoned`.

The Host or Agent selects this Skill. Eval does not detect task completion, schedule itself, or
activate another module.

## Core preflight

Before an operational request, locate `evalctl` on `PATH` and read `evalctl --version`. Continue
only for a public version in the range `>=0.2.0-dev.0,<0.3.0`.

For an explicit request, report a missing, unsupported, or failed Core as a blocker without
installing or repairing it. For implicit use, remain silent and return to the primary response.

## Explicit operations

Preserve the user's requested arguments and scope. Use these Core commands:

- `evalctl observe --input - [--best-effort] [--state-root ABS]`
- `evalctl validate [--file PATH | --state-root ABS]`
- `evalctl summarize --as-of DATE [--from DATE] [--through DATE] [--format json|markdown]`
- `evalctl compare --module ID --by usage|version [--left VERSION --right VERSION] --as-of DATE`

Do not print raw private observation rows. A validation, summary, or comparison report is safe to
return only to the caller that requested it; do not publish or share it automatically. Never turn
analysis into a causal claim, release recommendation, or retain/promote/remove decision.

## Implicit terminal observation

Run this procedure at most once for the primary task, after its artifact and terminal outcome are
already fixed. Observation must not change the task result or delay its handoff.

In implicit mode, do not announce Skill use or show commentary, progress, preflight, command,
status, or failure text. The exact success line defined below is the only Eval-related text that
may become user-visible.

1. Perform the Core preflight without any user-visible output, approval requests, installation, or
   permission escalation. Inspect command results directly; do not redirect preflight or observation
   output into a temporary, workspace, or state file. If the current Host authority does not already
   permit the observation write, stop silently before calling `observe`. Do not retry a failed or
   skipped attempt.
2. Build one draft only from facts already available in the task context. The draft may contain an
   existing `task_id` only for a known correction. Core rejects a supplied `task_id` unless it
   matches an existing valid v2 chain. The draft must contain `terminal_on`, `population`,
   `task_type`, `agent`, `model`, `host_os`, `modules`, `outcome`, and `task_effects`. Never supply
   `schema_version`, `observation_id`, `revision`, `supersedes`, or `recorded_on`; Core generates
   them.
3. Call `evalctl observe --best-effort --input -` exactly once. Supply the complete draft in that
   same tool call through a pipe or heredoc; never invoke `observe` with bare or inherited stdin.
   If the complete draft cannot be supplied, stop silently without invoking Core. Do not select a
   state root unless the Host or user already supplied one for this task.
4. Suppress Core output. Only when Core returns a successful `recorded` status, append this exact
   standalone line to the primary final response:

   `Eval: observation recorded.`

For a missing or unsupported Core, denied write, invalid draft, lock/storage error, or Core
`skipped` status, add no Eval line and expose no indication that Eval was attempted.

## Draft facts

The smallest useful implicit draft has this shape; replace the date, task type, OS, and outcome
with facts from the completed task, and add only assessed module entries:

```json
{
  "terminal_on": "YYYY-MM-DD",
  "population": "real",
  "task_type": "other",
  "agent": "codex",
  "model": null,
  "host_os": null,
  "modules": {},
  "outcome": {
    "status": "completed",
    "user_interventions": null,
    "module_interaction_time": null,
    "rework_required": null
  },
  "task_effects": {}
}
```

- Use `population: "real"` for an actual user task and the actual terminal status:
  `completed`, `failed`, or `abandoned`.
- Never include Eval itself in `modules` for an implicit observation. Eval is the post-task observer,
  not a module materially used to produce the primary task outcome.
- Omit a module when its usage is unassessed. Set `used: false` only when non-use is known.
- Each assessed module has `used`, `version`, and `metrics`. For a used module with a known exact
  public version, use `{"status":"known-public","value":"VERSION"}`. If its exact public
  version is unavailable, use `{"status":"unavailable","value":null}` and keep the used
  observation. For a known unused module, use
  `{"status":"not-applicable","value":null}` and `metrics: null`.
- Do not execute or query another module only to discover its version or metrics. Use
  `metrics: null` when no registered metric envelope is already known.
- Use an empty `task_effects` object when no registered completion, security, or requirements
  effect is already known. Omit unknown effects and never infer `false`, zero, or elapsed time.
- Keep unavailable task facts `null` where the draft contract permits it. Do not ask the user for
  observational data.

## Invariants

Never mutate the primary task, reorder its work, choose tools for measurement, orchestrate or
repair another module, expose identifiers from a raw observation, or automatically publish a
private report. Privacy review and product decisions remain human actions.
