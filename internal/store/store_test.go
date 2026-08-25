package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppendAndRead(t *testing.T) {
	store := testStore(t, t.TempDir(), Options{})
	want := []byte("{\"id\":1}\n")
	var validated []byte
	err := store.Append(context.Background(), []byte(`{"id":1}`), func(prospective []byte) error {
		validated = bytes.Clone(prospective)
		return validateTestJSONL(prospective)
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if !bytes.Equal(validated, want) {
		t.Fatalf("validator saw %q, want %q", validated, want)
	}

	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Read() = %q, want %q", got, want)
	}
	assertMode(t, filepath.Dir(store.Path()), 0o700)
	assertMode(t, store.Path(), 0o600)
	assertMode(t, store.LockPath(), 0o600)
}

func TestReadMissingDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-state")
	store := testStore(t, root, Options{})
	_, err := store.Read(context.Background())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read() error = %v, want fs.ErrNotExist", err)
	}
	if _, statErr := os.Lstat(root); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("Read() created state root: %v", statErr)
	}
}

func TestReadMissingFileDoesNotCreateLock(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, root, Options{})
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read() error = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Lstat(store.LockPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read() created lock: %v", err)
	}
}

func TestUpdateDerivesRevisionUnderLock(t *testing.T) {
	store := testStore(t, t.TempDir(), Options{LockTimeout: 5 * time.Second})
	const writers = 24
	var wg sync.WaitGroup
	errorsCh := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Update(context.Background(), func(existing []byte) ([]byte, error) {
				revision := bytes.Count(existing, []byte{'\n'}) + 1
				row := []byte(fmt.Sprintf(`{"revision":%d}`, revision))
				prospective := append(bytes.Clone(existing), row...)
				prospective = append(prospective, '\n')
				if err := validateTestJSONL(prospective); err != nil {
					return nil, err
				}
				return prospective, nil
			})
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
	}

	data, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var revisions []int
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		var row struct {
			Revision int `json:"revision"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, row.Revision)
	}
	sort.Ints(revisions)
	for index, revision := range revisions {
		if want := index + 1; revision != want {
			t.Fatalf("revisions = %v, missing %d", revisions, want)
		}
	}
}

func TestLockTimeoutHasStableCategory(t *testing.T) {
	root := t.TempDir()
	first := testStore(t, root, Options{LockTimeout: time.Second})
	second := testStore(t, root, Options{LockTimeout: 40 * time.Millisecond})
	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.Update(context.Background(), func(existing []byte) ([]byte, error) {
			close(locked)
			<-release
			return append(bytes.Clone(existing), []byte("{\"writer\":1}\n")...), nil
		})
	}()
	select {
	case <-locked:
	case err := <-firstDone:
		t.Fatalf("first Update() failed before locking: %v", err)
	case <-time.After(time.Second):
		t.Fatal("first Update() did not acquire lock")
	}

	err := second.Append(context.Background(), []byte(`{"writer":2}`), validateTestJSONL)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Append() error = %v, want ErrLockTimeout", err)
	}
	if category := CategoryOf(err); category != CategoryLockTimeout {
		t.Fatalf("CategoryOf() = %q, want %q", category, CategoryLockTimeout)
	}
	if reason := SafeReason(err); reason != "state-lock-timeout" {
		t.Fatalf("SafeReason() = %q", reason)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
}

func TestUpdateRejectsNonAppendTransactions(t *testing.T) {
	store := testStore(t, t.TempDir(), Options{})
	if err := store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); err != nil {
		t.Fatal(err)
	}
	tests := map[string]Transaction{
		"rewrite": func(existing []byte) ([]byte, error) { return []byte("{\"id\":2}\n"), nil },
		"two rows": func(existing []byte) ([]byte, error) {
			return append(existing, []byte("{\"id\":2}\n{\"id\":3}\n")...), nil
		},
		"not object": func(existing []byte) ([]byte, error) { return append(existing, []byte("[]\n")...), nil },
	}
	for name, transaction := range tests {
		t.Run(name, func(t *testing.T) {
			err := store.Update(context.Background(), transaction)
			if !errors.Is(err, ErrValidation) || CategoryOf(err) != CategoryValidation {
				t.Fatalf("Update() error = %v, want validation", err)
			}
		})
	}
}

func TestValidationFailureDoesNotReplaceLiveFile(t *testing.T) {
	store := testStore(t, t.TempDir(), Options{})
	if err := store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("semantic invalid")
	err = store.Append(context.Background(), []byte(`{"id":2}`), func([]byte) error { return injected })
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("Append() error = %v, want validation", err)
	}
	got, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("live file changed: got %q want %q", got, old)
	}
}

func TestUpdatePreservesTransactionError(t *testing.T) {
	store := testStore(t, t.TempDir(), Options{})
	injected := errors.New("transaction failed")
	err := store.Update(context.Background(), func([]byte) ([]byte, error) {
		return nil, injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("Update() error = %v, want preserved transaction error", err)
	}
	if errors.Is(err, ErrValidation) {
		t.Fatalf("Update() coerced transaction error to validation: %v", err)
	}
}

func TestFaultsLeaveOldOrCompleteNewFile(t *testing.T) {
	injected := errors.New("injected fault")
	tests := []struct {
		name      string
		hooks     Hooks
		wantNew   bool
		wantTemps bool
	}{
		{
			name: "partial temp write",
			hooks: Hooks{WriteTemp: func(file *os.File, prospective []byte) error {
				_, _ = file.Write(prospective[:len(prospective)/2])
				return injected
			}},
		},
		{name: "after temp write", hooks: Hooks{AfterTempWrite: func(string) error { return injected }}},
		{name: "after temp sync", hooks: Hooks{AfterTempSync: func(string) error { return injected }}},
		{name: "before replace", hooks: Hooks{BeforeReplace: func(string, string) error { return injected }}},
		{name: "after replace", hooks: Hooks{AfterReplace: func(string) error { return injected }}, wantNew: true},
		{name: "before directory sync", hooks: Hooks{BeforeDirectorySync: func(string) error { return injected }}, wantNew: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			seed := testStore(t, root, Options{})
			if err := seed.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(seed.Path())
			if err != nil {
				t.Fatal(err)
			}

			faulting := testStore(t, root, Options{Hooks: test.hooks})
			err = faulting.Append(context.Background(), []byte(`{"id":2}`), validateTestJSONL)
			if err == nil {
				t.Fatal("Append() unexpectedly succeeded")
			}
			got, readErr := os.ReadFile(seed.Path())
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := old
			if test.wantNew {
				want = []byte("{\"id\":1}\n{\"id\":2}\n")
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("live file = %q, want old-or-full %q", got, want)
			}
			matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(seed.Path()), ".observations.jsonl.tmp-*"))
			if globErr != nil {
				t.Fatal(globErr)
			}
			if len(matches) != 0 {
				t.Fatalf("orphan temp files = %v", matches)
			}
		})
	}
}

func TestUnsafePathsAreRejected(t *testing.T) {
	tests := []struct {
		root string
		rel  string
	}{
		{root: "relative", rel: "observations.jsonl"},
		{root: string(filepath.Separator), rel: "observations.jsonl"},
		{root: t.TempDir(), rel: "../observations.jsonl"},
		{root: t.TempDir() + string(filepath.Separator), rel: "observations.jsonl"},
	}
	for _, test := range tests {
		if _, err := New(test.root, test.rel, Options{}); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("New(%q, %q) error = %v, want unsafe", test.root, test.rel, err)
		}
	}
}

func TestGitWorktreePathIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "private-state")
	store := testStore(t, state, Options{})
	err := store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL)
	if !errors.Is(err, ErrUnsafePath) || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("Append() error = %v, want unsafe Git path", err)
	}
	if _, statErr := os.Lstat(state); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("unsafe path was created: %v", statErr)
	}
}

func TestSymlinkRootAndDataAreRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	realRoot := t.TempDir()
	linkedRoot := filepath.Join(filepath.Dir(realRoot), filepath.Base(realRoot)+"-link")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(linkedRoot) })
	store := testStore(t, linkedRoot, Options{})
	if err := store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink-root Append() error = %v, want unsafe", err)
	}

	root := t.TempDir()
	dataStore := testStore(t, root, Options{})
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Dir(dataStore.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dataStore.Path()); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink-data Append() error = %v, want unsafe", err)
	}
}

func TestIntermediateSymlinkIntoGitWorktreeIsRejectedWithoutWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows Store fails closed before filesystem access")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(base, "repository")
	subdirectory := filepath.Join(repository, "nested")
	physicalRoot := filepath.Join(subdirectory, "state")
	for _, directory := range []string{repository, filepath.Join(repository, ".git"), subdirectory, physicalRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "state-link")
	if err := os.Symlink(subdirectory, link); err != nil {
		t.Fatal(err)
	}
	logicalRoot := filepath.Join(link, "state")
	store, err := New(logicalRoot, filepath.Join("jgoneit", "eval", "v2", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL)
	if !errors.Is(err, ErrUnsafePath) || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("Append() error = %v, want unsafe path", err)
	}
	if _, err := os.Lstat(filepath.Join(physicalRoot, "jgoneit")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unsafe path was mutated: %v", err)
	}
}

func TestInsecurePermissionsAreRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go FileMode does not expose Windows DACL permissions")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	store, err := New(root, filepath.Join("v2", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL)
	if !errors.Is(err, ErrPermission) || CategoryOf(err) != CategoryPermission {
		t.Fatalf("Append() error = %v, want permission", err)
	}
	if reason := SafeReason(err); reason != "state-permission-denied" {
		t.Fatalf("SafeReason() = %q", reason)
	}
}

func TestOwnedReadOnlyStateRootAllowsPrivateSubdirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go FileMode does not expose Windows DACL permissions")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := New(root, filepath.Join("jgoneit", "eval", "v2", "observations.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), []byte(`{"id":1}`), validateTestJSONL); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	assertMode(t, filepath.Join(root, "jgoneit"), 0o700)
	assertMode(t, filepath.Join(root, "jgoneit", "eval"), 0o700)
	assertMode(t, filepath.Dir(store.Path()), 0o700)
}

func TestExistingFileWithoutTerminalNewlineIsRejected(t *testing.T) {
	root := t.TempDir()
	store := testStore(t, root, Options{})
	parent := filepath.Dir(store.Path())
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := store.Append(context.Background(), []byte(`{"id":2}`), validateTestJSONL)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("Append() error = %v, want validation", err)
	}
}

func TestBestEffortReasonNeverContainsPath(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "secret")
	err := storeError(CategoryPermission, "open", privatePath, ErrPermission)
	reason := SafeReason(err)
	if strings.Contains(reason, privatePath) || reason != "state-permission-denied" {
		t.Fatalf("SafeReason(%v) = %q", err, reason)
	}
}

func testStore(t *testing.T, root string, options Options) *Store {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Store fails closed on Windows until owner and DACL verification is available")
	}
	if info, err := os.Lstat(root); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatalf("Chmod(%s) error = %v", root, err)
		}
	}
	store, err := New(root, filepath.Join("v2", "observations.jsonl"), options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return store
}

func validateTestJSONL(data []byte) error {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return errors.New("missing final newline")
	}
	for index, line := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
		if len(line) == 0 || !json.Valid(line) {
			return fmt.Errorf("invalid JSON at line %d", index+1)
		}
		var value any
		if err := json.Unmarshal(line, &value); err != nil {
			return err
		}
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("line %d is not an object", index+1)
		}
	}
	return nil
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%s) = %04o, want %04o", path, got, want)
	}
}
