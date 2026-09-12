#!/usr/bin/env python3
"""External, bounded coding pilot. Eval itself never executes these workers."""

import argparse
import hashlib
import json
import os
import platform
import re
import selectors
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
import tomllib
from pathlib import Path

HERE = Path(__file__).resolve().parent
CASES = ("pagination", "quantity", "dedup")
GO_MOD = b"module pilotcase\n\ngo 1.25.0\n"
COMMON = """Implement the requirements in REQUIREMENTS.md. Read the provided code and public tests. Change only task.go. Do not change the public tests, requirements, module file, or these instructions. Use only the Go standard library. Do not use the network. This is a small Eval development fixture; do not collect Eval observations. Finish by briefly describing the change.\n"""
CANDIDATE = """Before modifying code, reproduce a failing case or otherwise demonstrate the defect. Make the smallest change that satisfies the requirements. Before finishing, run the relevant tests against the final code and report their outcome.\n"""
AGENTS = b"This workspace is a fixed Eval development fixture. Follow REQUIREMENTS.md. Only task.go may change. Do not access files outside this workspace to discover solutions or hidden tests.\n"
MAX_FILE = 16 << 20
MAX_FILES = 2048
MAX_EVENT = 1 << 20
SAFE_PATH = re.compile(r"^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$")
TOOL_KINDS = {"command_execution": "shell", "file_change": "file_change", "mcp_tool_call": "mcp", "web_search": "web", "plan": "plan"}
ENVIRONMENT_REASONS = {"model_unavailable", "reasoning_unsupported", "api_transport_error", "api_rate_limited", "api_server_error", "api_permission_denied", "tool_startup_error", "startup_failure_unknown"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=True, indent=2).encode() + b"\n"


def config_path():
    return Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex"))) / "config.toml"


def config_fingerprints(path):
    """Hash all settings, including notice/security keys; retain no values."""
    content = read_regular(path) if path.is_file() else b""
    try:
        parsed = tomllib.loads(content.decode())
    except (ValueError, UnicodeError):
        raise ValueError("configuration_parse_failed") from None

    def normalize(value):
        if isinstance(value, dict):
            return {key: normalize(child) for key, child in value.items()}
        if isinstance(value, list):
            return [normalize(child) for child in value]
        # TOML date/time objects have stable ISO representations.
        if hasattr(value, "isoformat"):
            return {"toml_type": type(value).__name__, "iso": value.isoformat()}
        return value

    normalized = normalize(parsed)
    return {"byte_digest": sha(content), "canonical_digest": sha(canonical(normalized)),
            "top_level_digests": {key: sha(canonical(value)) for key, value in sorted(normalized.items())}}


def catalog_capability(data, model, reasoning):
    if not isinstance(data, dict) or not isinstance(data.get("models"), list):
        raise ValueError("model_catalog_invalid")
    found = [item for item in data["models"] if isinstance(item, dict) and item.get("slug") == model]
    if len(found) != 1:
        raise ValueError("requested_model_unavailable")
    levels = found[0].get("supported_reasoning_levels")
    if not isinstance(levels, list):
        raise ValueError("model_catalog_invalid")
    efforts = sorted({level.get("effort") for level in levels if isinstance(level, dict) and isinstance(level.get("effort"), str)})
    if reasoning not in efforts:
        raise ValueError("requested_reasoning_unsupported")
    return {"model": model, "reasoning": reasoning, "supported_reasoning_efforts": efforts,
            "catalog_model_count": len(data["models"]), "source": "codex_debug_models_refresh"}


