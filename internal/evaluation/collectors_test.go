package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Re-exec this test binary as the configured exporter. No shell script or shell
// evaluation is used, and the exact command arguments are checked in the child.
func init() {
	if len(os.Args) == 5 && os.Args[1] == "run" && os.Args[2] == "export" && os.Args[3] == "--format" && os.Args[4] == "json" {
		if expected := os.Getenv("EVAL_TEST_EXPECT_REPO"); expected != "" {
			cwd, err := os.Getwd()
			if err != nil || cwd != expected {
				os.Exit(97)
			}
		}
		fixture := os.Getenv("EVAL_TEST_EXPORT")
		if fixture == "" {
			os.Exit(99)
		}
		if os.Getenv("EVAL_TEST_EXPORT_WAIT") == "1" {
			time.Sleep(10 * time.Second)
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			os.Exit(98)
		}
		fmt.Fprint(os.Stdout, string(data))
		if os.Getenv("EVAL_TEST_EXPORT_PARTIAL") == "1" {
			fmt.Fprintln(os.Stderr, "CANARY_PRIVATE_ERROR")
			os.Exit(8)
		}
		os.Exit(0)
	}
}

func gatherFixture(t *testing.T) (Snapshot, string, string, string) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sessions := filepath.Join(base, "sessions")
	ward := filepath.Join(base, "ward")
	for _, p := range []string{filepath.Join(repo, ".git"), sessions, ward} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	return Snapshot{Experiment: Experiment{Schema: Schema, ID: "experiment", StartedAt: start, Config: Config{Repositories: []string{repo}, SessionDirs: []string{sessions}, WardDirs: []string{ward}}}, Tasks: map[string]Task{}, Events: map[string]Event{}}, repo, sessions, ward
}
func writeRecords(t *testing.T, path string, records ...any) {
	t.Helper()
	var b strings.Builder
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
func metaFixture(id, repo string, source any) map[string]any {
	return map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "timestamp": "2026-09-11T01:00:00Z", "cwd": repo, "source": source, "user_instructions": "CANARY_PRIVATE_PROMPT"}}
}
func turnFixture(profile string) map[string]any {
	return map[string]any{"type": "turn_context", "payload": map[string]any{"permission_profile": map[string]any{"type": profile, "file_system": map[string]any{"CANARY_PRIVATE_PATH": "CANARY_PRIVATE_PATH"}}, "model": "gpt-6-astra", "developer_instructions": "CANARY_PRIVATE_INSTRUCTION"}}
}
func wardFixture(session string, seq int, outcome string) map[string]any {
	return map[string]any{"schema": "ward-diagnostic-record/v1", "received_at": "2026-09-11T01:01:00Z", "collector_generation": "generation-1", "sequence": seq, "event": map[string]any{"schema": "ward-diagnostic-event/v1", "ward_version": "0.1.0-dev.0", "tool": "CANARY_PRIVATE_TOOL", "stage": "evaluate", "outcome": outcome, "duration_us": 1200, "session_id": session, "turn_id": "private-turn", "tool_use_id": "private-tool"}}
}
func applyGather(s Snapshot, tasks []Task, events []Event, bindings []Binding, r Receipt) Snapshot {
	s.Bindings = append(s.Bindings, bindings...)
	for _, t := range tasks {
		s.Tasks[t.ID] = t
	}
	for _, e := range events {
		s.Events[e.ID] = e
	}
	s.Receipts = append(s.Receipts, r)
	return s
}
func hasIssue(r Receipt, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
func requirePrivate(t *testing.T, values ...any) {
	t.Helper()
	for _, v := range values {
		b, _ := json.Marshal(v)
		for _, bad := range []string{"CANARY_PRIVATE", "private-session", "private-child", "private-run", "private-task"} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("private value leaked: %s", b)
			}
		}
	}
}

