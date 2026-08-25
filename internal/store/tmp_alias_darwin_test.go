//go:build darwin

package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinTmpAliasUsesPrivatePhysicalChildAndRejectsNestedLink(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "eval-store-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}

	stateStore, err := New(root, filepath.Join("jgoneit", "eval", "v2", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatalf("New(/tmp/...) error = %v", err)
	}
	if !strings.HasPrefix(stateStore.Path(), "/private/tmp/") {
		t.Fatalf("Store path = %q, want physical /private/tmp path", stateStore.Path())
	}
	if err := stateStore.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); err != nil {
		t.Fatalf("Append(/tmp/...) error = %v", err)
	}

	physicalTarget := filepath.Join(root, "target")
	if err := os.Mkdir(physicalTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "caller-link")
	if err := os.Symlink(physicalTarget, link); err != nil {
		t.Fatal(err)
	}
	linkedStore, err := New(root, filepath.Join("caller-link", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = linkedStore.Append(context.Background(), []byte(`{"id":2}`), validateTestJSONL)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("nested-link Append() error = %v, want unsafe path", err)
	}
	if _, err := os.Lstat(filepath.Join(physicalTarget, "observations.jsonl")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("nested link target was mutated: %v", err)
	}
}

func TestDarwinTmpAliasRejectsWritableIntermediateAncestor(t *testing.T) {
	shared, err := os.MkdirTemp("/tmp", "eval-shared-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shared) })
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(shared, "private-root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}

	stateStore, err := New(root, filepath.Join("jgoneit", "eval", "v2", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = stateStore.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL)
	if !errors.Is(err, ErrPermission) || CategoryOf(err) != CategoryPermission {
		t.Fatalf("Append() error = %v, want unsafe ancestor permission failure", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "jgoneit")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("writable ancestor path was mutated: %v", err)
	}
}
