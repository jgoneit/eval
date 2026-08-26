package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jgoneit/eval/internal/experiment"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
)

const validDraft = `{"outcome":"completed","rework_required":false,"ward":{"used":true,"version":null,"defects_caught_before_terminal":1,"added_user_interventions":0,"interaction_seconds":2,"normal_work_blocked":false},"seal":{"used":false,"version":null}}`

type capturedResult struct {
	Status     string `json:"status"`
	Slot       int64  `json:"slot"`
	Durability string `json:"durability"`
	Reason     string `json:"reason"`
}

func TestVersionAndUsageSurface(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"--version"}, Runtime{Stdout: &stdout, Stderr: &stderr}); exit != ExitSuccess {
		t.Fatalf("--version exit = %d", exit)
	}
	if got, want := stdout.String(), "evalctl 0.2.0-experiment.1\n"; got != want {
		t.Fatalf("--version = %q, want %q", got, want)
	}
	for _, args := range [][]string{{}, {"validate"}, {"summarize"}, {"compare"}, {"observe", "extra"}, {"--version", "extra"}} {
		stdout.Reset()
		stderr.Reset()
		if exit := Run(context.Background(), args, Runtime{Stdout: &stdout, Stderr: &stderr}); exit != ExitUsage {
			t.Fatalf("Run(%q) exit = %d, want %d", args, exit, ExitUsage)
		}
	}
}