func TestGatherSessionsWardPrivacyAndIdempotence(t *testing.T) {
	snap, repo, sessions, ward := gatherFixture(t)
	writeRecords(t, filepath.Join(sessions, "one.jsonl"), metaFixture("private-session", repo, "vscode"), map[string]any{"type": "response_item", "payload": map[string]any{"command": "CANARY_PRIVATE_COMMAND"}}, turnFixture("managed"), turnFixture("disabled"))
	writeRecords(t, filepath.Join(ward, "events.jsonl"), wardFixture("private-session", 1, "defer"))
	now := snap.Experiment.StartedAt.Add(2 * time.Hour)
	tasks, events, bindings, r := Gather(context.Background(), snap, now)
	if !r.Complete || len(tasks) != 1 || len(events) != 1 || r.Unmatched != 1 {
		t.Fatalf("unexpected gather tasks=%+v events=%+v receipt=%+v", tasks, events, r)
	}
	if tasks[0].Profile != "managed" || tasks[0].Kind != "root" || events[0].HintTaskID != tasks[0].ID || events[0].Outcome != "defer" || events[0].DurationMS == nil || *events[0].DurationMS != 1.2 {
		t.Fatal(tasks, events)
	}
	requirePrivate(t, tasks, events, r)
	snap = applyGather(snap, tasks, events, bindings, r)
	tasks, events, bindings, r = Gather(context.Background(), snap, now.Add(time.Hour))
	if !r.Complete || len(tasks) != 0 || len(events) != 0 || len(bindings) != 0 || r.Duplicates != 2 {
		t.Fatalf("not idempotent: %+v", r)
	}
}
func TestGatherChildUnknownAndExcludedMetadata(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	snap.Experiment.Config.SelfRepository = repo
	childSource := map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "private-session", "agent_path": "CANARY_PRIVATE_AGENT"}}}
	writeRecords(t, filepath.Join(sessions, "a.jsonl"), metaFixture("private-child", repo, childSource), turnFixture("managed"))
	writeRecords(t, filepath.Join(sessions, "z.jsonl"), metaFixture("private-session", repo, "vscode"), turnFixture("disabled"))
	writeRecords(t, filepath.Join(sessions, "unknown.jsonl"), metaFixture("private-unknown", repo, map[string]any{"foreign": "CANARY_PRIVATE_SOURCE"}))
	tasks, _, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(tasks) != 3 || !hasIssue(r, "session_first_profile_unknown") || !hasIssue(r, "session_origin_unknown") || !hasIssue(r, "session_unprotected_profile") {
		t.Fatalf("tasks=%+v receipt=%+v", tasks, r)
	}
	parents := map[string]bool{}
	for _, task := range tasks {
		parents[task.ID] = true
		if task.ExclusionReason != "eval_self" {
			t.Fatal(task)
		}
	}
	for _, task := range tasks {
		if task.Kind == "child" && !parents[task.ParentID] {
			t.Fatal("child not linked to known parent")
		}
	}
	requirePrivate(t, tasks, r)
}
func TestGatherWardGapsMalformedUnmatchedAndRotation(t *testing.T) {
	snap, _, _, ward := gatherFixture(t)
	path := filepath.Join(ward, "events.jsonl")
	bad := wardFixture("private-session", 2, "defer")
	bad["unexpected"] = "CANARY_PRIVATE_SECRET"
	writeRecords(t, path, wardFixture("missing-session", 1, "defer"), bad, wardFixture("missing-session", 3, "deny"))
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 2 || r.Unmatched != 2 || !hasIssue(r, "ward_invalid_record") || !hasIssue(r, "ward_sequence_gap") {
		t.Fatal(events, r)
	}
	requirePrivate(t, events, r)
	snap = applyGather(snap, tasks, events, bindings, r)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, _, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(3*time.Hour))
	if !hasIssue(r, "ward_source_rotated_or_removed") {
		t.Fatal(r)
	}
}
func TestGatherRejectsSymlinkAndPartialLine(t *testing.T) {
	snap, repo, sessions, ward := gatherFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeRecords(t, outside, metaFixture("private-session", repo, "vscode"), turnFixture("managed"))
	if err := os.Symlink(outside, filepath.Join(sessions, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(wardFixture("missing", 1, "defer"))
	if err := os.WriteFile(filepath.Join(ward, "incomplete.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	tasks, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(tasks) != 0 || len(events) != 0 || !hasIssue(r, "source_symlink_rejected") || !hasIssue(r, "source_truncated_line") {
		t.Fatal(tasks, events, r)
	}
}
func sealFixture() map[string]any {
	return map[string]any{"schema": "seal-run-export/v1", "exporter_version": "0.3.0-rc.4", "scan_complete": true, "issues": []any{}, "runs": []any{map[string]any{"task_id": "private-task", "run_id": "private-run", "run_version": nil, "evidence_sha256": strings.Repeat("a", 64), "timestamp": "2026-09-11T01:00:00Z", "mechanical_result": "pass", "required_checks_pass": true, "scope_pass": true, "source_stable_during_checks": true, "scope_violation_count": 0, "checks": []any{map[string]any{"index": 0, "required": true, "passed": true, "timed_out": false, "exit_code": 0, "duration_seconds": 1.5}}, "completion_record": map[string]any{"state": "absent", "completed_at": nil}}}}
}
func prepareSeal(t *testing.T, snap *Snapshot, repo string) string {
	t.Helper()
	if err := os.Mkdir(filepath.Join(repo, ".seal"), 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	snap.Experiment.Config.SealBinary = bin
	fixture := filepath.Join(t.TempDir(), "fixture.json")
	t.Setenv("EVAL_TEST_EXPORT", fixture)
	return fixture
}
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestGatherSealCompletionRevisionAndBoundaries(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	writeJSON(t, file, fixture)
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if !r.Complete || len(events) != 1 || events[0].Seal == nil || r.Unmatched != 1 {
		t.Fatal(events, r)
	}
	first := events[0]
	requirePrivate(t, events, r)
	snap = applyGather(snap, tasks, events, bindings, r)
	_, events, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(3*time.Hour))
	if len(events) != 0 || r.Duplicates != 1 {
		t.Fatal(events, r)
	}
	run := fixture["runs"].([]any)[0].(map[string]any)
	run["completion_record"] = map[string]any{"state": "recorded_pass", "completed_at": "2026-09-11T03:01:00Z"}
	writeJSON(t, file, fixture)
	_, events, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(4*time.Hour))
	if !r.Complete || len(events) != 1 || events[0].Revision != 2 || events[0].SourceID != first.SourceID || events[0].ID == first.ID || events[0].Fingerprint == first.Fingerprint {
		t.Fatal(events, r)
	}
}

func TestGatherSealExporterUpgradeDoesNotReviseUnknownProducer(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	writeJSON(t, file, fixture)
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if !r.Complete || len(events) != 1 || events[0].Version != "unknown" {
		t.Fatal("exporter version was attributed to the Run", events, r)
	}
	snap = applyGather(snap, tasks, events, bindings, r)
	fixture["exporter_version"] = "0.4.0"
	writeJSON(t, file, fixture)
	tasks, events, bindings, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(3*time.Hour))
	if !r.Complete || len(events) != 0 || r.Duplicates != 1 {
		t.Fatal("exporter upgrade revised unchanged execution evidence", events, r)
	}
	snap = applyGather(snap, tasks, events, bindings, r)
	report := BuildReport(snap)
	if len(report.Cohorts) != 1 || report.Cohorts[0].Cohort.Module != "seal" || report.Cohorts[0].Cohort.Version != "unknown" || report.Cohorts[0].Counts.UnderlyingUniqueRuns != 1 || report.Cohorts[0].Counts.SourceUpdates != 0 {
		t.Fatal("unsupported producer cohort", report.Cohorts)
	}
	requirePrivate(t, events, r, report)
}

func TestGatherSealUsesOnlyValidRunProducerVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version any
		want    string
		invalid bool
	}{
		{name: "explicit producer", version: "0.2.0", want: "0.2.0"},
		{name: "unknown producer", version: nil, want: "unknown"},
		{name: "invalid producer", version: "CANARY_PRIVATE_VERSION", want: "unknown", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, repo, _, _ := gatherFixture(t)
			file := prepareSeal(t, &snap, repo)
			fixture := sealFixture()
			fixture["runs"].([]any)[0].(map[string]any)["run_version"] = tc.version
			writeJSON(t, file, fixture)
			_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
			if len(events) != 1 || events[0].Version != tc.want || hasIssue(r, "seal_invalid_version") != tc.invalid {
				t.Fatal(events, r)
			}
			requirePrivate(t, events, r)
		})
	}
}

