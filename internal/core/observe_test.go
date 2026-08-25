package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jgoneit/eval/internal/contract"
	"github.com/jgoneit/eval/internal/state"
)

func minimalDraft(taskID *string) []byte {
	value := map[string]any{
		"terminal_on": "2026-08-25", "population": "real", "task_type": "test",
		"agent": "codex", "model": nil, "host_os": "darwin", "modules": map[string]any{},
		"outcome":      map[string]any{"status": "completed", "user_interventions": nil, "module_interaction_time": nil, "rework_required": nil},
		"task_effects": map[string]any{},
	}
	if taskID != nil {
		value["task_id"] = *taskID
	}
	data, _ := json.Marshal(value)
	return data
}

func TestObserveGeneratesLinearCorrections(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	observer := Observer{Now: func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }}
	first, err := observer.Observe(context.Background(), root, minimalDraft(nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := observer.Observe(context.Background(), root, minimalDraft(&first.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || second.Revision != 2 || first.ObservationID == second.ObservationID {
		t.Fatalf("unexpected results: first=%#v second=%#v", first, second)
	}
	data, err := os.ReadFile(state.V2Path(root))
	if err != nil {
		t.Fatal(err)
	}
	parsed := contract.ParseJSONL("v2", data)
	validation := contract.ValidateLog(parsed)
	if !validation.Valid() || len(validation.ValidObservations) != 2 {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestObserveSerializesConcurrentCorrectionsForOneTask(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	observer := Observer{Now: func() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }}
	first, err := observer.Observe(context.Background(), root, minimalDraft(nil))
	if err != nil {
		t.Fatal(err)
	}

	const corrections = 8
	results := make(chan ObserveResult, corrections)
	errorsFound := make(chan error, corrections)
	var group sync.WaitGroup
	for range corrections {
		group.Add(1)
		go func() {
			defer group.Done()
			result, observeErr := observer.Observe(context.Background(), root, minimalDraft(&first.TaskID))
			if observeErr != nil {
				errorsFound <- observeErr
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for observeErr := range errorsFound {
		t.Errorf("concurrent correction failed: %v", observeErr)
	}
	if t.Failed() {
		return
	}

	revisions := make([]int, 0, corrections)
	for result := range results {
		revisions = append(revisions, result.Revision)
	}
	sort.Ints(revisions)
	for index, revision := range revisions {
		if want := index + 2; revision != want {
			t.Fatalf("revisions = %v, want contiguous 2..%d", revisions, corrections+1)
		}
	}

	data, err := os.ReadFile(state.V2Path(root))
	if err != nil {
		t.Fatal(err)
	}
	validation := contract.ValidateLog(contract.ParseJSONL("v2", data))
	if !validation.Valid() || len(validation.ValidObservations) != corrections+1 {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestObserveRejectsInvalidExistingLogWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(state.V2Path(root))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := state.V2Path(root)
	original := []byte("not-json\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	observer := Observer{Now: func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }}
	if _, err := observer.Observe(context.Background(), root, minimalDraft(nil)); err == nil {
		t.Fatal("Observe() succeeded with invalid existing log")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("existing log mutated: %q", after)
	}
}

func TestObserveRejectsCallerTaskIDWithoutExistingV2Chain(t *testing.T) {
	root := t.TempDir()
	taskID := "10000000-0000-4000-8000-000000009999"
	observer := Observer{Now: func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }}
	if _, err := observer.Observe(context.Background(), root, minimalDraft(&taskID)); !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("Observe() error = %v, want ErrInvalidDraft", err)
	}
	if _, err := os.Lstat(state.V2Path(root)); !os.IsNotExist(err) {
		t.Fatalf("unknown correction created data file: %v", err)
	}
}

func TestObserveIgnoresUnrelatedInvalidV1ForNewTask(t *testing.T) {
	root := t.TempDir()
	v1Path := state.V1Path(root)
	if err := os.MkdirAll(filepath.Dir(v1Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v1Path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	observer := Observer{Now: func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }}
	if _, err := observer.Observe(context.Background(), root, minimalDraft(nil)); err != nil {
		t.Fatalf("Observe() blocked by unrelated invalid v1: %v", err)
	}
}

func TestObserveClassifiesEntropyFailureAsOperational(t *testing.T) {
	root := t.TempDir()
	observer := Observer{
		Now:     func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) },
		NewUUID: func() (string, error) { return "", errors.New("entropy unavailable") },
	}
	if _, err := observer.Observe(context.Background(), root, minimalDraft(nil)); !errors.Is(err, ErrOperational) {
		t.Fatalf("Observe() error = %v, want ErrOperational", err)
	}
}

func TestObserveUsesHostLocalRecordedDateNearKSTMidnight(t *testing.T) {
	root := t.TempDir()
	kst := time.FixedZone("KST", 9*60*60)
	observer := Observer{Now: func() time.Time {
		return time.Date(2026, 8, 25, 0, 30, 0, 0, kst)
	}}
	if _, err := observer.Observe(context.Background(), root, minimalDraft(nil)); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	data, err := os.ReadFile(state.V2Path(root))
	if err != nil {
		t.Fatal(err)
	}
	validation := contract.ValidateLog(contract.ParseJSONL("v2", data))
	if !validation.Valid() || len(validation.ValidObservations) != 1 {
		t.Fatalf("validation = %#v", validation)
	}
	if got := validation.ValidObservations[0].RecordedOn; got != "2026-08-25" {
		t.Fatalf("recorded_on = %q, want Host-local 2026-08-25", got)
	}
}

func TestParseStoresRejectsSchemaInWrongVersionedFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "valid", "observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dataset := ParseStores(nil, data)
	if dataset.Validation.Valid() || dataset.Validation.InvalidRowCount == 0 {
		t.Fatalf("misplaced v1 rows accepted in v2 store: %#v", dataset.Validation)
	}
}

func TestReadStateAcceptsExistingEmptyStateRootWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dataset, err := ReadState(context.Background(), root)
	if err != nil {
		t.Fatalf("ReadState() error = %v", err)
	}
	if !dataset.Validation.Valid() || len(dataset.Rows) != 0 {
		t.Fatalf("dataset = %#v", dataset)
	}
	if _, err := os.Lstat(filepath.Join(root, "jgoneit")); !os.IsNotExist(err) {
		t.Fatalf("ReadState() created Eval state: %v", err)
	}
}

func TestAnalysisExclusionsPartitionLatestSyntheticAndSupersededRows(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "valid", "observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dataset := ParseStores(data, nil)
	records, exclusions := dataset.Analysis(nil, time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC))
	if len(records) != 0 || exclusions.SyntheticRows != 5 || exclusions.SupersededRows != 1 {
		t.Fatalf("records=%d exclusions=%#v", len(records), exclusions)
	}
	if exclusions.InvalidOrChainRows != 0 || exclusions.OutsideDateWindow != 0 {
		t.Fatalf("unexpected exclusions = %#v", exclusions)
	}
}

func TestAnalysisDisclosesRowsExcludedByInvalidChainPropagation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "invalid", "chains", "v2-schema-invalid-correction.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dataset := ParseStores(nil, data)
	records, exclusions := dataset.Analysis(nil, time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC))
	if len(records) != 0 || exclusions.InvalidRows != 1 || exclusions.InvalidChains != 1 || exclusions.InvalidOrChainRows != 2 {
		t.Fatalf("records=%d exclusions=%#v", len(records), exclusions)
	}
}
