package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgoneit/eval/internal/evaluation"
)

func TestLedgerEndToEndPrivateReviewAndReport(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	sessions := filepath.Join(base, "sessions")
	root := filepath.Join(base, "state")
	for _, p := range []string{repo, sessions} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := evaluation.Config{SchemaVersion: 1, Repositories: []string{repo}, SessionDirs: []string{sessions}}
	configFile := filepath.Join(base, "config.json")
	data, _ := json.Marshal(config)
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	invoke := func(args ...string) (int, []byte) {
		t.Helper()
		var out, stderr bytes.Buffer
		args = append(args, "--state-root", root)
		code := Run(context.Background(), args, Runtime{Stdout: &out, Stderr: &stderr, Now: func() time.Time { return now }})
		if stderr.Len() > 0 {
			t.Fatalf("unexpected stderr %q", stderr.String())
		}
		return code, out.Bytes()
	}
	code, data := invoke("experiment", "init", "--config", configFile)
	if code != 0 && code != 2 {
		t.Fatalf("init: %d %s", code, data)
	}
	var init struct {
		ID string `json:"experiment_id"`
	}
	if err := json.Unmarshal(data, &init); err != nil || init.ID == "" {
		t.Fatal("missing experiment ID")
	}
	now = now.Add(time.Minute)
	var session bytes.Buffer
	enc := json.NewEncoder(&session)
	_ = enc.Encode(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "original-task-canary", "timestamp": now.Format(time.RFC3339Nano), "cwd": repo, "source": "cli"}})
	_ = enc.Encode(map[string]any{"type": "turn_context", "payload": map[string]any{"model": "gpt-5.5", "permission_profile": map[string]any{"type": "managed"}}})
	_ = enc.Encode(map[string]any{"type": "response_item", "payload": map[string]any{"command": "CANARY_PRIVATE_COMMAND", "content": "CANARY_PRIVATE_TRANSCRIPT"}})
	if err := os.WriteFile(filepath.Join(sessions, "sample.jsonl"), session.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	code, data = invoke("collect", "--experiment", init.ID)
	if code != 0 && code != 2 {
		t.Fatalf("collect: %d %s", code, data)
	}
	var receipt evaluation.Receipt
	_ = json.Unmarshal(data, &receipt)
	if !receipt.Committed || receipt.TasksAdded != 1 {
		t.Fatalf("not persisted: %+v", receipt)
	}
	code, data = invoke("collect", "--experiment", init.ID)
	_ = json.Unmarshal(data, &receipt)
	if (code != 0 && code != 2) || receipt.TasksAdded != 0 || receipt.EventsAdded != 0 {
		t.Fatalf("not idempotent %d %+v", code, receipt)
	}
	outDir := filepath.Join(base, "review")
	code, data = invoke("review", "export", "--experiment", init.ID, "--out", outDir)
	if code != 0 && code != 2 {
		t.Fatalf("export: %d %s", code, data)
	}
	reviewFile := filepath.Join(outDir, "review.json")
	data, err := os.ReadFile(reviewFile)
	if err != nil {
		t.Fatal(err)
	}
	var review evaluation.Review
	if err = json.Unmarshal(data, &review); err != nil {
		t.Fatal(err)
	}
	if len(review.Tasks) != 1 {
		t.Fatal("missing roster")
	}
	review.Tasks[0].Eligibility = "yes"
	review.Tasks[0].Outcome = "completed"
	review.Tasks[0].TaskType = "coding"
	review.Tasks[0].TerminalRecordObserved = evaluation.Confirmed
	data, _ = json.Marshal(review)
	if err = os.WriteFile(reviewFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	code, data = invoke("review", "apply", "--experiment", init.ID, "--file", reviewFile)
	if code != 0 && code != 2 {
		t.Fatalf("apply: %d %s", code, data)
	}
	code, data = invoke("report", "--experiment", init.ID, "--format", "json")
	if code != 0 {
		t.Fatalf("report %d %s", code, data)
	}
	first := bytes.Clone(data)
	code, data = invoke("report", "--experiment", init.ID, "--format", "json")
	if code != 0 || !bytes.Equal(first, data) {
		t.Fatal("report not reproducible")
	}
	var report evaluation.Report
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Counts.Tasks != 1 || report.Counts.TerminalEligibleTasks != 1 {
		t.Fatal("incorrect report task denominator")
	}
	journal, err := os.ReadFile(filepath.Join(root, "jgoneit/eval-experiment/v2", init.ID, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"original-task-canary", repo, "CANARY_PRIVATE_COMMAND", "CANARY_PRIVATE_TRANSCRIPT"} {
		if bytes.Contains(journal, []byte(canary)) || bytes.Contains(data, []byte(canary)) {
			t.Fatalf("private canary leaked: %s", canary)
		}
	}
	if _, err = os.Stat(filepath.Join(root, "jgoneit/eval-experiment/v1/journal.jsonl")); !os.IsNotExist(err) {
		t.Fatal("new workflow touched legacy state")
	}
	// Reapplying an old human amendment cannot count it twice.
	code, data = invoke("review", "apply", "--experiment", init.ID, "--file", reviewFile)
	if code == 0 || strings.Contains(string(data), review.Tasks[0].TaskID) {
		t.Fatalf("old revision accepted or identity leaked: %d %s", code, data)
	}
}