func TestGatherSealRequiresExplicitVersionProvenance(t *testing.T) {
	for _, field := range []string{"exporter_version", "run_version"} {
		t.Run(field, func(t *testing.T) {
			snap, repo, _, _ := gatherFixture(t)
			file := prepareSeal(t, &snap, repo)
			fixture := sealFixture()
			if field == "exporter_version" {
				delete(fixture, field)
				fixture["seal_version"] = "0.3.0-rc.4"
			} else {
				delete(fixture["runs"].([]any)[0].(map[string]any), field)
			}
			writeJSON(t, file, fixture)
			_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
			if len(events) != 0 || !hasIssue(r, "seal_export_invalid") {
				t.Fatal("ambiguous version provenance was accepted", events, r)
			}
		})
	}
}

func TestGatherSealPartialInvalidAndTimeout(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	fixture["scan_complete"] = false
	writeJSON(t, file, fixture)
	t.Setenv("EVAL_TEST_EXPORT_PARTIAL", "1")
	_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 1 || !hasIssue(r, "seal_export_incomplete") {
		t.Fatal(events, r)
	}
	requirePrivate(t, events, r)
	fixture["raw_command"] = "CANARY_PRIVATE_COMMAND"
	writeJSON(t, file, fixture)
	_, events, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 0 || !hasIssue(r, "seal_export_invalid") {
		t.Fatal(events, r)
	}
	t.Setenv("EVAL_TEST_EXPORT_WAIT", "1")
	snap.Experiment.Config.ExportTimeoutSeconds = 1
	_, _, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if !hasIssue(r, "seal_export_timeout") {
		t.Fatal(r)
	}
}
func TestGatherActivationDoesNotIncludeOldTasks(t *testing.T) {
	snap, repo, sessions, ward := gatherFixture(t)
	old := metaFixture("private-session", repo, "vscode")
	old["payload"].(map[string]any)["timestamp"] = "2026-09-10T23:59:59Z"
	writeRecords(t, filepath.Join(sessions, "old.jsonl"), old, turnFixture("managed"))
	oldWard := wardFixture("private-session", 1, "defer")
	oldWard["received_at"] = "2026-09-10T23:59:59Z"
	writeRecords(t, filepath.Join(ward, "old.jsonl"), oldWard)
	tasks, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if !r.Complete || len(tasks) != 0 || len(events) != 0 {
		t.Fatal(tasks, events, r)
	}
}

