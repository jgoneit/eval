package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReadDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	s, err := New(root, "private/data.jsonl", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(); err == nil {
		t.Fatal("missing read succeeded")
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read created state")
	}
}
func TestExplicitStoreLimitAndPrivateArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	s, _ := New(root, "data.jsonl", Options{MaxBytes: 8})
	if _, err := s.Update(context.Background(), func(old []byte) ([]byte, error) { return []byte("{\"a\":1}\n"), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(context.Background(), func(old []byte) ([]byte, error) { return append(old, []byte("{}\n")...), nil }); err == nil {
		t.Fatal("accepted oversized write")
	}
	data, err := s.Read()
	if err != nil || string(data) != "{\"a\":1}\n" {
		t.Fatal("quota damaged state")
	}
	out := filepath.Join(t.TempDir(), "export")
	if commit, err := CreateArtifactBundle(out, map[string][]byte{"evidence.md": []byte("private evidence\n")}); err != nil || !commit.Committed {
		t.Fatal(err)
	}
	if _, err := CreateArtifactBundle(out, map[string][]byte{"evidence.md": []byte("overwrite")}); err == nil {
		t.Fatal("overwrote existing artifact")
	}
}

func TestArtifactBundleSecondFileFailureIsInvisibleAndRetryable(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "export")
	files := map[string][]byte{"review.json": []byte("{\"reviewer\":\"human\"}\n"), "evidence.md": []byte("private evidence\n")}
	writes := 0
	commit, err := CreateArtifactBundleWithHooks(out, files, Hooks{WriteTemp: func(file *os.File, content []byte) error {
		writes++
		if _, err := os.Lstat(out); !os.IsNotExist(err) {
			t.Fatal("partial bundle became visible")
		}
		if writes == 2 {
			if _, err := file.Write(content[:1]); err != nil {
				return err
			}
			return errors.New("injected interrupted second file")
		}
		return writeAll(file, content)
	}})
	if err == nil || commit.Committed || writes != 2 {
		t.Fatalf("commit=%+v err=%v writes=%d", commit, err, writes)
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("failure left visible destination")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed staging was not cleaned: %v %v", entries, err)
	}
	commit, err = CreateArtifactBundle(out, files)
	if err != nil || !commit.Committed {
		t.Fatalf("retry=%+v %v", commit, err)
	}
	assertArtifactBundle(t, out, files)
}

func TestArtifactBundleDoesNotReplaceRacingEmptyDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "export")
	var winner os.FileInfo
	commit, err := CreateArtifactBundleWithHooks(out, map[string][]byte{"review.json": []byte("{}")}, Hooks{BeforeReplace: func(_, target string) error {
		if err := createPrivateDir(target); err != nil {
			return err
		}
		winner, _ = os.Stat(target)
		return nil
	}})
	if err == nil || commit.Committed {
		t.Fatalf("replaced racing destination: %+v %v", commit, err)
	}
	after, err := os.Stat(out)
	if err != nil || !os.SameFile(winner, after) {
		t.Fatal("racing destination was changed")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote inside racing destination")
	}
}

func TestArtifactBundlePostPublicationFailureRetainsCompleteBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "export")
	files := map[string][]byte{"review.json": []byte("{}"), "evidence.md": []byte("private")}
	commit, err := CreateArtifactBundleWithHooks(out, files, Hooks{BeforeDirectorySync: func(string) error { return errors.New("sync unavailable") }})
	if err == nil || !commit.Committed || commit.DurabilityConfirmed {
		t.Fatalf("commit=%+v err=%v", commit, err)
	}
	assertArtifactBundle(t, out, files)
}

func TestArtifactBundleConcurrentPublicationHasOneWinner(t *testing.T) {
	out := filepath.Join(t.TempDir(), "export")
	files := map[string][]byte{"review.json": []byte("{}"), "evidence.md": []byte("private")}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			commit, err := CreateArtifactBundle(out, files)
			results <- err == nil && commit.Committed
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for success := range results {
		if success {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("publication winners=%d", winners)
	}
	assertArtifactBundle(t, out, files)
}

func TestArtifactBundleRejectsGitSymlinksAndUnsafeNames(t *testing.T) {
	files := map[string][]byte{"review.json": []byte("{}")}
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateArtifactBundle(filepath.Join(repository, "export"), files); err == nil {
		t.Fatal("allowed Git worktree export")
	}
	parent := t.TempDir()
	if _, err := CreateArtifactBundle(filepath.Join(parent, "export"), map[string][]byte{"../escape": []byte("secret")}); err == nil {
		t.Fatal("allowed escaping name")
	}
	if _, err := os.Stat(filepath.Join(parent, "export")); !os.IsNotExist(err) {
		t.Fatal("invalid input mutated export path")
	}
	target := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CreateArtifactBundle(filepath.Join(link, "export"), files); err == nil {
		t.Fatal("followed export parent symlink")
	}
	if _, err := CreateArtifactBundle(link, files); err == nil {
		t.Fatal("replaced export symlink")
	}
}

func assertArtifactBundle(t *testing.T, directory string, files map[string][]byte) {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDir(directory, info); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(files) {
		t.Fatalf("bundle entries=%v err=%v", entries, err)
	}
	for name, want := range files {
		path := filepath.Join(directory, name)
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("artifact %s incomplete: %v", name, err)
		}
		if err := inspectPrivateFile(path); err != nil {
			t.Fatal(err)
		}
	}
}
