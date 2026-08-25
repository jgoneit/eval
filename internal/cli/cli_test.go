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

	"github.com/jgoneit/eval/internal/state"
)

func runForTest(t *testing.T, args []string, input string, env map[string]string) (int, []byte, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := Run(context.Background(), args, Runtime{
		Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(key string) string { return env[key] },
		Now:    func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) },
	})
	return exit, stdout.Bytes(), stderr.String()
}

func draftWithSpec(versionStatus string) string {
	version := `{"status":"unavailable","value":null}`
	if versionStatus == "known-public" {
		version = `{"status":"known-public","value":"0.2.0-dev.0"}`
	}
	return `{
  "terminal_on":"2026-08-25",
  "population":"real",
  "task_type":"test",
  "agent":"codex",
  "model":null,
  "host_os":"darwin",
  "modules":{"spec":{"used":true,"version":` + version + `,"metrics":null}},
  "outcome":{"status":"completed","user_interventions":null,"module_interaction_time":null,"rework_required":null},
  "task_effects":{}
}`
}

func privateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestVersionAndUsageExitCodes(t *testing.T) {
	exit, output, _ := runForTest(t, []string{"--version"}, "", nil)
	if exit != ExitSuccess || string(output) != "evalctl 0.2.0-dev.0\n" {
		t.Fatalf("version exit/output = %d %q", exit, output)
	}
	exit, _, _ = runForTest(t, []string{"unknown"}, "", nil)
	if exit != ExitUsage {
		t.Fatalf("unknown command exit = %d", exit)
	}
	exit, _, _ = runForTest(t, []string{"version"}, "", nil)
	if exit != ExitUsage {
		t.Fatalf("undocumented version alias exit = %d", exit)
	}
}

func TestCompareRejectsUndocumentedDateWindowFlags(t *testing.T) {
	exit, _, _ := runForTest(t, []string{"compare", "--module", "spec", "--by", "usage", "--as-of", "2026-08-25", "--from", "2026-08-01"}, "", nil)
	if exit != ExitUsage {
		t.Fatalf("compare --from exit = %d", exit)
	}
}

func TestObserveRecordsAndBestEffortSkipsInvalidDraft(t *testing.T) {
	root := privateRoot(t)
	exit, output, stderr := runForTest(t, []string{"observe", "--input", "-", "--state-root", root}, draftWithSpec("unavailable"), nil)
	if exit != ExitSuccess || stderr != "" {
		t.Fatalf("observe exit=%d stderr=%q output=%s", exit, stderr, output)
	}
	var result struct {
		Status   string `json:"status"`
		Revision int    `json:"revision"`
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Status != "recorded" || result.Revision != 1 {
		t.Fatalf("observe result = %#v err=%v", result, err)
	}
	data, err := os.ReadFile(state.V2Path(root))
	if err != nil || bytes.Count(data, []byte{'\n'}) != 1 {
		t.Fatalf("stored rows = %q err=%v", data, err)
	}

	exit, output, stderr = runForTest(t, []string{"observe", "--input", "-", "--best-effort", "--state-root", root}, `{}`, nil)
	if exit != ExitSuccess || stderr != "" || !bytes.Contains(output, []byte(`"status":"skipped"`)) {
		t.Fatalf("best effort exit=%d stderr=%q output=%s", exit, stderr, output)
	}
	after, err := os.ReadFile(state.V2Path(root))
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("best effort mutated log: %q err=%v", after, err)
	}

	exit, _, _ = runForTest(t, []string{"observe", "--input", "-", "--state-root", root}, `{}`, nil)
	if exit != ExitInvalidData {
		t.Fatalf("invalid draft exit = %d", exit)
	}
}

func TestValidateInvalidFileReturnsOneWithoutRawIdentifiers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.jsonl")
	if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exit, output, _ := runForTest(t, []string{"validate", "--file", path}, "", nil)
	if exit != ExitInvalidData || !bytes.Contains(output, []byte(`"status":"invalid"`)) {
		t.Fatalf("validate exit=%d output=%s", exit, output)
	}
	if bytes.Contains(output, []byte(path)) {
		t.Fatalf("validation leaked path: %s", output)
	}
}