func TestGatherMetadataCompletionAppendsTaskRevision(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	file := filepath.Join(sessions, "active.jsonl")
	writeRecords(t, file, metaFixture("private-session", repo, "vscode"))
	now := snap.Experiment.StartedAt.Add(2 * time.Hour)
	tasks, events, bindings, r := Gather(context.Background(), snap, now)
	if len(tasks) != 1 || tasks[0].Revision != 1 || tasks[0].Profile != "unknown" || !hasIssue(r, "session_first_profile_unknown") {
		t.Fatal(tasks, r)
	}
	first := tasks[0]
	snap = applyGather(snap, tasks, events, bindings, r)
	writeRecords(t, file, metaFixture("private-session", repo, "vscode"), turnFixture("managed"))
	tasks, events, bindings, r = Gather(context.Background(), snap, now.Add(time.Hour))
	if len(tasks) != 1 || tasks[0].ID != first.ID || tasks[0].Revision != 2 || tasks[0].Profile != "managed" || !r.Complete || r.TasksAdded != 0 || r.TasksUpdated != 1 {
		t.Fatal(tasks, r)
	}
	snap = applyGather(snap, tasks, events, bindings, r)
	writeRecords(t, file, metaFixture("private-session", repo, "vscode"), turnFixture("disabled"))
	tasks, _, _, r = Gather(context.Background(), snap, now.Add(2*time.Hour))
	if len(tasks) != 0 || !hasIssue(r, "session_first_profile_changed") {
		t.Fatal(tasks, r)
	}
}
func TestGatherSealRejectsMissingResultFields(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	run := fixture["runs"].([]any)[0].(map[string]any)
	delete(run, "scope_pass")
	writeJSON(t, file, fixture)
	_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 0 || !hasIssue(r, "seal_export_invalid") {
		t.Fatal(events, r)
	}
}
func TestGatherMalformedSessionIdentifierNeverAllocatesTask(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	writeRecords(t, filepath.Join(sessions, "bad.jsonl"), metaFixture("CANARY_PRIVATE_PATH/escape", repo, "vscode"), turnFixture("managed"))
	tasks, _, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(tasks) != 0 || !hasIssue(r, "session_invalid_metadata") {
		t.Fatal(tasks, r)
	}
	for _, b := range bindings {
		if b.Kind == "task" {
			t.Fatal("invalid session became task binding", b)
		}
	}
}

