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
    def test_submitted_api_breaks_are_result_failures(self):
        sources = {
            "deleted": b"package task\n",
            "renamed": b"package task\nfunc Other(items []int, page, size int) ([]int, error) { return nil, nil }\n",
            "arguments": b"package task\nfunc Page(items []int) ([]int, error) { return nil, nil }\n",
            "argument_type": b"package task\nfunc Page(items []string, page, size int) ([]int, error) { return nil, nil }\n",
            "return_count": b"package task\nfunc Page(items []int, page, size int) []int { return nil }\n",
            "return_type": b'package task\nfunc Page(items []int, page, size int) (string, error) { return "", nil }\n',
            "syntax": b"package task\nfunc Page(\n",
        }
        for name, source in sources.items():
            with self.subTest(name=name):
                self.assertEqual(run.run_check("pagination", source, "requirement"), "fail")
        golden = (run.HERE / "cases/pagination/golden/task.go").read_bytes()
        self.assertEqual(run.run_check("pagination", golden, "requirement"), "pass")

    def test_fixed_test_error_without_passing_countercheck_stays_error(self):
        events = [
            {"Action": "build-output", "ImportPath": "pilotcase [pilotcase.test]", "Output": "./requirements_test.go:21:15: undefined: BROKEN_TEST\n"},
            {"Action": "build-fail", "ImportPath": "pilotcase [pilotcase.test]"},
        ]
        result = subprocess.CompletedProcess("go", 1, b"\n".join(json.dumps(event).encode() for event in events), b"")
        with mock.patch.object(run.subprocess, "run", return_value=result) as check:
            self.assertEqual(run.run_check("pagination", b"package task", "requirement"), "error")
            self.assertEqual(check.call_count, 2)

    def test_unattributed_build_errors_do_not_run_countercheck(self):
        for path, target in (("./unknown.go", "pilotcase"), ("./task.go", "unrelated"), ("./requirements_test.go", "unrelated")):
            with self.subTest(path=path, target=target):
                events = [{"Action": "build-output", "ImportPath": target, "Output": path + ":1:1: compile failure\n"},
                          {"Action": "build-fail", "ImportPath": target}]
                result = subprocess.CompletedProcess("go", 1, b"\n".join(json.dumps(event).encode() for event in events), b"")
                with mock.patch.object(run.subprocess, "run", return_value=result) as check:
                    self.assertEqual(run.run_check("pagination", b"package task", "requirement"), "error")
                    self.assertEqual(check.call_count, 1)

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
    def run_with_configuration_change(self, attempt_number, delete=False):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp).resolve()
            config = parent / "config.toml"
            config.write_text('model="CANARY_CONFIGURATION"')
            expected_digest = run.sha(config.read_bytes())
            setup = {"status": "ready", "codex_version": "test", "go_version": "test", "platform": "test", "architecture": "test", "codex_digest": run.sha(b"test"), "harness_digest": run.sha(b"test"),
                     "configuration": run.config_fingerprints(config)}
            executed = []

            def worker(command, workspace, prompt, timeout):
                executed.append(workspace)
                if len(executed) == attempt_number:
                    if delete:
                        config.unlink()
                    else:
                        config.write_text('model="CANARY_CHANGED"')
                normalizer = run.Normalizer()
                normalizer.thread_started = True
                normalizer.agent_activity = True
                return normalizer, "completed", 11

            args = Namespace(codex="codex", out=str(parent / "run"), model="model", reasoning="ultra", timeout=300)
            reason = "configuration_unavailable_no_retry" if delete else "configuration_changed_no_retry"
            with mock.patch.dict(os.environ, {"CODEX_HOME": str(parent)}), mock.patch.object(run, "preflight", return_value=setup), mock.patch.object(run, "self_test", return_value=[]), mock.patch.object(run, "execute_worker", side_effect=worker), mock.patch.object(run, "run_check", return_value="pass"), mock.patch("builtins.print"):
                with self.assertRaisesRegex(ValueError, reason):
                    run.run_pilot(args)
            receipt = json.loads((parent / "run/run.json").read_text())
            self.assertEqual((receipt["planned"], receipt["attempted"], receipt["missing"]), (6, attempt_number, 6 - attempt_number))
            self.assertEqual(receipt["abort_reason"], reason)
            self.assertEqual(receipt["automatic_retries"], 0)
            self.assertEqual(len(executed), attempt_number)
            attempts = []
            for condition in ("baseline", "candidate"):
                data = json.loads((parent / "run" / (condition + "-attempts.json")).read_text())
                self.assertEqual(data["schema"], "eval-attempts/v2")
                attempts.extend(data["attempts"])
            changed = [attempt for attempt in attempts if attempt["configuration_observation"]["status"] != "unchanged"]
            self.assertEqual(len(changed), 1)
            for attempt in attempts:
                self.assertEqual(attempt["termination"], "completed")
                self.assertEqual(attempt["measurements"]["duration_ms"], 11)
                self.assertIsNone(attempt["measurements"]["input_tokens"])
                retained = parent / "run/evidence" / attempt["id"]
                self.assertEqual(json.loads((retained / "attempt.json").read_text()), attempt)
                self.assertEqual(json.loads((retained / "runner.json").read_text())["configuration_observation"], attempt["configuration_observation"])
            observation = changed[0]["configuration_observation"]
            self.assertEqual(observation, {"status": "unavailable" if delete else "changed", "expected_digest": expected_digest,
                                           "before_digest": expected_digest, "after_digest": None if delete else run.sha(config.read_bytes())})
            self.assertTrue((parent / "run/snapshot/cases/pagination/independent/requirements_test.go").is_file())
            diagnostic = json.loads((parent / "run/configuration-drift.json").read_text())
            self.assertEqual(diagnostic["stage"], "after_attempt")
            self.assertEqual(diagnostic["configuration_observation"], observation)
            self.assertEqual(diagnostic["canonical_changed"], None if delete else True)
            self.assertNotIn("CANARY", json.dumps({"attempts": attempts, "diagnostic": diagnostic, "receipt": receipt}))

    def test_first_and_last_attempt_configuration_changes_are_retained(self):
        for attempt_number in (1, 6):
            with self.subTest(attempt_number=attempt_number):
                self.run_with_configuration_change(attempt_number)

    def test_first_and_last_attempt_configuration_deletion_is_unavailable(self):
        for attempt_number in (1, 6):
            with self.subTest(attempt_number=attempt_number):
                self.run_with_configuration_change(attempt_number, delete=True)

    def test_change_since_preflight_aborts_before_first_worker(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp).resolve()
            config = parent / "config.toml"
            config.write_text('model="initial"')
            setup = {"status": "ready", "codex_version": "test", "go_version": "test", "platform": "test", "architecture": "test", "codex_digest": run.sha(b"test"), "harness_digest": run.sha(b"test"),
                     "configuration": run.config_fingerprints(config)}
            config.write_text('model="changed"')
            args = Namespace(codex="codex", out=str(parent / "run"), model="model", reasoning="ultra", timeout=300)
            with mock.patch.dict(os.environ, {"CODEX_HOME": str(parent)}), mock.patch.object(run, "preflight", return_value=setup), mock.patch.object(run, "self_test", return_value=[]), mock.patch.object(run, "execute_worker") as worker:
                with self.assertRaisesRegex(ValueError, "configuration_changed_no_retry"):
                    run.run_pilot(args)
                worker.assert_not_called()
            receipt = json.loads((parent / "run/run.json").read_text())
            self.assertEqual((receipt["attempted"], receipt["missing"]), (0, 6))
            self.assertEqual(json.loads((parent / "run/configuration-drift.json").read_text())["stage"], "before_attempt")


class PreflightTests(unittest.TestCase):
    def test_missing_configuration_is_not_empty_configuration(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "config.toml"
            with self.assertRaises(OSError):
                run.config_fingerprints(path)
            self.assertIsNone(run.config_byte_digest(path))
            path.write_bytes(b"")
            self.assertEqual(run.config_byte_digest(path), run.sha(b""))
            with mock.patch.object(run, "read_regular", side_effect=PermissionError("CANARY")):
                self.assertIsNone(run.config_byte_digest(path))

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
