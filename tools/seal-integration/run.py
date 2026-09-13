#!/usr/bin/env python3
"""Exercise the real Seal export and Eval collection CLIs in disposable state."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


SEAL_SUPPORT_COMMIT = "11f6a304064be475fec75fe815bdcce85ae8a973"


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def write_json(path, value):
    path.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8"))


def refresh_fixture_manifest(directory):
    """Rehash deliberately modified fixture bytes, never real user Evidence."""
    path = directory / "run-manifest.json"
    manifest = read_json(path)
    manifest["run_id"] = directory.name
    for entry in manifest["files"]:
        data = (directory / entry["path"]).read_bytes()
        entry.update(size_bytes=len(data), sha256=hashlib.sha256(data).hexdigest())
    payload = {key: manifest[key] for key in ("schema_version", "task_id", "run_id", "files")}
    canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    manifest["evidence_sha256"] = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    write_json(path, manifest)


class Integration:
    def __init__(self, base, seal, evalctl):
        self.base, self.seal, self.evalctl = base, seal, evalctl
        self.repo = base / "repository"
        self.state = base / "state"
        self.repo.mkdir(mode=0o700)
        # Prevent ambient Git hooks, signing and config from affecting fixture work.
        self.env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        self.command(["git", "init", "--quiet"])
        self.command(["git", "config", "core.hooksPath", str(base / "no-hooks")])
        (self.repo / ".gitignore").write_text(".seal/tasks/\n.seal/evidence/\n", encoding="utf-8")
        (self.repo / ".seal").mkdir(mode=0o700)
        write_json(self.repo / ".seal/checks.json", {"schema_version": 1, "checks": []})
        self.command(["git", "add", ".gitignore", ".seal/checks.json"])
        self.command(["git", "-c", "user.name=Integration Fixture", "-c",
                      "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
                      "commit", "--quiet", "-m", "fixture baseline"])
        config = base / "config.json"
        write_json(config, {"schema_version": 1, "repositories": [str(self.repo)],
                            "session_dirs": [], "ward_dirs": [], "seal_binary": str(seal)})
        result = self.eval("experiment", "init", "--config", str(config))
        self.experiment = result["experiment_id"]
        self.ledger = self.state / "jgoneit/eval-experiment/v2" / self.experiment

    def command(self, argv, code=0):
        result = subprocess.run([str(part) for part in argv], cwd=self.repo, env=self.env,
                                capture_output=True, text=True, timeout=60)
        require(result.returncode == code,
                f"{argv[0]} {argv[1:]} exited {result.returncode}, expected {code}: "
                f"{result.stdout}\n{result.stderr}")
        return result.stdout

    def seal_json(self, *args, code=0):
        return json.loads(self.command([self.seal, *args], code))

    def eval(self, *args, code=0):
        return json.loads(self.command([self.evalctl, *args, "--state-root", self.state], code))

    def collect(self, added=0, issues=()):
        result = self.eval("collect", "--experiment", self.experiment, code=2 if issues else 0)
        require(result["committed"], f"collection was not persisted: {result}")
        require(result["durability"] == "confirmed", f"collection durability was not confirmed: {result}")
        require(result["events_added"] == added, f"unexpected event count: {result}")
        require(result["complete"] == (not issues), f"unexpected completion status: {result}")
        require({issue["code"] for issue in result["issues"]} == set(issues),
                f"unexpected issues: {result}")
        return result

    def events(self):
        batches = [json.loads(line) for line in (self.ledger / "journal.jsonl").read_text().splitlines()]
        return [event for batch in batches for event in batch.get("events", [])]

    def source(self, task_id, run_id):
        key = f"{self.repo}\0{task_id}\0{run_id}"
        batches = [json.loads(line) for line in (self.ledger / "private.jsonl").read_text().splitlines()]
        return next(binding["id"] for batch in batches for binding in batch.get("bindings", [])
                    if binding["kind"] == "seal_run" and binding["key"] == key)

    def snapshots(self, task_id, run_id):
        source = self.source(task_id, run_id)
        return [event for event in self.events() if event["source_id"] == source]

    def task(self, task_id, exit_code, verify=True):
        spec = self.base / f"{task_id}.json"
        write_json(spec, {"schema_version": 1, "id": task_id, "type": "test",
                          "objective": "Disposable CLI integration fixture", "scope": ["."],
                          "checks": [{"name": "fixture", "required": True,
                                      "argv": [sys.executable, "-c", f"raise SystemExit({exit_code})"]}],
                          "risk": "low", "verifier": {"required": False}})
        self.seal_json("task", "create", "--file", str(spec))
        if verify:
            return self.seal_json("verify", task_id)["run_id"]

    def exported(self, code=0):
        result = self.seal_json("run", "export", "--format", "json", code=code)
        require(result["schema"] == "seal-run-export/v1", "wrong export contract")
        return result

    def repeat_and_reopen(self, issues=()):
        before = self.events()
        result = self.collect(issues=issues)
        require(result["duplicates"] == len({event["source_id"] for event in before}),
                f"unchanged sources were not deduplicated: {result}")
        # A fresh process must load and validate the persisted ledger, including numbers.
        self.eval("report", "--experiment", self.experiment, "--format", "json")
        require(self.events() == before, "reopening or repeat collection changed event facts")

    def run(self):
        passed = self.task("TASK-PASS", 0)
        failed = self.task("TASK-FAIL", 23)
        self.task("TASK-INVENTORY-ONLY", 0, verify=False)
        export = self.exported()
        require(export["scan_complete"] and not export["issues"], "ordinary export was incomplete")
        require({task["task_id"] for task in export["tasks"]} ==
                {"TASK-PASS", "TASK-FAIL", "TASK-INVENTORY-ONLY"}, "Task inventory was lost")
        runs = {run["task_id"]: run for run in export["runs"]}
        for task, expected, exit_code in (("TASK-PASS", "pass", 0), ("TASK-FAIL", "fail", 23)):
            require(runs[task]["mechanical_result"] == expected, "wrong mechanical result")
            require(runs[task]["checks"][0]["exit_code"] == exit_code, "wrong real process exit code")
            require(runs[task]["run_version"] is None, "exporter invented the producer version")

        broken = self.repo / ".seal/evidence/TASK-PASS/broken-run"
        broken.mkdir()
        partial = self.exported(code=8)
        require(not partial["scan_complete"] and len(partial["runs"]) == 2,
                "partial export discarded intact Runs")
        partial_issues = ("seal_export_incomplete", "seal_invalid_evidence")
        self.collect(added=2, issues=partial_issues)
        require({event["outcome"] for event in self.events()} == {"pass", "fail"},
                "partial collection lost the passing or failed Run")
        require(all(event["version"] == "unknown" for event in self.events()),
                "Eval substituted reader version for producer version")
        self.repeat_and_reopen(issues=partial_issues)
        broken.rmdir()
        self.repeat_and_reopen()
        print("PASS real pass/fail Runs, Task inventory, partial export and idempotent collection")

        original_pass = self.snapshots("TASK-PASS", passed)[0]
        self.seal_json("complete", "TASK-PASS", "--run-id", passed)
        self.collect(added=1)
        completed = self.snapshots("TASK-PASS", passed)
        require(len(completed) == 2 and completed[-1]["revision"] == 2,
                "Completion did not append one revision")
        require(completed[-1]["evidence_sha256"] == original_pass["evidence_sha256"],
                "Completion changed the Evidence digest")
        require(completed[-1]["seal"]["completion_record"]["state"] == "recorded_pass",
                "Completion was not retained")
        self.repeat_and_reopen()
        print("PASS real Completion appends one same-digest revision")

        original = self.repo / ".seal/evidence/TASK-FAIL" / failed
        historical = (("historical-positive", 9223372036854775808),
                      ("historical-negative", -9223372036854775809))
        # Fabricated historical-format Evidence tests a compatibility boundary.
        # These numbers are not claimed to be exit statuses from real OS processes.
        for run_id, value in historical:
            directory = original.parent / run_id
            shutil.copytree(original, directory)
            verification = read_json(directory / "verification.json")
            verification["run_id"] = run_id
            write_json(directory / "verification.json", verification)
            checks = read_json(directory / "checks.json")
            checks["checks"][0]["exit_code"] = value
            write_json(directory / "checks.json", checks)
            refresh_fixture_manifest(directory)
            self.seal_json("run", "show", "TASK-FAIL", "--run-id", run_id)
        export = {run["run_id"]: run for run in self.exported()["runs"]}
        for run_id, value in historical:
            actual = export[run_id]["checks"][0]["exit_code"]
            require(type(actual) is int and actual == value, "Seal truncated a historical integer")
        self.collect(added=2)
        self.repeat_and_reopen()
        for run_id, value in historical:
            snapshots = self.snapshots("TASK-FAIL", run_id)
            actual = snapshots[0]["seal"]["checks"][0]["exit_code"]
            require(len(snapshots) == 1 and type(actual) is int and actual == value,
                    "Eval lost an exact historical integer across storage and reopen")
        print("PASS fabricated historical integers beyond int64 remain exact after collection and reopen")

        # Deliberately replace fixture bytes under an already collected identity.
        # The Run remains locally manifest-valid, but Eval must retain prior facts.
        before = self.events()
        old_digest = self.snapshots("TASK-FAIL", failed)[0]["evidence_sha256"]
        check = read_json(original / "checks.json")["checks"][0]
        with (original / check["stdout_path"]).open("ab") as stream:
            stream.write(b"deliberately replaced fixture bytes\n")
        refresh_fixture_manifest(original)
        changed = next(run for run in self.exported()["runs"] if run["run_id"] == failed)
        require(changed["evidence_sha256"] != old_digest, "fixture digest did not change")
        for _ in range(2):
            self.collect(issues=("seal_evidence_conflict",))
            report = self.eval("report", "--experiment", self.experiment, "--format", "json")
            require(self.events() == before, "conflicting replacement revised prior facts")
        print("PASS same-identity digest replacement reports conflict and retains prior facts")
        public = (self.ledger / "journal.jsonl").read_text() + json.dumps(report)
        for private_value in (str(self.repo), "TASK-PASS", "TASK-FAIL", "TASK-INVENTORY-ONLY",
                              passed, failed, "broken-run", *(run_id for run_id, _ in historical)):
            require(private_value not in public, "original identity or repository path leaked into facts/report")
        print("PASS facts and report exclude private repository, Task and Run identities")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seal", type=Path, required=True, help="absolute path to the built Seal binary")
    parser.add_argument("--evalctl", type=Path, required=True, help="absolute path to the built Eval binary")
    args = parser.parse_args()
    for binary in (args.seal, args.evalctl):
        if not binary.is_absolute() or not binary.is_file():
            parser.error(f"binary must be an existing absolute file: {binary}")
    # TemporaryDirectory and restrictive umask keep all fixtures private and disposable.
    previous_umask = os.umask(0o077)
    try:
        with tempfile.TemporaryDirectory(prefix="eval-seal-integration-") as directory:
            Integration(Path(directory).resolve(), args.seal.resolve(), args.evalctl.resolve()).run()
    finally:
        os.umask(previous_umask)
    print(f"Seal export integration passed (CI support pin: {SEAL_SUPPORT_COMMIT})")


if __name__ == "__main__":
    main()
