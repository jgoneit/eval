#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
schema="$repo_root/schemas/observation-v1.schema.json"

die() {
  echo "error: $*" >&2
  exit 1
}

if [[ -x "$repo_root/.venv/bin/check-jsonschema" ]]; then
  checker="$repo_root/.venv/bin/check-jsonschema"
elif command -v check-jsonschema >/dev/null 2>&1; then
  checker="$(command -v check-jsonschema)"
else
  echo "error: check-jsonschema is unavailable" >&2
  echo "run: python3 -m venv .venv && .venv/bin/python -m pip install -r requirements-dev.txt" >&2
  exit 2
fi

if [[ -x "$repo_root/.venv/bin/python" ]]; then
  python="$repo_root/.venv/bin/python"
elif command -v python3 >/dev/null 2>&1; then
  python="$(command -v python3)"
else
  echo "error: python3 is unavailable" >&2
  exit 2
fi

checker_version="$($checker --version)"
[[ "$checker_version" == "check-jsonschema, version 0.37.4" ]] || {
  echo "error: expected check-jsonschema 0.37.4, got: $checker_version" >&2
  exit 2
}
rg -q '^check-jsonschema==0\.37\.4$' "$repo_root/requirements-dev.txt" ||
  die "check-jsonschema development pin is missing"
[[ -f "$schema" ]] || die "observation schema is missing"
[[ -x "$repo_root/scripts/verify.sh" ]] || die "verification script is not executable"

echo "[1/5] validating the observation schema metaschema"
"$checker" --check-metaschema "$schema"

echo "[2/5] validating the static module contract and synthetic fixtures"
fixture_summary="$($python - "$repo_root" "$checker" <<'PY'
from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
from typing import Any


ROOT = Path(sys.argv[1]).resolve()
CHECKER = sys.argv[2]
SCHEMA = ROOT / "schemas/observation-v1.schema.json"


class StrictJSONFailure(Exception):
    kind = "malformed"


class DuplicateKey(StrictJSONFailure):
    kind = "duplicate"


class NonFiniteNumber(StrictJSONFailure):
    kind = "nonfinite"


class BlankDocument(StrictJSONFailure):
    kind = "blank"


class MalformedDocument(StrictJSONFailure):
    kind = "malformed"


def reject_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise DuplicateKey
        result[key] = value
    return result


def reject_nonfinite(_: str) -> None:
    raise NonFiniteNumber


def strict_document(raw: bytes) -> dict[str, Any]:
    if not raw.strip():
        raise BlankDocument
    try:
        text = raw.decode("utf-8")
        value = json.loads(
            text,
            object_pairs_hook=reject_duplicates,
            parse_constant=reject_nonfinite,
        )
    except (DuplicateKey, NonFiniteNumber):
        raise
    except (UnicodeError, json.JSONDecodeError, RecursionError, ValueError) as error:
        raise MalformedDocument from error
    if not isinstance(value, dict):
        raise MalformedDocument
    return value


