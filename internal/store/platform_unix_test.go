//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

func TestUpdateRejectsWritableAncestor(t *testing.T) {
	parent := t.TempDir()
	shared := filepath.Join(parent, "shared")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(shared, "state")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := mustStore(t, root, Options{})
	commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryPermission {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
}

func TestUpdateRejectsBroadFileMode(t *testing.T) {
	root := t.TempDir()
	journal := mustStore(t, root, Options{})
	if commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`)); err != nil || !commit.Committed {
		t.Fatalf("seed commit = %+v err = %v", commit, err)
	}
	if err := os.Chmod(journal.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := journal.Update(context.Background(), appendObject(`{"value":2}`))
	if err == nil || commit.Committed || CategoryOf(err) != CategoryPermission {
		t.Fatalf("commit = %+v err = %v category = %s", commit, err, CategoryOf(err))
	}
}
