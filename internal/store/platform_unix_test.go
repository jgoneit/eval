//go:build darwin || linux

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