def schema_result(raw: bytes) -> int:
    return subprocess.run(
        [
            CHECKER,
            "-q",
            "--force-filetype",
            "json",
            "--schemafile",
            str(SCHEMA),
            "-",
        ],
        input=raw,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode


schema_document = strict_document(SCHEMA.read_bytes())
if schema_document.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
    raise SystemExit("error: observation schema draft changed")
if schema_document.get("additionalProperties") is not False:
    raise SystemExit("error: observation schema must close its top-level object")

expected_observation_fields = [
    "schema_version",
    "observation_id",
    "task_id",
    "revision",
    "supersedes",
    "terminal_on",
    "recorded_on",
    "population",
    "task_type",
    "agent",
    "model",
    "host_os",
    "modules",
    "outcome",
    "task_effects",
    "module_metrics",
]
if schema_document.get("required") != expected_observation_fields:
    raise SystemExit("error: observation required fields changed")
if list(schema_document.get("properties", {})) != expected_observation_fields:
    raise SystemExit("error: observation top-level fields changed")


def assert_closed_objects(value: Any) -> None:
    if isinstance(value, dict):
        if value.get("type") == "object" and value.get("additionalProperties") is not False:
            raise SystemExit("error: observation schema contains an open object")
        for nested in value.values():
            assert_closed_objects(nested)
    elif isinstance(value, list):
        for nested in value:
            assert_closed_objects(nested)


assert_closed_objects(schema_document)

manifest_path = ROOT / "toolkit-module.json"
manifest = strict_document(manifest_path.read_bytes())
expected_manifest = {
    "schema_version": "toolkit-module/v1",
    "id": "eval",
    "name": "Eval",
    "description": "Post-task evidence protocol for measuring Agent Toolkit module value and cost on real tasks.",
    "plane": "evaluation",
    "status": "experimental",
    "kind": "artifact-protocol",
    "phase": "post-task",
    "invocation_owners": ["native-agent", "user", "ci"],
    "requires_terminal_outcome": True,
    "self_activates": False,
    "auto_invokes_modules": False,
    "mutates_user_task": False,
    "side_effects": ["external-private-observation-append"],
    "requires_host_write_authority": True,
    "artifacts": {
        "charter": "CHARTER.md",
        "protocol": "protocol.md",
        "observation_schema": "schemas/observation-v1.schema.json",
        "report_template": "templates/decision-report-v1.md",
    },
}
if manifest != expected_manifest:
    raise SystemExit("error: toolkit-module.json does not match the v1 contract")
for artifact in manifest["artifacts"].values():
    target = (ROOT / artifact).resolve()
    try:
        target.relative_to(ROOT)
    except ValueError as error:
        raise SystemExit("error: module artifact escapes the repository") from error
    if not target.is_file():
        raise SystemExit(f"error: module artifact is missing: {artifact}")

required_headings = [
    "## Report metadata",
    "## Dataset",
    "## Outcomes and cost",
    "## Module findings",
    "## Used-versus-unused and version comparisons",
    "## Limitations",
    "## Human decision",
    "## Privacy review",
]
template = (ROOT / "templates/decision-report-v1.md").read_text(encoding="utf-8")
actual_headings = [line for line in template.splitlines() if line.startswith("## ")]
if actual_headings != required_headings:
    raise SystemExit("error: decision report sections do not match the v1 contract")

valid_path = ROOT / "fixtures/valid/observations.jsonl"
valid_data = valid_path.read_bytes()
if not valid_data:
    raise SystemExit("error: valid observation fixture is empty")
valid_rows = 0
valid_documents: list[dict[str, Any]] = []
for line_number, raw in enumerate(valid_data.splitlines(), 1):
    try:
        row = strict_document(raw)
    except StrictJSONFailure as error:
        raise SystemExit(
            f"error: valid observation line {line_number} is not strict JSON"
        ) from error
    if row.get("population") != "synthetic":
        raise SystemExit(
            f"error: valid observation line {line_number} is not synthetic"
        )
    if schema_result(raw) != 0:
        raise SystemExit(
            f"error: valid observation line {line_number} failed schema validation"
        )
    valid_documents.append(row)
    valid_rows += 1

if valid_rows != 6:
    raise SystemExit("error: valid observation matrix must contain exactly six rows")
module_vectors = {
    tuple(row["modules"][name]["used"] for name in ("seal", "ward", "spec"))
    for row in valid_documents
}
expected_vectors = {
    (False, False, False),
    (False, False, True),
    (False, True, False),
    (True, False, False),
    (True, True, True),
}
if module_vectors != expected_vectors:
    raise SystemExit("error: valid observation module-use matrix is incomplete")
time_methods = {
    row["outcome"]["module_interaction_time"]["method"]
    for row in valid_documents
    if row["outcome"]["module_interaction_time"] is not None
}
if time_methods != {"measured", "bounded-estimate"}:
    raise SystemExit("error: valid observation time-method matrix is incomplete")

latest_by_task: dict[str, dict[str, Any]] = {}
for row in valid_documents:
    previous = latest_by_task.get(row["task_id"])
    if previous is None:
        if row["revision"] != 1 or row["supersedes"] is not None:
            raise SystemExit("error: valid observation fixture has a broken root")
    elif (
        row["revision"] != previous["revision"] + 1
        or row["supersedes"] != previous["observation_id"]
    ):
        raise SystemExit("error: valid observation fixture has a broken revision chain")
    latest_by_task[row["task_id"]] = row
if not any(row["revision"] > 1 for row in valid_documents):
    raise SystemExit("error: valid observation matrix lacks a revision correction")

schema_invalid = sorted((ROOT / "fixtures/invalid/schema").glob("*.json"))
if not schema_invalid:
    raise SystemExit("error: no schema-invalid observation fixtures found")
for path in schema_invalid:
    try:
        document = strict_document(path.read_bytes())
    except StrictJSONFailure as error:
        raise SystemExit(f"error: schema-invalid fixture is not strict JSON: {path.name}") from error
    if document.get("schema_version") != "eval-observation/v1":
        raise SystemExit(f"error: schema-invalid fixture has wrong contract: {path.name}")
    if document.get("population") != "synthetic":
        raise SystemExit(f"error: schema-invalid fixture is not synthetic: {path.name}")
    result = schema_result(path.read_bytes())
    if result != 1:
        raise SystemExit(f"error: expected schema rejection: {path.name}")

raw_invalid = sorted((ROOT / "fixtures/invalid/raw").glob("*.jsonl"))
if not raw_invalid:
    raise SystemExit("error: no raw-invalid fixtures found")
failure_kinds: set[str] = set()
for path in raw_invalid:
    data = path.read_bytes()
    try:
        if not data:
            raise BlankDocument
        for raw in data.splitlines():
            strict_document(raw)
    except StrictJSONFailure as error:
        failure_kinds.add(error.kind)
    else:
        raise SystemExit(f"error: expected strict JSON rejection: {path.name}")
if not {"blank", "duplicate", "nonfinite", "malformed"}.issubset(failure_kinds):
    raise SystemExit("error: raw-invalid fixtures do not cover strict JSON failures")

print(
    f"{valid_rows} valid rows; {len(schema_invalid)} schema-invalid and "
    f"{len(raw_invalid)} raw-invalid fixtures rejected"
)
PY
)"

