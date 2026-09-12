# Fixed Go coding pilot

This Python 3.11 or newer standard-library tool produces Eval suite and attempt inputs. It runs outside `evalctl`: assessment never starts an agent or executes submitted code.

The three cases cover pagination boundaries, quantity parsing with existing-format compatibility, and stable deduplication with input preservation. Each case contains broken initial code, requirements, a public test, original independent requirement/regression tests, and a golden solution used only for fixture validation. The nested `go.mod` isolates these source fragments from the Eval repository's `go test ./...`; use the commands below to validate them.

```sh
python3 -m unittest discover -s tools/pilot -p 'test_*.py'
python3 tools/pilot/run.py self-test
python3 tools/pilot/run.py preflight --codex /opt/homebrew/bin/codex --model gpt-5.5 --reasoning xhigh
python3 tools/pilot/run.py run --out /absolute/private/new-pilot-directory \
  --codex /opt/homebrew/bin/codex --model gpt-5.5 --reasoning xhigh
```

The output parent must already exist. The destination must be absent, outside Git, and reached without a symlink. The tool creates private directories and files without replacement. Preflight checks local Codex login status, tool availability, the refreshed Codex model catalog, supported reasoning effort, and the fixtures. Login and catalog checks do not send a model-generation request and do not prove a later request will succeed. The catalog's instructions and other raw contents are discarded. `self-test` requires only Go, with dependency downloads disabled; it proves each initial source fails requirements while preserving the regression case and each golden source passes both checks.

The run command performs exactly one attempt for each of the three cases under each condition, in case order with baseline then candidate. Each attempt starts a new workspace and `codex exec --json --ephemeral` process. Both conditions use the same explicit model, reasoning level, `workspace-write` sandbox request, inherited user configuration, offline Go environment, and 300-second deadline. The candidate instruction adds defect reproduction, minimal changes, and final verification. The tool does not alter user configuration, bypass approval rules, or retry a failed run. Configuration bytes are checked against the preflight digest before and after every attempt, including the sixth. A change, missing/unreadable configuration, or interruption stops further attempts and retains planned/missing counts. Already attempted records preserve the actual Agent termination and checks; configuration uncertainty does not rewrite a successful Agent exit as an Agent failure. Do not rerun an output directory to replace an unfavorable result.

Each output directory contains:

- `suite.json`, `baseline-attempts.json`, and `candidate-attempts.json`: the `eval-suite/v1` and `eval-attempts/v2` input contracts. Each attempt includes `configuration_observation` with `status` (`unchanged`, `changed`, or `unavailable`) and expected/before/after byte digests. Unreadable bytes stay null. Only three observed, equal digests establish `unchanged`; changed/unavailable attempts remain in the denominator and their measured costs are preserved, but result/process comparisons are unevaluated.
- `evidence/<attempt>/attempt.json`, `runner.json`, and `task.go`: normalized evidence, bounded private failure-reason codes, and the submitted source, suitable for linking and rechecking.
- `snapshot/`: the exact harness, requirements, initial/public/independent tests, and golden solutions retained for reproduction.
- `preflight.json`, `environment.json`, `prompts.json`, and `run.json`: tool/configuration digests, fixed instructions, planned counts, and run disposition. Configuration content and authentication credentials are never copied. Capture occurs after authentication/catalog initialization and that frozen digest remains the expected value. Exact-byte, canonical TOML, and per-top-level hashes include all security/tool/instruction and notice settings. Any byte change or observation gap stops subsequent runs; `configuration-drift.json` retains diagnostic hashes and the before/after stage. The same attempt observation is recorded in immutable `attempt.json` and private `runner.json` before publication.
- `workers/`: the separate workspaces. These are private working artifacts and may contain agent-created files; use the normalized input/output JSON for aggregate reporting.

Example assessment and comparison commands, using a separately built binary:

```sh
evalctl assess --suite /absolute/private/new-pilot-directory/suite.json \
  --attempts /absolute/private/new-pilot-directory/baseline-attempts.json \
  --out /absolute/private/new-pilot-directory/baseline-assessment
evalctl assess --suite /absolute/private/new-pilot-directory/suite.json \
  --attempts /absolute/private/new-pilot-directory/candidate-attempts.json \
  --out /absolute/private/new-pilot-directory/candidate-assessment
evalctl compare \
  --baseline /absolute/private/new-pilot-directory/baseline-assessment/assessment.json \
  --candidate /absolute/private/new-pilot-directory/candidate-assessment/assessment.json \
  --format json
```

Reassess those same retained suite/attempt files into two new output directories, then compare them again. Each assessment directory includes private `inputs.json`; keep it alongside `assessment.json` because comparison verifies its digest and regrades its contents. Assessment/comparison outputs use v2 and the `rules-v2` evaluator. Existing v1 assessment outputs must be regenerated from preserved suite/attempt inputs; a v1 attempt has unavailable configuration observations. JSON and Markdown assessment output is deterministic; a new Agent run is neither needed nor a substitute for reproducing the same evidence. To repeat the independent checks, import the retained `snapshot/run.py` and invoke `run_check(case_id, retained_source_bytes, kind)`, where kind is `requirement` or `regression`. This uses the retained original tests. It does not launch Codex.

## Evidence boundaries

The normalizer discards messages, reasoning, raw commands, command output, and source paths from event records. It retains sequence IDs, supported event/tool kinds, status, exact-command SHA-256 fingerprints, and reported usage. A direct `go test` command (optionally wrapped by a shell) is counted as Agent verification; compound shell scripts are deliberately not inferred as verification. Independent checks are separate records bound to the submitted manifest and source digest. Repeated command fingerprints are counts, not judgments of wasted work.

Unknown event types, malformed events, oversized records, and unfinished tools reduce event coverage. Permission coverage is unavailable: this event adapter has no explicit Host permission-event contract and does not infer compliance from successful tool execution or the requested sandbox setting. The `workspace-write` label records the CLI request; effective permissions remain unavailable, including when managed configuration supplies a permission default. Local `host_record` and `independent_check` provenance labels identify the producing mechanism; supplied IDs/digests do not authenticate the Host or constitute an attestation.

The complete final file manifest covers the worker workspace except `.git` metadata. It compares protected files and the allowed source path against the initial manifest. Unsafe names, unreadable/special files, or collection bounds make coverage partial. Symlinks are recorded without following their targets and are never copied as submitted source. Only submitted `task.go` bytes are copied into an independent checker workspace with the frozen module/tests. The tool verifies source/manifest consistency, checks the worker manifest before and after grading, and rejects checker-workspace mutation as a checker error. This is a workspace-level process rule, not observation of every intermediate filesystem action or a global permission audit.

`completed` records a successful Codex terminal event and process exit; correctness comes from the independent checks. Timeouts, interruptions, Agent errors, authentication errors, and environment errors remain distinct. Explicit model/effort, API transport, rate-limit, and server failures are environment failures. An unclassified failure before any observed Agent activity is `environment_error` with private reason `startup_failure_unknown`; a started thread alone does not prove Agent performance. Missing usage is null; duration is observed controller elapsed time and is not claimed to measure time saved. Submitted-source compilation diagnostics and failed tests produce failed results. A removed or incompatible submitted API can instead fail compilation at a fixed test's call or use of its return value: this is a result failure only when the local compiler diagnostic is linked to a frozen test and the golden source passes a countercheck. The countercheck is an independent evaluation, not Agent verification, and its frozen source is included in the checker digest. Checker startup/timeout, changed checker files, a failing golden countercheck, missing execution evidence, and unattributed build failures produce errors.

This is a six-run functionality demonstration. It provides separate result, process, and observed cost evidence and cannot establish general superiority of either instruction strategy. The local checker executes submitted Go code; use these controlled fixtures in a trusted local pilot environment. The tool is not a hardened service for arbitrary hostile submissions.
