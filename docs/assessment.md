# Coding assessment

The assessment workflow links a fixed coding case to one submitted artifact,
grades supplied execution evidence, and compares two instruction conditions.
It is independent of the Ward/Seal ledger and its human review rules. Eval
does not execute agents, run supplied commands, contact a model judge, select a
winning strategy, or deploy changes. A separate dashboard can consume the JSON.

## Commands and storage

```sh
go build -o bin/evalctl-assessment ./cmd/evalctl
bin/evalctl-assessment assess --suite suite.json --attempts baseline.json --out "$PRIVATE_OUTPUT/baseline"
bin/evalctl-assessment assess --suite suite.json --attempts candidate.json --out "$PRIVATE_OUTPUT/candidate"
bin/evalctl-assessment compare --baseline "$PRIVATE_OUTPUT/baseline/assessment.json" --candidate "$PRIVATE_OUTPUT/candidate/assessment.json" --format json
bin/evalctl-assessment compare --baseline "$PRIVATE_OUTPUT/baseline/assessment.json" --candidate "$PRIVATE_OUTPUT/candidate/assessment.json" --format markdown
```

`PRIVATE_OUTPUT` must name an absolute, private location outside Git. `assess`
creates an absent destination directory containing `assessment.json`,
`report.md`, and the private evidence sidecar `inputs.json`. Existing directories are never replaced, including after an
earlier successful run. Publication uses the existing private artifact store:
mode 0700 directories and 0600 files on POSIX, protected Windows storage,
symlink/path checks, file sync, and atomic directory publication without replacement.
Each retained JSON artifact is bounded to 64 MiB; inputs whose serialized
sidecar exceeds that limit are rejected before publication.

The receipt distinguishes publication from final directory durability:

```json
{"status":"assessed","committed":true,"durability":"confirmed"}
```

Exit 0 means valid input and successful output; it does **not** mean the coding
attempt passed. Exit 2 means the complete bundle was published but final
durability was not confirmed. Inspect the existing bundle instead of retrying
into the same directory. Invalid data, incompatible comparisons, I/O failures,
or canceled work return 1; invalid arguments return 64. Errors use static
reasons and do not echo private input or filesystem paths. `compare` is
read-only and defaults to JSON. It reads `inputs.json` from the same directory
as each supplied assessment, validates its digest, and regrades the retained
suite and attempts. The complete recomputed assessment must match the supplied
result before comparison. Keep the three files together; a missing or changed
sidecar is an error, and comparison never searches other directories for it.
Private bundle reads retain the storage checks for symlinks, file identity,
permissions, and bounded size. Neither command reads or appends an experiment
Journal or uses an experiment state root.

## Public contracts

The versioned contracts are:

| Schema | Contents |
| --- | --- |
| [eval-suite/v1](../schemas/suite-v1.schema.json) | Suite ID/version; planned cases; fixed input and criteria digests; initial file manifest; exact allowed and protected paths; requirement and regression checks |
| [eval-attempts/v2](../schemas/attempts-v2.schema.json) | Suite identity; instruction condition; model/reasoning/permission/environment/tool identity; attempts; configuration observations; final file manifest; checks; normalized events; optional measurements |
| [eval-attempts/v1](../schemas/attempts-v1.schema.json) | Retained earlier inputs; configuration observation is unavailable when reassessed |
| [eval-assessment-inputs/v1](../schemas/assessment-inputs-v1.schema.json) | Private sidecar containing the validated suite and attempts needed to reproduce the assessment |
| [eval-assessment/v2](../schemas/assessment-v2.schema.json) | Evaluator version; input sidecar digest; case outcomes; configuration observations; process findings; sanitized evidence references and origins; coverage; observed metrics; planned denominator |
| [eval-comparison/v2](../schemas/comparison-v2.schema.json) | Instruction comparison kind; paired case changes and configuration comparability; comparable and unevaluated populations; missing attempts; candidate-minus-baseline measurements and their coverage |

The evaluator is `rules-v2`. Earlier assessment/comparison output contracts are
not accepted by `compare`. Regenerate assessments from the preserved suite and
attempts into new private output directories. Earlier attempt inputs remain
usable, but absence of a configuration observation never implies an unchanged
configuration.

JSON Schema describes the structural interface. The Go validator additionally
enforces digest linkage, uniqueness, cross-field consistency, and comparison
compatibility. Unknown or duplicate object keys, trailing JSON, oversized
input, negative measurements, malformed paths, and duplicate cases/attempts
are rejected. There is at most one attempt per case per condition; the
protocol provides no best-retry selection. An absent attempt remains in the
planned population.

Suite manifests use normalized relative file paths. They remain private input,
including in `inputs.json`; do not publish that sidecar to a dashboard.
Aggregate assessment and comparison outputs contain case IDs, safe metadata tokens,
digests, enumerated statuses, evidence IDs, counts, and measurements instead.
They omit file paths, raw commands/output, conversations, and reasoning.
Labels are not a place to embed private text. Supplied provenance distinguishes
`agent_report`, `host_record`, and `independent_check`. These are provenance
claims: an ID or matching digest does not authenticate the producer or prove
that the Host executed a check. Retained evidence must remain reviewable.

## Digest recipes

Digests are lowercase SHA-256 hex over UTF-8 bytes. Every line below ends in
LF; fields are separated by a literal tab. Paths and IDs cannot contain tabs
or newlines. File content digests hash exact bytes, not normalized source.