func TestValidateAndObserveBothRejectMissingJSONLTerminalNewline(t *testing.T) {
	root := privateRoot(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "valid", "observations-v2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	truncated := bytes.TrimSuffix(fixture, []byte{'\n'})
	path := state.V2Path(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatal(err)
	}

	exit, output, stderr := runForTest(t, []string{"validate", "--state-root", root}, "", nil)
	if exit != ExitInvalidData || stderr != "" || !bytes.Contains(output, []byte(`"status":"invalid"`)) {
		t.Fatalf("validate exit=%d output=%s stderr=%q", exit, output, stderr)
	}
	exit, _, _ = runForTest(t, []string{"observe", "--input", "-", "--state-root", root}, draftWithSpec("unavailable"), nil)
	if exit != ExitInvalidData {
		t.Fatalf("observe exit = %d, want %d", exit, ExitInvalidData)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, truncated) {
		t.Fatalf("observe changed invalid state: %q err=%v", after, err)
	}
}

func TestSummaryAndComparisonAreDeterministic(t *testing.T) {
	root := privateRoot(t)
	env := map[string]string{"XDG_STATE_HOME": root}
	for _, status := range []string{"unavailable", "known-public"} {
		exit, output, stderr := runForTest(t, []string{"observe", "--input", "-"}, draftWithSpec(status), env)
		if exit != ExitSuccess {
			t.Fatalf("seed observe exit=%d output=%s stderr=%s", exit, output, stderr)
		}
	}
	args := []string{"summarize", "--as-of", "2026-08-25"}
	firstExit, first, firstErr := runForTest(t, args, "", env)
	secondExit, second, secondErr := runForTest(t, args, "", env)
	if firstExit != ExitSuccess || secondExit != ExitSuccess || firstErr != "" || secondErr != "" {
		t.Fatalf("summary exits=%d/%d stderr=%q/%q", firstExit, secondExit, firstErr, secondErr)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("summary differs:\n%s\n%s", first, second)
	}

	exit, comparison, stderr := runForTest(t, []string{"compare", "--module", "spec", "--by", "version", "--as-of", "2026-08-25"}, "", env)
	if exit != ExitSuccess || stderr != "" {
		t.Fatalf("compare exit=%d stderr=%q", exit, stderr)
	}
	if !bytes.Contains(comparison, []byte(`"version_unavailable":1`)) || !bytes.Contains(comparison, []byte(`"cohort":"0.2.0-dev.0","n":1`)) {
		t.Fatalf("comparison did not isolate unavailable version: %s", comparison)
	}
	usageExit, usage, _ := runForTest(t, []string{"compare", "--module", "spec", "--by", "usage", "--as-of", "2026-08-25"}, "", env)
	if usageExit != ExitSuccess || !bytes.Contains(usage, []byte(`"cohort":"used","n":2`)) {
		t.Fatalf("usage comparison excluded unavailable version: %s", usage)
	}
}

func TestReadOnlySummaryDoesNotCreateMissingState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	env := map[string]string{"XDG_STATE_HOME": root}
	exit, output, stderr := runForTest(t, []string{"summarize", "--as-of", "2026-08-25"}, "", env)
	if exit != ExitSuccess || stderr != "" || !bytes.Contains(output, []byte(`"included_real_tasks":0`)) {
		t.Fatalf("summary exit=%d output=%s stderr=%q", exit, output, stderr)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("summary created missing state: %v", err)
	}
}

func TestObserveEntropyFailureIsOperationalAndBestEffortSafe(t *testing.T) {
	root := privateRoot(t)
	run := func(bestEffort bool) (int, string, string) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		args := []string{"observe", "--input", "-", "--state-root", root}
		if bestEffort {
			args = append(args, "--best-effort")
		}
		exit := Run(context.Background(), args, Runtime{
			Stdin: strings.NewReader(draftWithSpec("unavailable")), Stdout: &stdout, Stderr: &stderr,
			Now:     func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) },
			NewUUID: func() (string, error) { return "", os.ErrNotExist },
		})
		return exit, stdout.String(), stderr.String()
	}
	exit, _, _ := run(false)
	if exit != ExitOperational {
		t.Fatalf("entropy failure exit = %d, want %d", exit, ExitOperational)
	}
	exit, output, stderr := run(true)
	if exit != ExitSuccess || stderr != "" || !strings.Contains(output, `"reason":"observe-operational-error"`) {
		t.Fatalf("best effort exit=%d output=%q stderr=%q", exit, output, stderr)
	}
	if _, err := os.Lstat(state.V2Path(root)); !os.IsNotExist(err) {
		t.Fatalf("entropy failure created state data: %v", err)
	}
}