func TestObserveRecordsCanonicalRow(t *testing.T) {
	t.Parallel()
	root := testStateRoot(t)
	result, exit := captureObserve(t, root, validDraft, store.Options{})
	if exit != ExitSuccess || result.Status != "recorded" || result.Slot != 1 {
		t.Fatalf("observe = %+v exit %d", result, exit)
	}
	if result.Durability != "confirmed" && result.Durability != "unconfirmed" {
		t.Fatalf("durability = %q", result.Durability)
	}
	data, err := os.ReadFile(state.JournalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := experiment.ParseJournal(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Slot != 1 || rows[0].Ward.Version != nil || !rows[0].Ward.Used {
		t.Fatalf("stored rows = %#v", rows)
	}
}

func TestObserveInvalidInputNeverCreatesJournal(t *testing.T) {
	t.Parallel()
	for name, draft := range map[string]string{
		"malformed":  `{`,
		"duplicate":  `{"outcome":"completed","outcome":"failed","ward":{"used":false,"version":null},"seal":{"used":false,"version":null}}`,
		"fractional": `{"outcome":"completed","ward":{"used":true,"version":null,"interaction_seconds":1.5},"seal":{"used":false,"version":null}}`,
		"unusedEffect": `{"outcome":"completed","ward":{"used":false,"version":null,"interaction_seconds":1},` +
			`"seal":{"used":false,"version":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := testStateRoot(t)
			result, exit := captureObserve(t, root, draft, store.Options{})
			if exit != ExitSuccess || result.Status != "skipped" || result.Reason != "invalid-observation" {
				t.Fatalf("observe = %+v exit %d", result, exit)
			}
			if _, err := os.Lstat(state.JournalPath(root)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal exists after invalid input: %v", err)
			}
		})
	}
}

func TestObserveConcurrentSlotsAndBound(t *testing.T) {
	root := testStateRoot(t)
	results := make(chan capturedResult, experiment.MaxRows)
	var wait sync.WaitGroup
	for index := int64(0); index < experiment.MaxRows; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, exit := captureObserve(t, root, validDraft, store.Options{})
			if exit != ExitSuccess {
				t.Errorf("observe exit = %d", exit)
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	var slots []int
	for result := range results {
		if result.Status != "recorded" {
			t.Fatalf("concurrent result = %+v", result)
		}
		slots = append(slots, int(result.Slot))
	}
	sort.Ints(slots)
	for index, slot := range slots {
		if slot != index+1 {
			t.Fatalf("slots = %v", slots)
		}
	}

	result, exit := captureObserve(t, root, validDraft, store.Options{})
	if exit != ExitSuccess || result.Status != "skipped" || result.Reason != "journal-full" {
		t.Fatalf("twenty-first observe = %+v exit %d", result, exit)
	}
	data, err := os.ReadFile(state.JournalPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := experiment.ParseJournal(bytes.NewReader(data)); err != nil || len(rows) != int(experiment.MaxRows) {
		t.Fatalf("final journal rows = %d, err = %v", len(rows), err)
	}
}

func TestObserveInvalidJournalIsPreserved(t *testing.T) {
	t.Parallel()
	root := testStateRoot(t)
	if result, _ := captureObserve(t, root, validDraft, store.Options{}); result.Status != "recorded" {
		t.Fatalf("seed result = %+v", result)
	}
	path := state.JournalPath(root)
	invalid := []byte("{\"schema_version\":\"wrong\"}\n")
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	result, exit := captureObserve(t, root, validDraft, store.Options{})
	if exit != ExitSuccess || result.Status != "skipped" || result.Reason != "invalid-state-data" {
		t.Fatalf("invalid journal result = %+v exit %d", result, exit)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, invalid) {
		t.Fatalf("invalid journal changed: %q", got)
	}
}

func TestObserveFaultBoundaries(t *testing.T) {
	t.Parallel()
	precommit := map[string]store.Hooks{
		"partialWrite": {
			WriteTemp: func(file *os.File, _ []byte) error {
				_, _ = file.WriteString("{")
				return io.ErrUnexpectedEOF
			},
		},
		"beforeReplace": {BeforeReplace: func(_, _ string) error { return errors.New("injected") }},
	}
	for name, hooks := range precommit {
		t.Run(name, func(t *testing.T) {
			root := testStateRoot(t)
			result, exit := captureObserve(t, root, validDraft, store.Options{Hooks: hooks})
			if exit != ExitSuccess || result.Status != "skipped" || result.Reason != "state-io-error" {
				t.Fatalf("result = %+v exit %d", result, exit)
			}
			if _, err := os.Lstat(state.JournalPath(root)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal exists after precommit failure: %v", err)
			}
		})
	}

	postcommit := map[string]store.Hooks{
		"afterReplace":  {AfterReplace: func(string) error { return errors.New("injected") }},
		"directorySync": {BeforeDirectorySync: func(string) error { return errors.New("injected") }},
	}
	for name, hooks := range postcommit {
		t.Run(name, func(t *testing.T) {
			root := testStateRoot(t)
			result, exit := captureObserve(t, root, validDraft, store.Options{Hooks: hooks})
			if exit != ExitSuccess || result.Status != "recorded" || result.Slot != 1 || result.Durability != "unconfirmed" {
				t.Fatalf("result = %+v exit %d", result, exit)
			}
			if _, err := os.Stat(state.JournalPath(root)); err != nil {
				t.Fatalf("committed journal missing: %v", err)
			}
		})
	}
}

func TestObservePrecommitFaultPreservesExistingJournal(t *testing.T) {
	t.Parallel()
	for name, hooks := range map[string]store.Hooks{
		"partialWrite": {
			WriteTemp: func(file *os.File, _ []byte) error {
				_, _ = file.WriteString("partial")
				return io.ErrUnexpectedEOF
			},
		},
		"beforeReplace": {BeforeReplace: func(_, _ string) error { return errors.New("injected") }},
	} {
		t.Run(name, func(t *testing.T) {
			root := testStateRoot(t)
			if result, _ := captureObserve(t, root, validDraft, store.Options{}); result.Status != "recorded" {
				t.Fatalf("seed result = %+v", result)
			}
			path := state.JournalPath(root)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result, _ := captureObserve(t, root, validDraft, store.Options{Hooks: hooks})
			if result.Status != "skipped" {
				t.Fatalf("fault result = %+v", result)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("journal changed: before %q after %q", before, after)
			}
		})
	}
}

func captureObserve(t *testing.T, root, draft string, options store.Options) (capturedResult, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"observe", "--state-root", root}, Runtime{
		Stdin: strings.NewReader(draft), Stdout: &stdout, Stderr: &stderr,
		Now:          func() time.Time { return time.Date(2026, 8, 26, 12, 34, 56, 123, time.UTC) },
		StoreOptions: options,
	})
	var result capturedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %q: %v (stderr %q)", stdout.String(), err, stderr.String())
	}
	return result, exit
}

func testStateRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state")
}

func TestStateRootMustBeAbsoluteButFailureIsBestEffort(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	exit := Run(context.Background(), []string{"observe", "--state-root", filepath.Join("relative", "state")}, Runtime{
		Stdin: strings.NewReader(validDraft), Stdout: &stdout, Stderr: io.Discard,
	})
	if exit != ExitSuccess || !strings.Contains(stdout.String(), `"reason":"unsafe-state-path"`) {
		t.Fatalf("exit = %d output = %q", exit, stdout.String())
	}
}