* A file manifest starts with `eval-files/v1` and then contains sorted
  `path<TAB>digest` lines. `input_digest` hashes the initial manifest;
  `artifact_digest` hashes the complete submitted manifest.
* Criteria start with `eval-criteria/v1`. Append sorted
  `allow<TAB>path` lines, sorted `protect<TAB>path` lines, then checks sorted by
  ID as `check<TAB>id<TAB>kind<TAB>checker_digest` lines.
* A suite starts with `eval-suite/v1`, suite ID, and suite version on separate
  lines, then cases sorted by ID as
  `id<TAB>input_digest<TAB>criteria_digest` lines.
* `inputs_digest` hashes `eval-assessment-inputs/v1` followed by LF and the
  compact typed JSON produced by Go `json.Marshal(AssessmentInputs)`, without
  a trailing LF. `inputs.json` may be indented; validation decodes and hashes
  the typed value. Array order is preserved in this digest.

The external tool freezes the checker implementation and computes its digest.
Every check names that fixed checker and the submitted artifact before and
after checking. Any mismatch is invalid input, including a result copied from
an earlier artifact or altered tests presented under the old criteria digest.
The core validates internal consistency; it does not open manifest paths to
independently authenticate their bytes.

Comparison recomputes file-scope and protected-file findings from those
retained manifests and criteria. Changing findings and their totals together
does not bypass this check. Matching digests and recomputation establish
consistency with the supplied inputs, not authentic Host origin. Replacing
inputs, their digests, and all derived results consistently is not detectable
as producer impersonation without an independent trust mechanism.

## Grading rules

Requirements and preservation of existing behavior use separate, fixed checks.
Only independent execution with Host or independent provenance can establish
their results. Agent verification remains visible separately. Completion text,
a process exit code, and an agent's claimed test result cannot establish success.

Every finding uses `pass`, `fail`, `unavailable`, or `error`. A confirmed failure
takes precedence; checker error is distinct from an agent defect; missing
required evidence is unavailable. All required checks must confirm success for
an outcome to pass. Timeout, interruption, agent errors, and authentication or
environment failures remain explicit terminal reasons. A terminated attempt
does not become successful simply because its process stopped.

Process grading is limited to three rules:

* **Allowed scope:** compare complete submitted and initial manifests; additions,
  deletions, and modifications must use exact allowed file names.
* **Protected integrity:** provided tests and criteria files must retain their
  original content digests. Final snapshots cannot establish that an unchanged
  file was never temporarily edited.
* **Permission compliance:** require complete Host permission observation and
  explicit Host events. A denied request is not by itself a policy violation.
  Missing or partial permission observation stays unavailable.

Manifest checks require Host or independent provenance and complete coverage.
They describe the submitted workspace, not every action on the machine. Tool
calls are grouped by kind with failure and repeated-fingerprint counts. A
repeat is a count, not a judgment that work was unnecessary. Observation gaps
remain visible. Agent verification and independent verification are counted
separately as verification attempts. An unavailable check record does not
prove execution and does not increment that count; an explicit observed
verification event can record a started attempt with an unknown result.
Neither a partial trace nor absent metrics is filled with invented events or
zero measurements.

## Comparison rules and limits

Compare the same suite/version, input and criteria digests, evaluator, model,
reasoning, permission profile, environment, and tool digest. Incompatible
assessments are rejected. Different instruction digests produce
`kind: different_instruction`; equal digests produce `kind: same_instruction`,
regardless of the condition labels. Same-instruction runs can show execution
variation, but do not establish the effect of changing instructions.
Every planned case appears, including missing attempts and interrupted or
environment-failed runs. Only definitive result pairs whose attempts both
completed and have compatible configuration observations count as comparable.
Each new attempt records `configuration_observation` with a status of
`unchanged`, `changed`, or `unavailable` and nullable `expected_digest`,
`before_digest`, and `after_digest`. Both attempts must report unchanged
configuration under the same expected digest. A missing observation or a
configuration change leaves the pair unevaluated, while preserving the attempt
and its check findings. Missing, interrupted, and environment-failed
pairs remain unevaluated even when a checker confirmed that their submitted
source failed. The failure finding stays visible; it does not establish a
strategy regression. The same completion gate applies to process and
individual rule changes. Outcome and process changes are separate; no composite
score or automatic winner is produced.

Time and token differences are candidate minus baseline for observed paired
values, with per-metric sample counts. Those observations remain visible even
when configuration uncertainty excludes a pair from result and process
changes. Missing measurements stay `null`. Observed runtime differences do
not establish time saved; concurrent load, caching,
and model variability can affect them. Three cases and one attempt per
condition are a feature pilot, not evidence of general strategy superiority.

## External pilot and rollback

[The pilot tool](../tools/pilot/README.md) supplies three frozen Go cases and
six fresh Codex executions. It copies submitted source into an independent
workspace with the original checker tests, retains normalized evidence, and
can regenerate the assessment inputs. Other Native Agents or CI can implement
the same contract without adopting this runner.

Build assessment binaries at a separate path from the pinned production
collector. No automation configuration or collector hash update is required.
To discontinue the feature, stop using `assess`, `compare`, and the pilot tool.
Existing `observe`, collection, human review, reports, and journals continue
under their existing contracts. Removal does not require a journal migration.
