import json
import os
import subprocess
import sys
import tempfile
import unittest
from argparse import Namespace
from pathlib import Path
from unittest import mock

import run


class NormalizerTests(unittest.TestCase):
    def test_private_raw_text_is_not_retained(self):
        n = run.Normalizer()
        command = 'go test -run CANARY_PRIVATE_COMMAND'
        records = [
            {"type": "thread.started", "thread_id": "/CANARY_PRIVATE_THREAD"},
            {"type": "item.completed", "item": {"id": "message", "type": "agent_message", "text": "CANARY_PRIVATE_MESSAGE"}},
            {"type": "item.completed", "item": {"id": "reasoning", "type": "reasoning", "text": "CANARY_PRIVATE_REASONING"}},
            {"type": "item.started", "item": {"id": "cmd", "type": "command_execution", "command": command}},
            {"type": "item.completed", "item": {"id": "cmd", "type": "command_execution", "command": command, "exit_code": 1, "aggregated_output": "CANARY_PRIVATE_OUTPUT"}},
            {"type": "item.completed", "item": {"id": "edit", "type": "file_change", "status": "completed", "changes": [{"path": "/CANARY_PRIVATE_PATH", "kind": "update"}]}},
            {"type": "turn.completed", "usage": {"input_tokens": 10, "output_tokens": 0, "cached_input_tokens": 5}},
        ]
        for record in records:
            n.feed(json.dumps(record))
        n.finish()
        self.assertNotIn("CANARY", json.dumps({"events": n.events, "usage": n.usage, "pending": n.pending}))
        self.assertEqual([e["kind"] for e in n.events], ["tool_call", "verification", "tool_call"])
        self.assertEqual(n.events[0]["status"], "failed")
        self.assertEqual(n.events[0]["fingerprint"], run.sha(command.encode()))
        self.assertEqual(n.usage["output_tokens"], 0)

    def test_unknown_and_missing_are_never_zero_or_permission_success(self):
        n = run.Normalizer()
        n.feed('{"type":"future_permission_event","decision":"allow"}')
        n.feed('not json')
        n.feed('{"type":"turn.completed","usage":{"input_tokens":-1,"output_tokens":true}}')
        self.assertTrue(n.gaps)
        self.assertTrue(all(e["kind"] == "observation_gap" for e in n.events))
        self.assertEqual(n.usage, {"input_tokens": None, "output_tokens": None, "cached_input_tokens": None})

    def test_incomplete_tool_and_duplicate_terminal(self):
        n = run.Normalizer()
        n.feed('{"type":"item.started","item":{"id":"one","type":"command_execution","command":"go test ./..."}}')
        n.finish()
        self.assertTrue(n.gaps)
        self.assertEqual(n.events[0]["status"], "unknown")
        n = run.Normalizer()
        item = '{"type":"item.completed","item":{"id":"one","type":"command_execution","command":"go test ./...","exit_code":0}}'
        n.feed(item)
        n.feed(item)
        self.assertEqual(sum(e["kind"] == "tool_call" for e in n.events), 1)
        self.assertTrue(n.gaps)

    def test_verification_requires_direct_invocation(self):
        self.assertTrue(run.direct_go_test("go test ./..."))
        self.assertTrue(run.direct_go_test("/bin/zsh -lc 'go test ./...'"))
        for command in ("echo go test", "false && go test ./...", "go test ./... || true", "cat 'go test'", "go test ; echo ignored", "go test ./...; echo ignored"):
            self.assertFalse(run.direct_go_test(command))