def failure_reason(data):
    """Classify transient error content into a fixed, private reason enum."""
    text = json.dumps(data).lower() if not isinstance(data, str) else data.lower()
    if any(word in text for word in ("unauthorized", "authentication", "not logged in", "invalid api key", '"status": 401', '"status_code": 401')):
        return "authentication_failed"
    if ("reasoning" in text or "effort" in text) and any(word in text for word in ("unsupported", "not supported", "invalid")):
        return "reasoning_unsupported"
    if "model" in text and any(word in text for word in ("model_not_found", "unsupported_model", "does not exist", "not found", "not available", "unavailable", "not supported")):
        return "model_unavailable"
    if any(word in text for word in ("rate_limit", "rate limit", '"status": 429', '"status_code": 429')):
        return "api_rate_limited"
    if any(word in text for word in ("error sending request", "connection refused", "connection reset", "dns error", "failed to lookup address", "stream disconnected", "network is unreachable", "connect timeout")):
        return "api_transport_error"
    if re.search(r'"(?:status|status_code)"\s*:\s*5[0-9][0-9]\b', text) or "internal server error" in text:
        return "api_server_error"
    if re.search(r'"(?:status|status_code)"\s*:\s*403\b', text):
        return "api_permission_denied"
    if ("mcp" in text or "tool" in text) and any(word in text for word in ("failed to start", "startup failed", "startup error")):
        return "tool_startup_error"
    return None


def files_digest(files):
    return sha(("eval-files/v1\n" + "".join(f"{f['path']}\t{f['digest']}\n" for f in sorted(files, key=lambda f: f["path"]))).encode())


def criteria_digest(case):
    text = "eval-criteria/v1\n"
    text += "".join(f"allow\t{p}\n" for p in sorted(case["allowed_files"]))
    text += "".join(f"protect\t{p}\n" for p in sorted(case["protected_files"]))
    text += "".join(f"check\t{c['id']}\t{c['kind']}\t{c['checker_digest']}\n" for c in sorted(case["required_checks"], key=lambda c: c["id"]))
    return sha(text.encode())


def suite_digest(suite):
    text = f"eval-suite/v1\n{suite['id']}\n{suite['version']}\n"
    text += "".join(f"{c['id']}\t{c['input_digest']}\t{c['criteria_digest']}\n" for c in sorted(suite["cases"], key=lambda c: c["id"]))
    return sha(text.encode())


def initial_contents(case_id):
    case = HERE / "cases" / case_id
    return {"go.mod": GO_MOD, "task.go": (case / "initial/task.go").read_bytes(),
            "public_test.go": (case / "public/public_test.go").read_bytes(),
            "REQUIREMENTS.md": (case / "requirements.md").read_bytes(), "AGENTS.md": AGENTS}


def checker_digest(case_id, kind):
    case = HERE / "cases" / case_id
    # Bind the exact command, module, and both original test files used to compile.
    data = {"command": check_command(kind), "go_mod": GO_MOD.decode(),
            "requirements": (case / "independent/requirements_test.go").read_text(),
            "regressions": (case / "independent/regression_test.go").read_text(),
            "api_failure_countercheck": (case / "golden/task.go").read_text()}
    return sha(canonical(data))


def build_suite():
    suite = {"schema": "eval-suite/v1", "id": "go-coding-pilot", "version": "1.0.0", "cases": []}
    for case_id in CASES:
        files = [{"path": p, "digest": sha(b)} for p, b in sorted(initial_contents(case_id).items())]
        case = {"id": case_id, "input_digest": files_digest(files), "criteria_digest": "", "initial_files": files,
                "allowed_files": ["task.go"], "protected_files": ["AGENTS.md", "REQUIREMENTS.md", "go.mod", "public_test.go"],
                "required_checks": [{"id": kind, "kind": kind, "checker_digest": checker_digest(case_id, kind)} for kind in ("requirement", "regression")]}
        case["criteria_digest"] = criteria_digest(case)
        suite["cases"].append(case)
    return suite


