package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testRelativeJournal = "jgoneit/eval-experiment/v1/journal.jsonl"

func TestUpdateRejectsGitWorktreeRoot(t *testing.T) {
	t.Parallel()
	root := privateTestRoot(t)
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	journal := mustStore(t, root, Options{})
	commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
}

func TestUpdateRejectsSymlinkComponent(t *testing.T) {
	t.Parallel()
	root := privateTestRoot(t)
	outside := t.TempDir()
	link := filepath.Join(root, "jgoneit")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	journal := mustStore(t, root, Options{})
	commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
}

func TestUpdateRejectsHardLinkedJournal(t *testing.T) {
	t.Parallel()
	root := privateTestRoot(t)
	journal := mustStore(t, root, Options{})
	if commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`)); err != nil || !commit.Committed {
		t.Fatalf("seed commit = %+v err = %v", commit, err)
	}
	alias := journal.Path() + ".alias"
	if err := os.Link(journal.Path(), alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	commit, err := journal.Update(context.Background(), appendObject(`{"value":2}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
}

func TestUpdateLockTimeoutPreservesJournal(t *testing.T) {
	root := privateTestRoot(t)
	journal := mustStore(t, root, Options{LockTimeout: 25 * time.Millisecond})
	if commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`)); err != nil || !commit.Committed {
		t.Fatalf("seed commit = %+v err = %v", commit, err)
	}
	before, err := os.ReadFile(journal.Path())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := openPrivateFile(journal.LockPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := acquireFileLock(context.Background(), lock, time.Second); err != nil {
		t.Fatal(err)
	}
	defer releaseFileLock(lock)
	commit, err := journal.Update(context.Background(), appendObject(`{"value":2}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryLockTimeout {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
	after, err := os.ReadFile(journal.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("journal changed under lock: before %q after %q", before, after)
	}
}

func TestUpdateAnchorsCommitWhenStateRootPathMoves(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := createPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "state-moved")
	journal := mustStore(t, root, Options{Hooks: Hooks{
		BeforeReplace: func(_, _ string) error {
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			return createPrivateDir(root)
		},
	}})
	commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`))
	if err != nil || !commit.Committed {
		t.Fatalf("commit = %+v err = %v", commit, err)
	}
	newPath := filepath.Join(root, filepath.FromSlash(testRelativeJournal))
	if _, err := os.Lstat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement root was mutated: %v", err)
	}
	movedPath := filepath.Join(moved, filepath.FromSlash(testRelativeJournal))
	if got, err := os.ReadFile(movedPath); err != nil || string(got) != "{\"value\":1}\n" {
		t.Fatalf("anchored journal = %q err = %v", got, err)
	}
}

func TestUpdateRequiresExactAppend(t *testing.T) {
	t.Parallel()
	for name, transaction := range map[string]Transaction{
		"empty": func([]byte) ([]byte, error) { return nil, nil },
		"twoRows": func(existing []byte) ([]byte, error) {
			return append(existing, []byte("{\"a\":1}\n{\"b\":2}\n")...), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := privateTestRoot(t)
			journal := mustStore(t, root, Options{})
			commit, err := journal.Update(context.Background(), transaction)
			if err == nil || commit.Committed || CategoryOf(err) != CategoryValidation {
				t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
			}
		})
	}
	t.Run("replaceExisting", func(t *testing.T) {
		root := privateTestRoot(t)
		journal := mustStore(t, root, Options{})
		if commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`)); err != nil || !commit.Committed {
			t.Fatalf("seed commit = %+v err = %v", commit, err)
		}
		commit, err := journal.Update(context.Background(), func([]byte) ([]byte, error) {
			return []byte("{\"other\":1}\n"), nil
		})
		if err == nil || commit.Committed || CategoryOf(err) != CategoryValidation {
			t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
		}
	})
}

func privateTestRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	if err := createPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustStore(t *testing.T, root string, options Options) *Store {
	t.Helper()
	journal, err := New(root, testRelativeJournal, options)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func appendObject(object string) Transaction {
	return func(existing []byte) ([]byte, error) {
		prospective := append([]byte(nil), existing...)
		prospective = append(prospective, object...)
		prospective = append(prospective, '\n')
		return prospective, nil
	}
}