echo "[3/5] checking ignore and minimal-surface rules"
ignored_canaries=(
  "observations.jsonl"
  ".local/observations.jsonl"
  "reports/private/example.md"
  "reports/generated/example.md"
  "reports/drafts/example.md"
  "scratch.tmp"
)
for canary in "${ignored_canaries[@]}"; do
  git -C "$repo_root" check-ignore -q --no-index -- "$canary" ||
    die "private/generated path is not ignored: $canary"
done

tracked_canaries=(
  "toolkit-module.json"
  "schemas/observation-v1.schema.json"
  "fixtures/valid/observations.jsonl"
  "templates/decision-report-v1.md"
)
for canary in "${tracked_canaries[@]}"; do
  if git -C "$repo_root" check-ignore -q --no-index -- "$canary"; then
    die "contract artifact is unexpectedly ignored: $canary"
  fi
done

obsolete_paths=(
  "$repo_root/schemas/study-v1.schema.json"
  "$repo_root/fixtures/valid/studies.jsonl"
  "$repo_root/fixtures/invalid/dataset"
  "$repo_root/scripts/validate-data.sh"
)
for path in "${obsolete_paths[@]}"; do
  [[ ! -e "$path" ]] || die "obsolete study/runtime surface exists: ${path#"$repo_root/"}"
done

while IFS= read -r path; do
  die "unexpected minimal-contract file exists: ${path#"$repo_root/"}"
done < <(
  find "$repo_root/schemas" -type f ! -path "$schema" -print
  find "$repo_root/fixtures/valid" -type f \
    ! -path "$repo_root/fixtures/valid/observations.jsonl" -print
  find "$repo_root/scripts" -type f ! -path "$repo_root/scripts/verify.sh" -print
)

while IFS= read -r path; do
  die "prohibited product surface exists: ${path#"$repo_root/"}"
done < <(
  find "$repo_root" \
    \( -path "$repo_root/.git" -o -path "$repo_root/.venv" \) -prune -o \
    \( \
      -type d \( -name .codex-plugin -o -name skills -o -name hooks \) -o \
      -type f \( -name SKILL.md -o -name plugin.json -o -name '*.py' -o \
        -name '*.go' -o -name go.mod -o -name go.sum \) \
    \) -print
)

while IFS= read -r path; do
  case "$path" in
    "$repo_root/fixtures/valid/observations.jsonl" | \
      "$repo_root"/fixtures/invalid/raw/*.jsonl) ;;
    *) die "checkout-local raw observation data exists: ${path#"$repo_root/"}" ;;
  esac
done < <(
  find "$repo_root" \
    \( -path "$repo_root/.git" -o -path "$repo_root/.venv" \) -prune -o \
    -type f -name '*.jsonl' -print
)

for path in \
  "$repo_root/.local" \
  "$repo_root/reports/private" \
  "$repo_root/reports/generated" \
  "$repo_root/reports/drafts"; do
  [[ ! -e "$path" ]] || die "checkout-local private/generated data exists: ${path#"$repo_root/"}"
done

echo "[4/5] checking shell syntax and repository whitespace"
script_count=0
for shell_script in "$repo_root"/scripts/*.sh; do
  bash -n "$shell_script"
  script_count=$((script_count + 1))
done
[[ "$script_count" -eq 1 ]] || die "scripts directory must contain only verify.sh"

"$python" - "$repo_root" <<'PY'
from pathlib import Path
import sys


root = Path(sys.argv[1])
errors = []
for path in sorted(root.rglob("*")):
    if not path.is_file() or ".git" in path.parts or ".venv" in path.parts:
        continue
    data = path.read_bytes()
    relative = path.relative_to(root)
    if data and not data.endswith(b"\n"):
        errors.append(f"{relative}: missing final newline")
    if data.endswith(b"\n\n"):
        errors.append(f"{relative}: blank line at end of file")
    for line_number, line in enumerate(data.splitlines(), 1):
        if line.endswith((b" ", b"\t")):
            errors.append(f"{relative}:{line_number}: trailing whitespace")
if errors:
    raise SystemExit("\n".join(f"error: {item}" for item in errors))
PY
git -C "$repo_root" diff --check

echo "[5/5] verification complete"
echo "Eval verification passed: $fixture_summary."