def read_regular(path):
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_FILE:
            raise ValueError("unsupported_file")
        with os.fdopen(fd, "rb", closefd=False) as stream:
            data = stream.read(MAX_FILE + 1)
        if len(data) > MAX_FILE:
            raise ValueError("oversized_file")
        after = os.fstat(fd)
        if (info.st_ino, info.st_size, info.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
            raise ValueError("file_changed_during_read")
        return data
    finally:
        os.close(fd)


def manifest(root):
    """Inspect the workspace only; .git metadata is not a submitted artifact.

    Unsafe names/special entries yield partial coverage. Symlinks are recorded
    as such without following their target, so replacement changes the digest.
    """
    files, complete, walk_errors = [], True, []
    for parent, directories, names in os.walk(root, followlinks=False, onerror=walk_errors.append):
        directories.sort()
        if Path(parent) == root and ".git" in directories:
            directories.remove(".git")
        for name in list(directories):
            if (Path(parent) / name).is_symlink():
                directories.remove(name)
                names.append(name)
        for name in sorted(names):
            path = Path(parent) / name
            relative = path.relative_to(root).as_posix()
            if not SAFE_PATH.fullmatch(relative) or any(p in (".", "..") for p in relative.split("/")):
                complete = False
                continue
            if len(files) >= MAX_FILES:
                return files, "partial"
            try:
                if path.is_symlink():
                    digest = sha(b"eval-symlink/v1\n" + os.readlink(path).encode())
                    complete = False
                else:
                    digest = sha(read_regular(path))
                files.append({"path": relative, "digest": digest})
            except (OSError, ValueError):
                complete = False
    return sorted(files, key=lambda f: f["path"]), "complete" if complete and not walk_errors else "partial"


def write_private(path, data):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def private_root(path):
    path = Path(os.path.abspath(path))
    for ancestor in [*reversed(path.parents), path]:
        if ancestor.is_symlink():
            raise ValueError("output_symlink")
        if (ancestor / ".git").exists():
            raise ValueError("output_inside_git")
    if not path.parent.is_dir():
        raise ValueError("output_parent_missing")
    path.mkdir(mode=0o700, exist_ok=False)
    return path


def go_env():
    env = os.environ.copy()
    env.update({"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off", "GOFLAGS": ""})
    return env


def check_command(kind):
    test = "TestRequirements" if kind == "requirement" else "TestRegression"
    return ["go", "test", "-json", "-count=1", "-timeout=20s", "-run", "^" + test + "$", "."]


def run_check(case_id, source, kind):
    case = HERE / "cases" / case_id / "independent"
    try:
        tests = {name: read_regular(case / name) for name in ("requirements_test.go", "regression_test.go")}
    except (OSError, ValueError):
        return "error"
    return _run_check(case_id, source, kind, tests, diagnose_api=True)


def _run_check(case_id, source, kind, tests, *, diagnose_api):
    # The candidate's module, tests, and auxiliary source never enter this space.
    with tempfile.TemporaryDirectory(prefix="eval-independent-") as temp:
        root = Path(temp)
        (root / "go.mod").write_bytes(GO_MOD)
        (root / "task.go").write_bytes(source)
        for name, data in tests.items():
            (root / name).write_bytes(data)
        frozen, _ = manifest(root)
        try:
            process = subprocess.run(check_command(kind), cwd=root, env=go_env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=45)
        except (OSError, subprocess.TimeoutExpired):
            return "error"
        after, coverage = manifest(root)
        if coverage != "complete" or after != frozen:
            return "error"
        expected_test = "TestRequirements" if kind == "requirement" else "TestRegression"
        events = []
        for line in process.stdout.splitlines():
            try:
                event = json.loads(line)
                if isinstance(event, dict):
                    events.append(event)
            except ValueError:
                pass
        if process.returncode == 0:
            return "pass" if any(event.get("Action") == "pass" and event.get("Test") == expected_test for event in events) else "error"
        # Retain only classifications. Only compiler records for this fixture
        # can attribute a build failure to submitted code.
        output = "".join(event.get("Output", "") for event in events if isinstance(event.get("Output", ""), str))
        if "panic: test timed out" in output:
            return "error"
        if any(event.get("Action") == "fail" and event.get("Test") == expected_test for event in events):
            return "fail"
        local_targets = ("pilotcase", "pilotcase [pilotcase.test]")
        failed_targets = {event.get("ImportPath") for event in events if event.get("Action") == "build-fail" and event.get("ImportPath") in local_targets}
        compiler_output = "".join(event.get("Output", "") for event in events
                                  if event.get("Action") == "build-output" and event.get("ImportPath") in failed_targets
                                  and isinstance(event.get("Output", ""), str))
        if re.search(r"(?:^|\n)\./task\.go:[0-9]+:[0-9]+:", compiler_output):
            return "fail"
        if diagnose_api and re.search(r"(?:^|\n)\./(?:requirements|regression)_test\.go:[0-9]+:[0-9]+:", compiler_output):
            # A removed/changed API can fail at a frozen test's call or at use
            # of its return value. Require a passing golden countercheck under
            # the same fixed tests; a broken test or environment stays error.
            try:
                golden = read_regular(HERE / "cases" / case_id / "golden/task.go")
            except (OSError, ValueError):
                return "error"
            if _run_check(case_id, golden, kind, tests, diagnose_api=False) == "pass":
                return "fail"
        if any(event.get("Action") == "run" and event.get("Test") == expected_test for event in events) and "panic:" in output:
            return "fail"
        return "error"


def self_test():
    results = []
    for case_id in CASES:
        case = HERE / "cases" / case_id
        initial = (case / "initial/task.go").read_bytes()
        golden = (case / "golden/task.go").read_bytes()
        statuses = {"initial_requirement": run_check(case_id, initial, "requirement"),
                    "initial_regression": run_check(case_id, initial, "regression"),
                    "golden_requirement": run_check(case_id, golden, "requirement"),
                    "golden_regression": run_check(case_id, golden, "regression")}
        if statuses != {"initial_requirement": "fail", "initial_regression": "pass", "golden_requirement": "pass", "golden_regression": "pass"}:
            raise ValueError("fixture_self_test_failed_" + case_id)
        results.append({"case_id": case_id, **statuses})
    return results


def direct_go_test(command):
    """Recognize a direct go test invocation; do not infer from arbitrary text."""
    try:
        def tokens(text):
            lexer = shlex.shlex(text, posix=True, punctuation_chars=";&|<>()")
            lexer.whitespace_split = True
            lexer.commenters = ""
            return list(lexer)
        parts = tokens(command)
        if len(parts) >= 3 and Path(parts[0]).name in ("bash", "zsh", "sh") and parts[1] in ("-c", "-lc"):
            if len(parts) != 3:
                return False
            parts = tokens(parts[2])
        return len(parts) >= 2 and parts[0] == "go" and parts[1] == "test" and not any(p and all(c in ";&|<>()" for c in p) for p in parts)
    except ValueError:
        return False


class Normalizer:
    def __init__(self):
        self.events = []
        self.pending = {}
        self.completed = set()
        self.gaps = False
        self.terminal = None
        self.usage = None
        self.authentication_error = False
        self.thread_started = False
        self.agent_activity = False
        self.failure_reason = None

    def emit(self, kind, status="unknown", tool=None, fingerprint=None):
        if len(self.events) >= 20000:
            self.gaps = True
            return
        event = {"id": "event-" + str(len(self.events) + 1), "sequence": len(self.events) + 1,
                 "kind": kind, "provenance": "host_record", "status": status}
        if tool:
            event["tool"] = tool
        if fingerprint:
            event["fingerprint"] = fingerprint
        self.events.append(event)

    def gap(self):
        self.gaps = True
        self.emit("observation_gap")

    def feed(self, line):
        try:
            data = json.loads(line)
        except (ValueError, UnicodeError):
            self.gap()
            return
        if not isinstance(data, dict):
            self.gap()
            return
        event_type = data.get("type")
        if event_type == "thread.started":
            self.thread_started = True
        elif event_type == "turn.started":
            pass
        elif event_type == "turn.completed":
            self.terminal = "completed"
            usage = data.get("usage")
            if isinstance(usage, dict):
                self.usage = {key: value if isinstance(value, int) and not isinstance(value, bool) and value >= 0 else None for key in ("input_tokens", "output_tokens", "cached_input_tokens") for value in [usage.get(key)]}
        elif event_type in ("turn.failed", "error"):
            self.terminal = "agent_error"
            reason = failure_reason(data)
            if reason:
                self.failure_reason = reason
            if reason == "authentication_failed":
                self.authentication_error = True
        elif event_type in ("item.started", "item.updated", "item.completed"):
            item = data.get("item")
            if not isinstance(item, dict):
                self.gap()
                return
            item_type, item_id = item.get("type"), item.get("id")
            if item_type in ("agent_message", "reasoning"):
                self.agent_activity = True
                return
            if item_type not in TOOL_KINDS or not isinstance(item_id, str):
                self.gap()
                return
            self.agent_activity = True
            if event_type != "item.completed":
                previous = self.pending.get(item_id, {})
                self.pending[item_id] = {"type": item_type, "command": item.get("command", previous.get("command"))}
                return
            if item_id in self.completed:
                self.gap()
                return
            self.completed.add(item_id)
            pending = self.pending.pop(item_id, {})
            command = item.get("command", pending.get("command"))
            fingerprint = sha(command.encode()) if isinstance(command, str) else None
            status = "unknown"
            if item_type == "command_execution":
                code = item.get("exit_code")
                if isinstance(code, int) and not isinstance(code, bool):
                    status = "succeeded" if code == 0 else "failed"
            elif item.get("status") in ("completed", "succeeded"):
                status = "succeeded"
            elif item.get("status") == "failed":
                status = "failed"
            if status == "unknown":
                self.gaps = True
            self.emit("tool_call", status, TOOL_KINDS[item_type], fingerprint)
            if item_type == "command_execution" and isinstance(command, str) and direct_go_test(command):
                self.emit("verification", status, "shell", fingerprint)
        else:
            self.gap()

    def finish(self):
        for item in self.pending.values():
            command = item.get("command")
            self.emit("tool_call", "unknown", TOOL_KINDS[item["type"]], sha(command.encode()) if isinstance(command, str) else None)
        if self.pending:
            self.gaps = True
        self.pending.clear()


def terminate_group(process):
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=5)


