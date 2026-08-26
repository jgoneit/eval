// Package store provides a private, single-writer, crash-atomic JSONL store.
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultLockTimeout = 2 * time.Second
	lockPollInterval   = 10 * time.Millisecond
	maxJournalBytes    = 256 << 10
)

type Transaction func(existing []byte) ([]byte, error)

type Hooks struct {
	WriteTemp           func(file *os.File, prospective []byte) error
	AfterTempWrite      func(tempPath string) error
	AfterTempSync       func(tempPath string) error
	BeforeReplace       func(tempPath, targetPath string) error
	AfterReplace        func(targetPath string) error
	BeforeDirectorySync func(directory string) error
}

type Options struct {
	LockTimeout time.Duration
	Hooks       Hooks
}

// Commit distinguishes pre-commit failures from failures after atomic replace.
type Commit struct {
	Committed           bool
	DurabilityConfirmed bool
}

type Store struct {
	root         string
	relativePath string
	relativeDir  string
	journalName  string
	lockName     string
	path         string
	lockPath     string
	privateDirs  []string
	lockTimeout  time.Duration
	hooks        Hooks
}

func New(stateRoot, relativePath string, options Options) (*Store, error) {
	if stateRoot == "" || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot || filepath.Dir(stateRoot) == stateRoot {
		return nil, storeError(CategoryUnsafePath, "configure", stateRoot, ErrUnsafePath)
	}
	root, err := normalizeSystemRootAlias(stateRoot)
	if err != nil {
		return nil, err
	}
	relative := filepath.FromSlash(relativePath)
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, storeError(CategoryUnsafePath, "configure", relativePath, ErrUnsafePath)
	}
	target := filepath.Join(root, relative)
	inside, err := filepath.Rel(root, target)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return nil, storeError(CategoryUnsafePath, "configure", target, ErrUnsafePath)
	}
	timeout := options.LockTimeout
	if timeout == 0 {
		timeout = DefaultLockTimeout
	}
	if timeout < 0 {
		return nil, storeError(CategoryUnsafePath, "configure", target, ErrUnsafePath)
	}
	directory := filepath.Dir(relative)
	var privateDirs []string
	if directory != "." {
		current := ""
		for _, component := range strings.Split(directory, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			privateDirs = append(privateDirs, current)
		}
	}
	return &Store{
		root: root, relativePath: relative, relativeDir: directory,
		journalName: filepath.Base(relative), lockName: filepath.Base(relative) + ".lock",
		path: target, lockPath: target + ".lock",
		privateDirs: privateDirs, lockTimeout: timeout, hooks: options.Hooks,
	}, nil
}

func (s *Store) Path() string     { return s.path }
func (s *Store) LockPath() string { return s.lockPath }

// Update holds the stable exclusive lock while deriving and validating a
// complete append-only journal. A committed result is authoritative even when
// the returned error means that final directory durability is unconfirmed.
func (s *Store) Update(ctx context.Context, transaction Transaction) (Commit, error) {
	if transaction == nil {
		return Commit{}, storeError(CategoryValidation, "update", s.path, ErrValidation)
	}
	root, err := s.prepare()
	if err != nil {
		return Commit{}, err
	}
	defer root.Close()

	lock, err := openOrCreatePrivateFile(s.lockPath)
	if err != nil {
		return Commit{}, err
	}
	defer lock.Close()
	if err := acquireFileLock(ctx, lock, s.lockTimeout); err != nil {
		return Commit{}, classifyLockError("write-lock", s.lockPath, err)
	}
	defer releaseFileLock(lock)

	if err := s.verifyTree(root); err != nil {
		return Commit{}, err
	}
	if err := inspectOpenPrivateFile(lock, s.lockPath); err != nil {
		return Commit{}, err
	}
	rootLock, err := root.OpenFile(s.relativePath+".lock", os.O_RDWR, 0)
	if err != nil {
		return Commit{}, classifyError("open-rooted-lock", s.lockPath, err)
	}
	if err := sameOpenFile(lock, rootLock, s.lockPath); err != nil {
		rootLock.Close()
		return Commit{}, err
	}
	rootLock.Close()

	journalRoot, err := root.OpenRoot(s.relativeDir)
	if err != nil {
		return Commit{}, classifyError("open-journal-root", filepath.Dir(s.path), err)
	}
	defer journalRoot.Close()
	existing, err := s.readExisting(journalRoot)
	if err != nil {
		return Commit{}, err
	}
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		return Commit{}, storeError(CategoryValidation, "read-journal", s.path, ErrValidation)
	}
	prospective, err := transaction(bytes.Clone(existing))
	if err != nil {
		return Commit{}, err
	}
	if err := validateAppendOnly(existing, prospective); err != nil {
		return Commit{}, err
	}
	return s.replaceWith(journalRoot, prospective)
}