class ManifestTests(unittest.TestCase):
    def test_manifest_tracks_protected_extra_and_deleted_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "task.go").write_text("before")
            (root / "public_test.go").write_text("frozen")
            (root / ".git").mkdir()
            (root / ".git/index").write_text("ignored")
            original, coverage = run.manifest(root)
            self.assertEqual(coverage, "complete")
            (root / "public_test.go").write_text("tampered")
            (root / "extra.go").write_text("extra")
            (root / "task.go").unlink()
            after, coverage = run.manifest(root)
            self.assertNotEqual(run.files_digest(original), run.files_digest(after))
            self.assertEqual({f["path"] for f in after}, {"public_test.go", "extra.go"})
            self.assertEqual(coverage, "complete")

    def test_symlink_is_never_followed_and_unsafe_name_is_partial(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            secret = root / "secret"
            secret.write_text("CANARY")
            workspace = root / "workspace"
            workspace.mkdir()
            (workspace / "task.go").symlink_to(secret)
            files, coverage = run.manifest(workspace)
            self.assertEqual(coverage, "partial")
            self.assertNotEqual(files[0]["digest"], run.sha(b"CANARY"))
            with self.assertRaises(OSError):
                run.read_regular(workspace / "task.go")
            (workspace / "bad name").write_text("bad")
            self.assertEqual(run.manifest(workspace)[1], "partial")

    def test_output_must_be_new_and_outside_git(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp).resolve()
            root = run.private_root(parent / "new")
            self.assertEqual(root.stat().st_mode & 0o777, 0o700)
            run.write_private(root / "record.json", b"{}")
            self.assertEqual((root / "record.json").stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                run.private_root(root)
            with self.assertRaises(FileExistsError):
                run.write_private(root / "record.json", b"overwritten")
            (parent / ".git").mkdir()
            with self.assertRaisesRegex(ValueError, "output_inside_git"):
                run.private_root(parent / "other")


@unittest.skipUnless(os.name == "posix", "pilot worker requires POSIX process groups")
class ExecutionTests(unittest.TestCase):
    def worker(self, program, timeout=3):
        with tempfile.TemporaryDirectory() as temp:
            return run.execute_worker([sys.executable, "-c", program], Path(temp), "prompt", timeout)

    def test_process_exit_is_not_agent_completion(self):
        n, termination, duration = self.worker('import sys; sys.stdin.read(); print(\'{"type":"thread.started"}\')')
        self.assertEqual(termination, "environment_error")
        self.assertEqual(n.failure_reason, "startup_failure_unknown")
        self.assertGreaterEqual(duration, 0)
        self.assertIsNone(n.usage)

    def test_timeout_and_authentication_are_distinct(self):
        _, termination, _ = self.worker("import sys,time; sys.stdin.read(); time.sleep(10)", timeout=0.1)
        self.assertEqual(termination, "timeout")
        _, termination, _ = self.worker('import sys; sys.stdin.read(); print(\'{"type":"turn.failed","error":{"message":"authentication failed CANARY"}}\'); sys.exit(1)')
        self.assertEqual(termination, "authentication_error")

    def test_completed_event_and_zero_exit_keep_usage(self):
        n, termination, _ = self.worker('import sys; sys.stdin.read(); print(\'{"type":"thread.started"}\'); print(\'{"type":"turn.completed","usage":{"input_tokens":7,"output_tokens":2}}\')')
        self.assertEqual(termination, "completed")
        self.assertEqual(n.usage["input_tokens"], 7)
        self.assertIsNone(n.usage["cached_input_tokens"])

    def test_model_transport_failure_is_environment_and_bounded(self):
        program = 'import sys; sys.stdin.read(); print(\'{"type":"thread.started"}\'); print(\'{"type":"turn.failed","error":{"message":"model CANARY_MODEL is not available at /CANARY_PATH"}}\'); sys.exit(1)'
        n, termination, _ = self.worker(program)
        self.assertEqual(termination, "environment_error")
        self.assertEqual(n.failure_reason, "model_unavailable")
        self.assertNotIn("CANARY", json.dumps({"events": n.events, "reason": n.failure_reason}))
        self.assertEqual(run.failure_reason({"message": "error sending request for url https://CANARY"}), "api_transport_error")
        self.assertEqual(run.failure_reason({"status_code": 429}), "api_rate_limited")
        self.assertEqual(run.failure_reason({"status_code": 503}), "api_server_error")
        self.assertEqual(run.failure_reason({"message": "unsupported reasoning effort ultra"}), "reasoning_unsupported")

    def test_generic_failure_after_activity_is_agent_error(self):
        program = 'import sys; sys.stdin.read(); print(\'{"type":"thread.started"}\'); print(\'{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"CANARY"}}\'); print(\'{"type":"turn.failed","error":{"message":"unknown failure"}}\'); sys.exit(1)'
        n, termination, _ = self.worker(program)
        self.assertEqual(termination, "agent_error")
        self.assertEqual(n.failure_reason, "agent_turn_failed")


class CheckerTests(unittest.TestCase):
    def test_changed_checker_workspace_is_error(self):
        def mutate(command, **kwargs):
            (kwargs["cwd"] / "requirements_test.go").write_text("tampered")
            return subprocess.CompletedProcess(command, 0, b'{"Action":"pass","Test":"TestRequirements"}\n', b"")
        with mock.patch.object(run.subprocess, "run", side_effect=mutate):
            self.assertEqual(run.run_check("pagination", b"package task", "requirement"), "error")

    def test_checker_timeout_is_error(self):
        with mock.patch.object(run.subprocess, "run", side_effect=subprocess.TimeoutExpired("go", 45)):
            self.assertEqual(run.run_check("pagination", b"package task", "requirement"), "error")

    def test_environment_failure_is_not_source_failure(self):
        result = subprocess.CompletedProcess("go", 1, b"", b"tool unavailable CANARY")
        with mock.patch.object(run.subprocess, "run", return_value=result):
            self.assertEqual(run.run_check("pagination", b"package task", "requirement"), "error")

    def test_suite_is_deterministic_and_binds_rubric(self):
        a, b = run.build_suite(), run.build_suite()
        self.assertEqual(run.canonical(a), run.canonical(b))
        self.assertEqual(run.suite_digest(a), run.suite_digest(b))
        b["cases"][0]["protected_files"].append("extra.go")
        self.assertNotEqual(run.criteria_digest(b["cases"][0]), a["cases"][0]["criteria_digest"])


class RetentionTests(unittest.TestCase):
    def test_config_drift_preserves_first_attempt_and_missing_denominator(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp).resolve()
            config = parent / "config.toml"
            config.write_text('model="initial"')
            setup = {"status": "ready", "codex_version": "test", "go_version": "test", "platform": "test", "architecture": "test", "codex_digest": run.sha(b"test"), "harness_digest": run.sha(b"test")}
            def record(case, condition, *args):
                config.write_text('model="changed"')
                return {"id": "first", "case_id": case["id"], "termination": "completed", "checks": []}
            args = Namespace(codex="codex", out=str(parent / "run"), model="model", reasoning="ultra", timeout=300)
            with mock.patch.dict(os.environ, {"CODEX_HOME": str(parent)}), mock.patch.object(run, "preflight", return_value=setup), mock.patch.object(run, "self_test", return_value=[]), mock.patch.object(run, "attempt_record", side_effect=record):
                with self.assertRaisesRegex(ValueError, "configuration_changed_no_retry"):
                    run.run_pilot(args)
            receipt = json.loads((parent / "run/run.json").read_text())
            self.assertEqual((receipt["planned"], receipt["attempted"], receipt["missing"]), (6, 1, 5))
            self.assertEqual(len(json.loads((parent / "run/baseline-attempts.json").read_text())["attempts"]), 1)
            self.assertEqual(json.loads((parent / "run/candidate-attempts.json").read_text())["attempts"], [])
            self.assertTrue((parent / "run/snapshot/cases/pagination/independent/requirements_test.go").is_file())
            self.assertTrue(json.loads((parent / "run/configuration-drift.json").read_text())["canonical_changed"])


class PreflightTests(unittest.TestCase):
    def test_model_and_effort_must_be_in_live_catalog(self):
        catalog = {"models": [{"slug": "gpt-5.5", "base_instructions": "CANARY", "supported_reasoning_levels": [{"effort": "high"}, {"effort": "xhigh"}]}]}
        capability = run.catalog_capability(catalog, "gpt-5.5", "xhigh")
        self.assertEqual(capability["supported_reasoning_efforts"], ["high", "xhigh"])
        self.assertNotIn("CANARY", json.dumps(capability))
        with self.assertRaisesRegex(ValueError, "requested_model_unavailable"):
            run.catalog_capability(catalog, "gpt-6-astra", "ultra")
        with self.assertRaisesRegex(ValueError, "requested_reasoning_unsupported"):
            run.catalog_capability(catalog, "gpt-5.5", "ultra")

    def test_config_hashes_keep_security_and_notice_changes(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "config.toml"
            path.write_text('model="test"\n[permissions]\nsecret="CANARY"\n')
            first = run.config_fingerprints(path)
            self.assertNotIn("CANARY", json.dumps(first))
            path.write_text('# comment\nmodel = "test"\n[permissions]\nsecret = "CANARY"\n')
            formatted = run.config_fingerprints(path)
            self.assertNotEqual(first["byte_digest"], formatted["byte_digest"])
            self.assertEqual(first["canonical_digest"], formatted["canonical_digest"])
            path.write_text('model="test"\n[permissions]\nsecret="CHANGED"\n[notice]\nhide=true\n')
            changed = run.config_fingerprints(path)
            self.assertNotEqual(first["top_level_digests"]["permissions"], changed["top_level_digests"]["permissions"])
            self.assertIn("notice", changed["top_level_digests"])


if __name__ == "__main__":
    unittest.main()