def execute_worker(command, workspace, prompt, timeout):
    normalizer = Normalizer()
    started = time.monotonic()
    try:
        process = subprocess.Popen(command, cwd=workspace, env=go_env(), stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    except OSError:
        normalizer.failure_reason = "process_start_failed"
        return normalizer, "environment_error", int((time.monotonic() - started) * 1000)
    try:
        process.stdin.write(prompt.encode())
        process.stdin.close()
    except BrokenPipeError:
        pass
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ, "stdout")
    selector.register(process.stderr, selectors.EVENT_READ, "stderr")
    buffer = b""
    dropping = False
    timeout_reached = False
    interrupted = False
    try:
        while selector.get_map():
            if time.monotonic() - started > timeout:
                timeout_reached = True
                terminate_group(process)
                break
            for key, _ in selector.select(timeout=0.1):
                data = os.read(key.fileobj.fileno(), 65536)
                if not data:
                    selector.unregister(key.fileobj)
                    continue
                if key.data == "stderr":
                    reason = failure_reason(data.decode(errors="replace"))
                    if reason:
                        normalizer.failure_reason = reason
                    if reason == "authentication_failed":
                        normalizer.authentication_error = True
                    continue
                buffer += data
                while b"\n" in buffer:
                    line, buffer = buffer.split(b"\n", 1)
                    if not dropping and len(line) <= MAX_EVENT:
                        normalizer.feed(line)
                    else:
                        normalizer.gap()
                    dropping = False
                if len(buffer) > MAX_EVENT:
                    buffer = b""
                    dropping = True
        if buffer and not timeout_reached and not dropping:
            normalizer.feed(buffer)
        if dropping:
            normalizer.gap()
        if process.poll() is None:
            remaining = max(0.01, timeout - (time.monotonic() - started))
            try:
                process.wait(timeout=remaining)
            except subprocess.TimeoutExpired:
                timeout_reached = True
                terminate_group(process)
    except KeyboardInterrupt:
        interrupted = True
        terminate_group(process)
    except BaseException:
        terminate_group(process)
        raise
    finally:
        selector.close()
        process.stdout.close()
        process.stderr.close()
    duration = int((time.monotonic() - started) * 1000)
    normalizer.finish()
    if interrupted:
        termination = "interrupted"
        normalizer.failure_reason = "controller_interrupted"
    elif timeout_reached:
        termination = "timeout"
        normalizer.failure_reason = "controller_timeout"
    elif normalizer.authentication_error:
        termination = "authentication_error"
    elif normalizer.terminal == "completed" and process.returncode == 0:
        termination = "completed"
        normalizer.failure_reason = None
    elif process.returncode is not None and process.returncode < 0:
        termination = "interrupted"
        normalizer.failure_reason = "process_signaled"
    elif normalizer.failure_reason in ENVIRONMENT_REASONS:
        termination = "environment_error"
    elif not normalizer.agent_activity:
        termination = "environment_error"
        normalizer.failure_reason = normalizer.failure_reason or "startup_failure_unknown"
    else:
        termination = "agent_error"
        normalizer.failure_reason = normalizer.failure_reason or "agent_turn_failed"
    return normalizer, termination, duration