func TestGatherRejectsDuplicateSourceKeys(t *testing.T) {
	snap, repo, _, ward := gatherFixture(t)
	raw, _ := json.Marshal(wardFixture("missing", 1, "defer"))
	raw = []byte(strings.Replace(string(raw), `"outcome":"defer"`, `"outcome":"deny","outcome":"defer"`, 1))
	if err := os.WriteFile(filepath.Join(ward, "duplicate.jsonl"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	file := prepareSeal(t, &snap, repo)
	data, _ := json.Marshal(sealFixture())
	data = []byte(strings.Replace(string(data), `"scan_complete":true`, `"scan_complete":false,"scan_complete":true`, 1))
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 0 || !hasIssue(r, "ward_invalid_record") || !hasIssue(r, "seal_export_invalid") {
		t.Fatal(events, r)
	}
}
func TestGatherExportsExactVerifiedNestedCheckout(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	nested := filepath.Join(repo, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	file := prepareSeal(t, &snap, nested)
	writeJSON(t, file, sealFixture())
	t.Setenv("EVAL_TEST_EXPECT_REPO", physical)
	writeRecords(t, filepath.Join(sessions, "nested.jsonl"), metaFixture("private-session", nested, "vscode"), turnFixture("managed"))
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if !r.Complete || len(tasks) != 1 || len(events) != 1 || events[0].RepositoryID != tasks[0].RepositoryID {
		t.Fatal(tasks, events, r)
	}
	found := false
	for _, binding := range bindings {
		if binding.Kind == "repository" && binding.Key == physical {
			found = true
		}
	}
	if !found {
		t.Fatal("nested checkout not independently aliased")
	}
}
func TestOldSessionPartitionKeepsActivationTimeZoneBoundary(t *testing.T) {
	start := time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC)
	if !oldSessionPartition("2026/08", start) || !oldSessionPartition("2026/09/08", start) {
		t.Fatal("old partition not skipped")
	}
	for _, path := range []string{"2026", "2026/09", "2026/09/10", "2026/09/11", "archive", "../2026/08"} {
		if oldSessionPartition(path, start) {
			t.Fatalf("unsafe partition skip %q", path)
		}
	}
}

func TestGatherSealRevertedSnapshotAppendsRevision(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	var source, firstFingerprint string
	for i, state := range []string{"absent", "recorded_pass", "absent"} {
		run := fixture["runs"].([]any)[0].(map[string]any)
		var completedAt any
		if state == "recorded_pass" {
			completedAt = "2026-09-11T01:30:00Z"
		}
		run["completion_record"] = map[string]any{"state": state, "completed_at": completedAt}
		writeJSON(t, file, fixture)
		tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(time.Duration(i+2)*time.Hour))
		if !r.Complete || len(events) != 1 || events[0].Revision != i+1 {
			t.Fatalf("step %d events=%+v receipt=%+v", i, events, r)
		}
		if i == 0 {
			source = events[0].SourceID
			firstFingerprint = events[0].Fingerprint
		}
		if events[0].SourceID != source {
			t.Fatal("source identity changed")
		}
		if i == 2 && events[0].Fingerprint != firstFingerprint {
			t.Fatal("reverted snapshot was not retained as a new revision")
		}
		snap = applyGather(snap, tasks, events, bindings, r)
	}
	_, events, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(6*time.Hour))
	if !r.Complete || len(events) != 0 || r.Duplicates != 1 {
		t.Fatal("unchanged repeat inflated revisions", events, r)
	}
}
func TestGatherWardImmutableSequenceConflict(t *testing.T) {
	snap, _, _, ward := gatherFixture(t)
	file := filepath.Join(ward, "events.jsonl")
	writeRecords(t, file, wardFixture("missing", 1, "defer"), wardFixture("missing", 1, "deny"))
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(events) != 1 || events[0].Outcome != "defer" || !hasIssue(r, "ward_record_conflict") {
		t.Fatal(events, r)
	}
	snap = applyGather(snap, tasks, events, bindings, r)
	writeRecords(t, file, wardFixture("missing", 1, "deny"))
	_, events, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(3*time.Hour))
	if len(events) != 0 || !hasIssue(r, "ward_record_conflict") {
		t.Fatal("immutable Ward fact overwritten", events, r)
	}
}