func (s *Store) prepare() (*os.Root, error) {
	if err := rejectSymlinkComponents(s.root); err != nil {
		return nil, err
	}
	if err := rejectSymlinkComponents(s.path); err != nil {
		return nil, err
	}
	if err := rejectSymlinkComponents(s.lockPath); err != nil {
		return nil, err
	}
	if err := validateStateRootAncestors(s.root); err != nil {
		return nil, err
	}
	if err := rejectGitWorktree(s.root); err != nil {
		return nil, err
	}
	if err := ensureStateRoot(s.root); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, classifyError("open-state-root", s.root, err)
	}
	for _, relative := range s.privateDirs {
		absolute := filepath.Join(s.root, relative)
		info, statErr := root.Lstat(relative)
		if errors.Is(statErr, fs.ErrNotExist) {
			if err := createPrivateDir(absolute); err != nil && !errors.Is(err, fs.ErrExist) {
				root.Close()
				return nil, classifyError("create-private-directory", absolute, err)
			}
			info, statErr = root.Lstat(relative)
		}
		if statErr != nil {
			root.Close()
			return nil, classifyError("inspect-private-directory", absolute, statErr)
		}
		if err := validatePrivateDir(absolute, info); err != nil {
			root.Close()
			return nil, err
		}
	}
	if err := s.verifyDirectories(root); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func (s *Store) verifyTree(root *os.Root) error {
	if err := s.verifyDirectories(root); err != nil {
		return err
	}
	for _, pair := range []struct{ relative, absolute string }{
		{s.relativePath, s.path}, {s.relativePath + ".lock", s.lockPath},
	} {
		rootInfo, rootErr := root.Lstat(pair.relative)
		absoluteInfo, absoluteErr := os.Lstat(pair.absolute)
		if errors.Is(rootErr, fs.ErrNotExist) && errors.Is(absoluteErr, fs.ErrNotExist) {
			continue
		}
		if rootErr != nil || absoluteErr != nil || !os.SameFile(rootInfo, absoluteInfo) {
			return storeError(CategoryUnsafePath, "inspect-private-file-identity", pair.absolute, ErrUnsafePath)
		}
		if err := inspectPrivateFile(pair.absolute); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) verifyDirectories(root *os.Root) error {
	stateInfo, err := os.Lstat(s.root)
	if err != nil {
		return classifyError("inspect-state-root", s.root, err)
	}
	rootInfo, err := root.Lstat(".")
	if err != nil || !os.SameFile(stateInfo, rootInfo) {
		return storeError(CategoryUnsafePath, "inspect-state-root-identity", s.root, ErrUnsafePath)
	}
	if err := validateStateRoot(s.root, stateInfo); err != nil {
		return err
	}
	for _, relative := range s.privateDirs {
		info, err := root.Lstat(relative)
		if err != nil {
			return classifyError("inspect-private-directory", filepath.Join(s.root, relative), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return storeError(CategoryUnsafePath, "inspect-private-directory", filepath.Join(s.root, relative), ErrUnsafePath)
		}
		absolute := filepath.Join(s.root, relative)
		absoluteInfo, err := os.Lstat(absolute)
		if err != nil || !os.SameFile(info, absoluteInfo) {
			return storeError(CategoryUnsafePath, "inspect-private-directory-identity", absolute, ErrUnsafePath)
		}
		if err := validatePrivateDir(absolute, info); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) readExisting(root *os.Root) ([]byte, error) {
	rootInfo, rootErr := root.Lstat(s.journalName)
	absoluteInfo, absoluteErr := os.Lstat(s.path)
	if errors.Is(rootErr, fs.ErrNotExist) && errors.Is(absoluteErr, fs.ErrNotExist) {
		return nil, nil
	}
	if rootErr != nil || absoluteErr != nil || !os.SameFile(rootInfo, absoluteInfo) {
		return nil, storeError(CategoryUnsafePath, "inspect-private-file-identity", s.path, ErrUnsafePath)
	}
	if err := inspectPrivateFile(s.path); err != nil {
		return nil, err
	}
	file, err := root.Open(s.journalName)
	if err != nil {
		return nil, classifyError("open-journal", s.path, err)
	}
	defer file.Close()
	if err := inspectOpenPrivateFile(file, s.path); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxJournalBytes+1))
	if err != nil {
		return nil, classifyError("read-journal", s.path, err)
	}
	if len(data) > maxJournalBytes {
		return nil, storeError(CategoryValidation, "read-journal", s.path, ErrValidation)
	}
	return data, nil
}

func (s *Store) replaceWith(root *os.Root, prospective []byte) (result Commit, retErr error) {
	directory := filepath.Dir(s.path)
	temp, tempName, tempPath, err := createTemp(root, directory, filepath.Base(s.path))
	if err != nil {
		return Commit{}, classifyError("create-temp", directory, err)
	}
	committed := false
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if !committed {
			_ = root.Remove(tempName)
		}
	}()
	if err := inspectOpenPrivateFile(temp, tempPath); err != nil {
		return Commit{}, err
	}
	if hook := s.hooks.WriteTemp; hook != nil {
		err = hook(temp, bytes.Clone(prospective))
	} else {
		err = writeAll(temp, prospective)
	}
	if err != nil {
		return Commit{}, classifyError("write-temp", tempPath, err)
	}
	if hook := s.hooks.AfterTempWrite; hook != nil {
		if err := hook(tempPath); err != nil {
			return Commit{}, classifyError("after-temp-write", tempPath, err)
		}
	}
	if err := temp.Sync(); err != nil {
		return Commit{}, classifyError("sync-temp", tempPath, err)
	}
	if hook := s.hooks.AfterTempSync; hook != nil {
		if err := hook(tempPath); err != nil {
			return Commit{}, classifyError("after-temp-sync", tempPath, err)
		}
	}
	if err := temp.Close(); err != nil {
		return Commit{}, classifyError("close-temp", tempPath, err)
	}
	temp = nil
	if hook := s.hooks.BeforeReplace; hook != nil {
		if err := hook(tempPath, s.path); err != nil {
			return Commit{}, classifyError("before-replace", s.path, err)
		}
	}
	if err := root.Rename(tempName, s.journalName); err != nil {
		return Commit{}, classifyError("replace", s.path, err)
	}
	committed = true
	result = Commit{Committed: true}
	if hook := s.hooks.AfterReplace; hook != nil {
		if err := hook(s.path); err != nil {
			return Commit{Committed: true}, classifyError("after-replace", s.path, err)
		}
	}
	if hook := s.hooks.BeforeDirectorySync; hook != nil {
		if err := hook(directory); err != nil {
			return Commit{Committed: true}, classifyError("before-directory-sync", directory, err)
		}
	}
	directoryFile, err := root.Open(".")
	if err != nil {
		return Commit{Committed: true}, classifyError("open-directory-sync", directory, err)
	}
	defer directoryFile.Close()
	durable, err := syncDirectory(directoryFile)
	if err != nil {
		return Commit{Committed: true}, classifyError("sync-directory", directory, err)
	}
	return Commit{Committed: true, DurabilityConfirmed: durable}, nil
}