def preflight(codex, model="gpt-5.5", reasoning="xhigh", include_fixtures=True):
    if os.name != "posix":
        raise ValueError("pilot_requires_posix")
    executable = shutil.which(codex)
    if not executable:
        raise ValueError("codex_missing")
    for program in ("git", "go"):
        if not shutil.which(program):
            raise ValueError(program + "_missing")
    version = subprocess.run([executable, "--version"], capture_output=True, timeout=15, check=True).stdout.decode().strip()
    go_version = subprocess.run(["go", "version"], env=go_env(), capture_output=True, timeout=15, check=True).stdout.decode().strip()
    login = subprocess.run([executable, "login", "status"], capture_output=True, timeout=15)
    if login.returncode != 0:
        raise ValueError("authentication_preflight_failed")
    catalog = subprocess.run([executable, "debug", "models"], capture_output=True, timeout=45)
    if catalog.returncode != 0:
        raise ValueError("model_catalog_preflight_failed")
    try:
        capability = catalog_capability(json.loads(catalog.stdout), model, reasoning)
    except (UnicodeError, json.JSONDecodeError):
        raise ValueError("model_catalog_invalid") from None
    result = {"status": "ready", "codex_version": version, "go_version": go_version,
              "authentication": "login_status_succeeded", "codex_digest": sha(Path(executable).read_bytes()),
              "harness_digest": sha(Path(__file__).read_bytes()), "platform": platform.system(), "architecture": platform.machine(),
              "model_capability": capability, "configuration": config_fingerprints(config_path()),
              "permission_profile": {"requested": "workspace-write", "effective": "unavailable", "verification": "requested_cli_flag_not_host_attestation"}}
    if include_fixtures:
        result["fixtures"] = self_test()
    return result


