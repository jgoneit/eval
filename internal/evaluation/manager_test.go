package evaluation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jgoneit/eval/internal/store"
)

func managerFixture(t *testing.T) (Manager, Experiment) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	m := Manager{Root: filepath.Join(base, "state")}
	e, _, err := m.Init(context.Background(), Config{SchemaVersion: 1, Repositories: []string{repo}}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return m, e
}

func TestManagerCollectionPublicationRecovery(t *testing.T) {
	m, e := managerFixture(t)
	j, p, _ := m.stores(e.ID)
	before, err := j.Read()
	if err != nil {
		t.Fatal(err)
	}
	m.Options.Hooks.AfterTempSync = func(path string) error {
		if strings.Contains(path, "journal.jsonl") {
			return errors.New("injected crash before facts")
		}
		return nil
	}
	receipt, err := m.Collect(context.Background(), e.ID, e.StartedAt.Add(time.Hour))
	if err == nil || receipt.Committed {
		t.Fatalf("expected uncommitted failure: %+v %v", receipt, err)
	}
	after, _ := j.Read()
	if !bytes.Equal(before, after) {
		t.Fatal("facts changed before publication")
	}
	privateBefore, _ := p.Read()
	if !bytes.Contains(privateBefore, []byte(`"bindings"`)) {
		t.Fatal("private alias checkpoint missing")
	}
	m.Options.Hooks = store.Hooks{}
	receipt, err = m.Collect(context.Background(), e.ID, e.StartedAt.Add(2*time.Hour))
	if err != nil || !receipt.Committed {
		t.Fatalf("recovery failed: %+v %v", receipt, err)
	}
	privateAfter, _ := p.Read()
	if !bytes.Equal(privateBefore, privateAfter) {
		t.Fatal("orphan aliases were duplicated")
	}
	s, err := m.Load(e.ID)
	if err != nil || len(s.Receipts) != 1 {
		t.Fatalf("bad recovery snapshot: %v", err)
	}
	receipt, err = m.Collect(context.Background(), e.ID, e.StartedAt.Add(3*time.Hour))
	if err != nil || receipt.TasksAdded != 0 || receipt.EventsAdded != 0 {
		t.Fatalf("repeated collection inflated facts: %+v %v", receipt, err)
	}
}

func TestManagerReviewRevisionsSerialize(t *testing.T) {
	m, e := managerFixture(t)
	taskID, _ := NewID()
	j, _, _ := m.stores(e.ID)
	_, err := j.Update(context.Background(), func(old []byte) ([]byte, error) {
		return appendLine(old, Batch{Schema: Schema, At: e.StartedAt, Tasks: []Task{{ID: taskID, Revision: 1, Kind: "root", Profile: "unknown", Model: "unknown"}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Load(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReviewTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := m.Apply(context.Background(), e.ID, r, e.StartedAt.Add(time.Hour))
			results <- err == nil && c.Committed
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent revision committed %d times", successes)
	}
	s, err = m.Load(e.ID)
	if err != nil || len(s.Reviews) != 1 {
		t.Fatalf("unexpected review history: %v", err)
	}
	r, _ = ReviewTemplate(s)
	r.Tasks[0].Outcome = "ongoing"
	if _, err = m.Apply(context.Background(), e.ID, r, e.StartedAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, err = m.Load(e.ID)
	if err != nil || len(s.Tasks) != 1 || len(s.Reviews) != 2 {
		t.Fatalf("reopen inflated tasks or lost history: %v", err)
	}
}

func TestDecodeStrictAndConfigBoundaries(t *testing.T) {
	for _, input := range []string{`{"schema_version":1,"schema_version":2}`, `{"schema_version":1,"unknown":true}`, `{} {}`, `{"repositories":[{"a":1,"a":2}]}`} {
		var c Config
		if DecodeStrict([]byte(input), &c) == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if ValidateConfig(Config{SchemaVersion: 1, Repositories: []string{"relative"}}) == nil {
		t.Fatal("accepted relative source")
	}
	m, e := managerFixture(t)
	if _, err := m.Load("../" + e.ID); err == nil {
		t.Fatal("accepted experiment path escape")
	}
}

func TestUnconfirmedPublicationPreservesUsableExperiment(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	m := Manager{Root: filepath.Join(base, "state"), Options: store.Options{Hooks: store.Hooks{BeforeDirectorySync: func(string) error { return errors.New("simulated directory sync unavailable") }}}}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	e, c, err := m.Init(context.Background(), Config{SchemaVersion: 1, Repositories: []string{repo}}, now)
	if err == nil || !c.Committed || c.DurabilityConfirmed || e.ID == "" {
		t.Fatalf("publication was hidden: %+v %+v %v", e, c, err)
	}
	if _, err := m.Load(e.ID); err != nil {
		t.Fatalf("published experiment unusable: %v", err)
	}
	r, err := m.Collect(context.Background(), e.ID, now.Add(time.Hour))
	if err == nil || !r.Committed || r.Durability != "unconfirmed" {
		t.Fatalf("collection publication hidden: %+v %v", r, err)
	}
	s, err := m.Load(e.ID)
	if err != nil || len(s.Receipts) != 1 {
		t.Fatalf("cannot read unconfirmed publication: %v", err)
	}
}

func TestQuotaStopsBeforePrivatePublication(t *testing.T) {
	m, e := managerFixture(t)
	j, p, _ := m.stores(e.ID)
	pb, _ := p.Read()
	// A valid, near-limit private binding leaves no room for collection receipt.
	id, _ := NewID()
	padding := strings.Repeat("x", int(MaxBytes)-len(pb)-400)
	_, err := p.Update(context.Background(), func(old []byte) ([]byte, error) {
		return appendLine(old, PrivateBatch{Schema: Schema, Bindings: []Binding{{ID: id, Kind: "padding", Key: padding}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeJ, _ := j.Read()
	beforeP, _ := p.Read()
	receipt, err := m.Collect(context.Background(), e.ID, e.StartedAt.Add(time.Hour))
	if !errors.Is(err, ErrFull) || receipt.Committed {
		t.Fatalf("quota failed: %+v %v", receipt, err)
	}
	afterJ, _ := j.Read()
	afterP, _ := p.Read()
	if !bytes.Equal(beforeJ, afterJ) || !bytes.Equal(beforeP, afterP) {
		t.Fatal("quota changed persisted state")
	}
}