func createTemp(root *os.Root, directory, base string) (*os.File, string, string, error) {
	for attempts := 0; attempts < 100; attempts++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", "", err
		}
		name := "." + base + ".tmp-" + hex.EncodeToString(random[:])
		path := filepath.Join(directory, name)
		file, err := createPrivateTemp(path)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", "", err
		}
		rooted, err := root.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			file.Close()
			_ = os.Remove(path)
			return nil, "", "", err
		}
		if err := sameOpenFile(file, rooted, path); err != nil {
			rooted.Close()
			file.Close()
			_ = os.Remove(path)
			return nil, "", "", err
		}
		rooted.Close()
		return file, name, path, nil
	}
	return nil, "", "", fmt.Errorf("temporary name collision")
}

func sameOpenFile(left, right *os.File, path string) error {
	leftInfo, leftErr := left.Stat()
	rightInfo, rightErr := right.Stat()
	if leftErr != nil || rightErr != nil || !os.SameFile(leftInfo, rightInfo) {
		return storeError(CategoryUnsafePath, "inspect-rooted-file-identity", path, ErrUnsafePath)
	}
	return nil
}

func validateAppendOnly(existing, prospective []byte) error {
	if len(prospective) <= len(existing) || !bytes.Equal(existing, prospective[:len(existing)]) {
		return storeError(CategoryValidation, "validate-append", "", ErrValidation)
	}
	appended := prospective[len(existing):]
	if len(appended) < 3 || appended[len(appended)-1] != '\n' || bytes.Count(appended, []byte{'\n'}) != 1 {
		return storeError(CategoryValidation, "validate-append", "", ErrValidation)
	}
	row := appended[:len(appended)-1]
	if !bytes.Equal(row, bytes.TrimSpace(row)) || !json.Valid(row) || len(row) == 0 || row[0] != '{' {
		return storeError(CategoryValidation, "validate-append", "", ErrValidation)
	}
	return nil
}

func writeAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