def attempt_record(case, condition, root, codex, model, reasoning, timeout):
    attempt_id = condition + "-" + case["id"] + "-1"
    workspace = root / "workers" / attempt_id
    workspace.mkdir(mode=0o700, parents=True)
    for name, data in initial_contents(case["id"]).items():
        write_private(workspace / name, data)
    subprocess.run(["git", "init", "--quiet"], cwd=workspace, capture_output=True, check=True)
    prompt = COMMON + (CANDIDATE if condition == "candidate" else "")
    command = [codex, "exec", "--json", "--ephemeral", "--sandbox", "workspace-write", "-m", model,
               "-c", 'model_reasoning_effort="' + reasoning + '"', "-"]
    normalizer, termination, duration = execute_worker(command, workspace, prompt, timeout)
    files, coverage = manifest(workspace)
    artifact_digest = files_digest(files)
    checks = []
    retained = root / "evidence" / attempt_id
    retained.mkdir(mode=0o700, parents=True)
    try:
        source = read_regular(workspace / "task.go")
        write_private(retained / "task.go", source)
    except (OSError, ValueError):
        source = None
    for criterion in case["required_checks"]:
        before_files, before_coverage = manifest(workspace)
        observed_source = next((item["digest"] for item in before_files if item["path"] == "task.go"), None)
        if source is None:
            status = "unavailable"
        elif observed_source != sha(source):
            status = "error"
        else:
            status = run_check(case["id"], source, criterion["kind"])
        after_files, after_coverage = manifest(workspace)
        if before_coverage != "complete" or after_coverage != "complete":
            status = "unavailable"
        checks.append({"id": criterion["id"], "evidence_id": attempt_id + "-" + criterion["id"],
                       "provenance": "independent_check", "executor": "independent", "checker_digest": criterion["checker_digest"],
                       "artifact_digest": files_digest(before_files), "artifact_after_digest": files_digest(after_files), "status": status})
    usage = normalizer.usage or {}
    attempt = {"id": attempt_id, "case_id": case["id"], "termination": termination,
               "artifact_digest": artifact_digest, "files": files, "manifest_provenance": "host_record",
               "manifest_evidence_id": attempt_id + "-manifest", "coverage": {"manifest": coverage, "tools": ("partial" if normalizer.gaps else "complete") if normalizer.thread_started else "unavailable", "permissions": "unavailable"},
               "checks": checks, "events": normalizer.events,
               "measurements": {"provenance": "host_record", "duration_ms": duration,
                                "input_tokens": usage.get("input_tokens"), "output_tokens": usage.get("output_tokens"), "cached_input_tokens": usage.get("cached_input_tokens")}}
    write_private(retained / "attempt.json", canonical(attempt))
    write_private(retained / "runner.json", canonical({"schema": "eval-pilot-attempt/v1", "attempt_id": attempt_id,
                                                        "termination": termination, "failure_reason": normalizer.failure_reason,
                                                        "agent_activity_observed": normalizer.agent_activity,
                                                        "thread_started_observed": normalizer.thread_started,
                                                        "permission_profile": {"requested": "workspace-write", "effective": "unavailable"}}))
    return attempt