func TestGatherSealNullTimestampRetainsVerifiedUnknownTime(t *testing.T) {
	snap, repo, _, _ := gatherFixture(t)
	file := prepareSeal(t, &snap, repo)
	fixture := sealFixture()
	fixture["runs"].([]any)[0].(map[string]any)["timestamp"] = nil
	fixture["tasks"] = []any{map[string]any{"task_id": "private-task"}}
	writeJSON(t, file, fixture)
	tasks, events, bindings, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if r.Complete || len(tasks) != 0 || len(events) != 1 || events[0].SourceTime != nil || events[0].Seal == nil || events[0].Outcome != "pass" || !hasIssue(r, "seal_invalid_time") {
		t.Fatal(tasks, events, r)
	}
	for _, issue := range r.Issues {
		if issue.Code == "seal_invalid_time" && issue.SourceID != events[0].SourceID {
			t.Fatal("time issue lost run correlation")
		}
	}
	found := false
	for _, binding := range bindings {
		if binding.Kind == "seal_task" {
			found = true
		}
	}
	if !found {
		t.Fatal("private Seal task inventory missing")
	}
	requirePrivate(t, tasks, events, r)
	snap = applyGather(snap, tasks, events, bindings, r)
	_, events, _, r = Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(3*time.Hour))
	if len(events) != 0 || r.Duplicates != 1 || !hasIssue(r, "seal_invalid_time") {
		t.Fatal("unknown time repeated as new invocation", events, r)
	}
}

func TestGatherEquivalentMetadataContinuesAndRefinesExistingTask(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	file := filepath.Join(sessions, "repeated.jsonl")
	meta := metaFixture("private-session", repo, "vscode")
	writeRecords(t, file, meta)
	now := snap.Experiment.StartedAt.Add(2 * time.Hour)
	tasks, events, bindings, r := Gather(context.Background(), snap, now)
	if len(tasks) != 1 || tasks[0].Profile != "unknown" {
		t.Fatal(tasks, r)
	}
	oldID := tasks[0].ID
	snap = applyGather(snap, tasks, events, bindings, r)
	writeRecords(t, file, meta, meta, turnFixture("managed"))
	tasks, _, _, r = Gather(context.Background(), snap, now.Add(time.Hour))
	if !r.Complete || len(tasks) != 1 || tasks[0].ID != oldID || tasks[0].Revision != 2 || tasks[0].Profile != "managed" || r.TasksAdded != 0 || r.TasksUpdated != 1 {
		t.Fatal(tasks, r)
	}
	if hasIssue(r, "session_duplicate_metadata") {
		t.Fatal("equivalent metadata rejected")
	}
}
func TestGatherConflictingInheritedMetadataNeverSuppliesChildProfile(t *testing.T) {
	snap, repo, sessions, _ := gatherFixture(t)
	child := metaFixture("private-child", repo, map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "private-parent"}}})
	parent := metaFixture("private-parent", repo, "vscode")
	writeRecords(t, filepath.Join(sessions, "child.jsonl"), child, parent, turnFixture("managed"))
	tasks, _, _, r := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(tasks) != 1 || tasks[0].Kind != "child" || tasks[0].Profile != "unknown" || !hasIssue(r, "session_duplicate_metadata") || !hasIssue(r, "session_first_profile_unknown") {
		t.Fatal(tasks, r)
	}
}

func TestGatherWardHintRemainsUnmatchedWithoutHumanReview(t *testing.T) {
	snap, repo, sessions, ward := gatherFixture(t)
	writeRecords(t, filepath.Join(sessions, "hint.jsonl"), metaFixture("private-session", repo, "vscode"), turnFixture("managed"))
	writeRecords(t, filepath.Join(ward, "hint.jsonl"), wardFixture("private-session", 1, "defer"))
	tasks, events, _, receipt := Gather(context.Background(), snap, snap.Experiment.StartedAt.Add(2*time.Hour))
	if len(tasks) != 1 || len(events) != 1 || events[0].HintTaskID != tasks[0].ID || receipt.Unmatched != 1 {
		t.Fatal("a diagnostic hint was promoted to confirmed membership", tasks, events, receipt)
	}
}
