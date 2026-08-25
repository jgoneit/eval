#!/usr/bin/env bash

set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
plugin_validator="/Users/jgoneit/.codex/skills/.system/plugin-creator/scripts/validate_plugin.py"
skill_validator="/Users/jgoneit/.codex/skills/.system/skill-creator/scripts/quick_validate.py"

die() {
  echo "error: $*" >&2
  exit 1
}

command -v go >/dev/null 2>&1 || die "go is unavailable"

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
  die "python3 is unavailable"
fi

checker_version="$($checker --version)"
[[ "$checker_version" == "check-jsonschema, version 0.37.4" ]] ||
  die "expected check-jsonschema 0.37.4, got: $checker_version"
rg -q '^check-jsonschema==0\.37\.4$' "$repo_root/requirements-dev.txt" ||
  die "check-jsonschema development pin is missing"
rg -q '^PyYAML==6\.0\.2$' "$repo_root/requirements-dev.txt" ||
  die "PyYAML development pin is missing"
"$python" -c 'import yaml; assert yaml.__version__ == "6.0.2"' ||
  die "PyYAML 6.0.2 is unavailable; install requirements-dev.txt"

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/eval-verify.XXXXXX")"
trap 'rm -rf -- "$temp_dir"' EXIT

schemas=(
  "$repo_root/schemas/observation-v1.schema.json"
  "$repo_root/schemas/observation-v2.schema.json"
  "$repo_root/schemas/observe-draft-v2.schema.json"
  "$repo_root"/schemas/extensions/*.schema.json
)

echo "[1/8] validating JSON Schema documents"
for schema in "${schemas[@]}"; do
  [[ -f "$schema" ]] || die "schema is missing: ${schema#"$repo_root/"}"
  "$checker" --check-metaschema "$schema"
done

echo "[2/8] building evalctl and validating fixtures"
go -C "$repo_root" build -trimpath -o "$temp_dir/evalctl" ./cmd/evalctl

valid_fixtures=(
  "$repo_root/fixtures/valid/observations.jsonl"
  "$repo_root/fixtures/valid/observations-v2.jsonl"
)
for fixture in "${valid_fixtures[@]}"; do
  "$temp_dir/evalctl" validate --file "$fixture" >/dev/null ||
    die "valid fixture rejected: ${fixture#"$repo_root/"}"
done

invalid_count=0
while IFS= read -r fixture; do
  set +e
  "$temp_dir/evalctl" validate --file "$fixture" >/dev/null 2>&1
  status=$?
  set -e
  [[ "$status" -eq 1 ]] ||
    die "invalid fixture returned $status instead of 1: ${fixture#"$repo_root/"}"
  invalid_count=$((invalid_count + 1))
done < <(
  find "$repo_root/fixtures/invalid" -type f \
    \( -name '*.json' -o -name '*.jsonl' \) -print | sort
)
[[ "$invalid_count" -gt 0 ]] || die "no invalid fixtures were checked"

echo "[3/8] checking privacy and repository boundaries"
ignored_canaries=(
  "observations.jsonl"
  ".local/observations.jsonl"
  "reports/private/example.md"
  "reports/generated/example.md"
  "reports/drafts/example.md"
)
for canary in "${ignored_canaries[@]}"; do
  git -C "$repo_root" check-ignore -q --no-index -- "$canary" ||
    die "private/generated path is not ignored: $canary"
done

while IFS= read -r path; do
  case "$path" in
    "$repo_root/fixtures/valid/observations.jsonl" | \
      "$repo_root/fixtures/valid/observations-v2.jsonl" | \
      "$repo_root"/fixtures/invalid/raw/*.jsonl | \
      "$repo_root"/fixtures/invalid/chains/*.jsonl) ;;
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

"$python" - "$repo_root" <<'PY'
from __future__ import annotations

import json
from pathlib import Path
import sys


root = Path(sys.argv[1])
forbidden = {
    "notes", "note", "prompt", "transcript", "chain_of_thought", "command",
    "arguments", "url", "hostname", "repository", "path", "source_code",
    "secret", "credential", "task_description", "narrative", "rationale",
}


def property_names(value: object) -> set[str]:
    found: set[str] = set()
    if isinstance(value, dict):
        properties = value.get("properties")
        if isinstance(properties, dict):
            found.update(properties)
        for nested in value.values():
            found.update(property_names(nested))
    elif isinstance(value, list):
        for nested in value:
            found.update(property_names(nested))
    return found


for path in sorted((root / "schemas").rglob("*.json")):
    document = json.loads(path.read_text(encoding="utf-8"))
    if document.get("additionalProperties") is not False:
        raise SystemExit(f"error: schema top level is open: {path.relative_to(root)}")
    leaked = sorted(property_names(document) & forbidden)
    if leaked:
        raise SystemExit(f"error: prohibited observation fields in {path.relative_to(root)}: {leaked}")

manifest = json.loads((root / "toolkit-module.json").read_text(encoding="utf-8"))
if manifest.get("version") != "0.2.0-dev.0" or manifest.get("kind") != "native-agent-product":
    raise SystemExit("error: toolkit module product identity is inconsistent")
if manifest.get("self_activates") is not False or manifest.get("mutates_user_task") is not False:
    raise SystemExit("error: toolkit module autonomy boundaries changed")
if [command.get("name") for command in manifest.get("public_commands", [])] != [
    "version", "observe", "validate", "summarize", "compare"
]:
    raise SystemExit("error: public command manifest is incomplete or reordered")
PY

echo "[4/8] validating Plugin and Skill metadata"
[[ -f "$plugin_validator" ]] || die "Plugin validator is unavailable"
[[ -f "$skill_validator" ]] || die "Skill validator is unavailable"
"$python" "$plugin_validator" "$repo_root"
"$python" "$skill_validator" "$repo_root/skills/eval"

echo "[5/8] checking Go formatting, tests, race behavior, and vet"
unformatted="$(gofmt -l "$repo_root/cmd" "$repo_root/internal" "$repo_root/schemas")"
[[ -z "$unformatted" ]] || die "gofmt required:\n$unformatted"
go -C "$repo_root" test ./...
go -C "$repo_root" test -race ./...
go -C "$repo_root" vet ./...

echo "[6/8] cross-building Darwin, Linux, and Windows binaries"
for target in darwin/amd64 linux/amd64 windows/amd64; do
  target_os="${target%/*}"
  target_arch="${target#*/}"
  suffix=""
  [[ "$target_os" == "windows" ]] && suffix=".exe"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go -C "$repo_root" build -trimpath -o "$temp_dir/evalctl-$target_os-$target_arch$suffix" ./cmd/evalctl
done

echo "[7/8] checking shell syntax and repository whitespace"
bash -n "$repo_root/scripts/verify.sh"
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

echo "[8/8] verification complete"
echo "Eval verification passed: ${#valid_fixtures[@]} valid fixture sets and $invalid_count invalid fixtures checked."