def run_pilot(args):
    global HERE
    setup = preflight(args.codex, args.model, args.reasoning, include_fixtures=False)
    root = private_root(args.out)
    original_here = HERE
    snapshot_files = []
    for path in sorted(original_here.rglob("*")):
        if path.is_file() and (path.suffix in (".py", ".go", ".md") or path.name == "go.mod") and "__pycache__" not in path.parts:
            relative = path.relative_to(original_here)
            data = read_regular(path)
            write_private(root / "snapshot" / relative, data)
            snapshot_files.append({"path": relative.as_posix(), "digest": sha(data)})
    HERE = root / "snapshot"
    setup["snapshot_digest"] = files_digest(snapshot_files)
    setup["fixtures"] = self_test()
    suite = build_suite()
    write_private(root / "preflight.json", canonical(setup))
    write_private(root / "suite.json", canonical(suite))
    write_private(root / "prompts.json", canonical({"baseline": COMMON, "candidate": COMMON + CANDIDATE}))
    # Configuration bytes are hashed, never copied or printed. Both conditions
    # inherit the same local configuration, and changes abort remaining runs.
    configuration_path = config_path()
    configuration = config_fingerprints(configuration_path)
    config_digest = configuration["byte_digest"]
    env_facts = {key: setup[key] for key in ("go_version", "platform", "architecture")}
    env_facts.update({"codex_config_digest": config_digest, "configuration": configuration,
                      "requested_permission_profile": "workspace-write", "effective_permission_profile": "unavailable",
                      "offline_go_env": {key: go_env()[key] for key in ("GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOWORK", "GOFLAGS")}, "timeout_seconds": args.timeout})
    environment = {"model": args.model, "reasoning": args.reasoning, "permission_profile": "workspace-write",
                   "environment_digest": sha(canonical(env_facts)), "tool_digest": sha(canonical({key: setup[key] for key in ("codex_version", "codex_digest", "harness_digest")}))}
    write_private(root / "environment.json", canonical({"environment": environment, "facts": env_facts}))
    sets = {condition: {"schema": "eval-attempts/v1", "suite_id": suite["id"], "suite_version": suite["version"], "suite_digest": suite_digest(suite),
                        "condition": {"id": condition, "instruction_digest": sha((COMMON + (CANDIDATE if condition == "candidate" else "")).encode())},
                        "environment": environment, "attempts": []} for condition in ("baseline", "candidate")}
    abort_reason = None
    try:
        for case in suite["cases"]:
            for condition in ("baseline", "candidate"):
                current = config_fingerprints(configuration_path)
                if current["byte_digest"] != config_digest:
                    write_private(root / "configuration-drift.json", canonical({"before": configuration, "after": current,
                                                                                "canonical_changed": configuration["canonical_digest"] != current["canonical_digest"]}))
                    raise ValueError("configuration_changed_no_retry")
                print(json.dumps({"status": "starting", "case_id": case["id"], "condition": condition}), flush=True)
                attempt = attempt_record(case, condition, root, args.codex, args.model, args.reasoning, args.timeout)
                sets[condition]["attempts"].append(attempt)
                print(json.dumps({"status": "finished", "case_id": case["id"], "condition": condition, "termination": attempt["termination"], "checks": {check["id"]: check["status"] for check in attempt["checks"]}}), flush=True)
                if attempt["termination"] == "interrupted":
                    raise ValueError("run_interrupted_no_retry")
    except KeyboardInterrupt:
        abort_reason = "run_interrupted_no_retry"
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        abort_reason = str(error) if isinstance(error, ValueError) and re.fullmatch(r"[a-z0-9_]+", str(error)) else type(error).__name__
    finally:
        for condition, attempts in sets.items():
            write_private(root / (condition + "-attempts.json"), canonical(attempts))
        attempted = sum(len(v["attempts"]) for v in sets.values())
        write_private(root / "run.json", canonical({"schema": "eval-pilot-run/v1", "planned": 6, "attempted": attempted, "missing": 6 - attempted,
                                                   "abort_reason": abort_reason, "automatic_retries": 0, "evidence": "normalized_local_producer_claims_not_authenticated_host_attestation",
                                                   "permissions": "unavailable_no_explicit_host_permission_events", "scope": "workspace_manifest_excluding_git_metadata",
                                                   "interpretation": "small_functionality_pilot_no_general_strategy_claim"}))
        HERE = original_here
    if abort_reason:
        raise ValueError(abort_reason)
    print(json.dumps({"status": "finished", "planned": 6, "attempted": attempted}), flush=True)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("self-test")
    pre = commands.add_parser("preflight")
    pre.add_argument("--codex", default="codex")
    pre.add_argument("--model", default="gpt-5.5")
    pre.add_argument("--reasoning", default="xhigh")
    run = commands.add_parser("run")
    run.add_argument("--out", required=True)
    run.add_argument("--codex", default="codex")
    run.add_argument("--model", default="gpt-5.5")
    run.add_argument("--reasoning", choices=("low", "medium", "high", "xhigh", "max", "ultra"), default="xhigh")
    run.add_argument("--timeout", type=int, default=300)
    args = parser.parse_args()
    if args.command == "run" and (args.timeout <= 0 or args.timeout > 300 or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}", args.model)):
        parser.error("model must be a token and timeout must be between 1 and 300 seconds")
    try:
        if args.command == "self-test":
            print(json.dumps({"status": "passed", "cases": self_test()}, sort_keys=True))
        elif args.command == "preflight":
            print(json.dumps(preflight(args.codex, args.model, args.reasoning), sort_keys=True))
        else:
            run_pilot(args)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        # Error messages may contain machine paths or command output. Preserve
        # bounded, controlled stage codes only.
        reason = str(error) if isinstance(error, ValueError) and re.fullmatch(r"[a-z0-9_]+", str(error)) else type(error).__name__
        print(json.dumps({"status": "error", "reason": reason}), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
