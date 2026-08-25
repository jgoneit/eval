// Package store provides a private, crash-atomic JSONL state store.
//
// Store is deliberately contract-agnostic. Callers provide one canonical JSON
// object and a validator that receives the complete prospective file while the
// writer holds the store's exclusive lock.
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
	"strings"
	"time"
)

const (
	// DefaultLockTimeout bounds waits for another Eval writer or reader.
	DefaultLockTimeout = 2 * time.Second
	lockPollInterval   = 10 * time.Millisecond
)

// Validator checks a complete prospective JSONL file. Append invokes it while
// holding the store's exclusive kernel-backed lock. The byte slice passed to a
// Validator is a private copy and may be retained or modified by the callback.
type Validator func(prospective []byte) error

// Transaction derives complete prospective JSONL bytes from the exact current
// bytes while the exclusive lock is held. It enables callers to assign
// revision and supersedes fields without a read/write race. The returned bytes
// must be an append-only extension containing exactly one new JSON object.
type Transaction func(existing []byte) (prospective []byte, err error)

// Hooks are fault-injection points used to verify crash-atomic behavior. They
// must not be configured in production code.
type Hooks struct {
	// WriteTemp replaces the normal complete write to the same-directory temp
	// file. Returning an error prevents replacement of the live file.
	WriteTemp func(file *os.File, prospective []byte) error
	// AfterTempWrite runs after the complete prospective bytes are written.
	AfterTempWrite func(tempPath string) error
	// AfterTempSync runs after the temp file is synced and before replacement.
	AfterTempSync func(tempPath string) error
	// BeforeReplace runs immediately before atomic replacement.
	BeforeReplace func(tempPath, targetPath string) error
	// AfterReplace runs after atomic replacement and before directory sync.
	AfterReplace func(targetPath string) error
	// BeforeDirectorySync runs immediately before syncing the parent directory.
	BeforeDirectorySync func(directory string) error
}

// Options controls store locking and test-only fault injection.
type Options struct {
	// LockTimeout defaults to DefaultLockTimeout when zero.
	LockTimeout time.Duration
	Hooks       Hooks
}

// Store is an immutable reference to one JSONL data file and its stable lock
// file. A Store is safe for concurrent use.
type Store struct {
	root        string
	privateRoot string
	path        string
	lockPath    string
	lockTimeout time.Duration
	hooks       Hooks
}

// New constructs a store beneath an absolute state root. relativePath must be
// a clean relative file path without traversal. Construction may normalize an
// immutable system root alias or fail a platform-security preflight, but it
// never creates state; mutable path checks are repeated by Read and Update.
func New(stateRoot, relativePath string, options Options) (*Store, error) {
	root, err := validateRootSyntax(stateRoot)
	if err != nil {
		return nil, err
	}
	root, err = normalizeSystemRootAlias(root)
	if err != nil {
		return nil, err
	}
	relative, err := validateRelativePath(relativePath)
	if err != nil {
		return nil, err
	}

	target := filepath.Join(root, relative)
	if !pathWithin(root, target) {
		return nil, storeError(CategoryUnsafePath, "configure", target, ErrUnsafePath)
	}
	privateRoot := root
	if relativeDir := filepath.Dir(relative); relativeDir != "." {
		firstComponent := strings.Split(relativeDir, string(filepath.Separator))[0]
		privateRoot = filepath.Join(root, firstComponent)
	}
	return newStore(root, privateRoot, target, options)
}

// NewPath constructs a store for an absolute target file path. Its parent is
// treated as the private state root.
func NewPath(targetPath string, options Options) (*Store, error) {
	if targetPath == "" || !filepath.IsAbs(targetPath) {
		return nil, storeError(CategoryUnsafePath, "configure", targetPath, ErrUnsafePath)
	}
	clean := filepath.Clean(targetPath)
	if clean != targetPath || filepath.Base(clean) == "." || filepath.Base(clean) == string(filepath.Separator) {
		return nil, storeError(CategoryUnsafePath, "configure", targetPath, ErrUnsafePath)
	}
	clean, err := normalizeSystemRootAlias(clean)
	if err != nil {
		return nil, err
	}
	root, err := validateRootSyntax(filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	return newStore(root, root, clean, options)
}

func newStore(root, privateRoot, target string, options Options) (*Store, error) {
	if err := platformSecurityCheck(target); err != nil {
		return nil, err
	}
	timeout := options.LockTimeout
	if timeout == 0 {
		timeout = DefaultLockTimeout
	}
	if timeout < 0 {
		return nil, storeError(CategoryUnsafePath, "configure", target, fmt.Errorf("negative lock timeout: %w", ErrUnsafePath))
	}
	return &Store{
		root:        root,
		privateRoot: privateRoot,
		path:        target,
		lockPath:    target + ".lock",
		lockTimeout: timeout,
		hooks:       options.Hooks,
	}, nil
}

// Path returns the absolute JSONL data path.
func (s *Store) Path() string { return s.path }

// LockPath returns the stable lock-file path.
func (s *Store) LockPath() string { return s.lockPath }

// Read returns a consistent snapshot. It never creates the root, parent
// directories, data file, or lock file. If the data file is absent, the
// returned error matches fs.ErrNotExist.
func (s *Store) Read(ctx context.Context) ([]byte, error) {
	if err := s.inspectForRead(); err != nil {
		return nil, err
	}

	lock, err := openExistingPrivateFile(s.lockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Atomic replacement means a lock-free read is still an old-or-new
			// snapshot. A valid writer always creates the stable lock first.
			return s.readData()
		}
		return nil, classifyError("open-lock", s.lockPath, err)
	}
	defer lock.Close()

	if err := acquireFileLock(ctx, lock, false, s.lockTimeout); err != nil {
		return nil, classifyLockError("read-lock", s.lockPath, err)
	}
	defer releaseFileLock(lock)

	if err := inspectPrivateFile(s.lockPath); err != nil {
		return nil, err
	}
	return s.readData()
}

// Append atomically appends one canonical JSON object and a newline. The live
// file is replaced only after the complete prospective bytes pass validation
// and the temp file is synced.
func (s *Store) Append(ctx context.Context, row []byte, validate Validator) error {
	if validate == nil {
		return storeError(CategoryValidation, "append", s.path, fmt.Errorf("validator required: %w", ErrValidation))
	}
	if err := validateRow(row); err != nil {
		return err
	}
	return s.Update(ctx, func(existing []byte) ([]byte, error) {
		prospective := make([]byte, 0, len(existing)+len(row)+1)
		prospective = append(prospective, existing...)
		prospective = append(prospective, row...)
		prospective = append(prospective, '\n')
		if err := validate(bytes.Clone(prospective)); err != nil {
			return nil, storeError(CategoryValidation, "validate", s.path, errors.Join(ErrValidation, err))
		}
		return prospective, nil
	})
}

// Update performs an append transaction under the exclusive store lock. The
// transaction receives the exact current bytes and may derive IDs, revision,
// and predecessor links from them before validating the complete prospective
// log. A transaction cannot alter or remove existing bytes.
func (s *Store) Update(ctx context.Context, transaction Transaction) error {
	if transaction == nil {
		return storeError(CategoryValidation, "update", s.path, fmt.Errorf("transaction required: %w", ErrValidation))
	}
	if err := s.prepareForWrite(); err != nil {
		return err
	}

	lock, err := openOrCreatePrivateFile(s.lockPath)
	if err != nil {
		return classifyError("open-lock", s.lockPath, err)
	}
	defer lock.Close()

	if err := acquireFileLock(ctx, lock, true, s.lockTimeout); err != nil {
		return classifyLockError("write-lock", s.lockPath, err)
	}
	defer releaseFileLock(lock)

	// Reinspect all caller-controlled paths after acquiring the stable lock.
	if err := s.inspectOwnedPaths(); err != nil {
		return err
	}
	if err := inspectOpenPrivateFile(lock, s.lockPath); err != nil {
		return err
	}

	existing, err := s.readExistingForAppend()
	if err != nil {
		return err
	}
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		return storeError(CategoryValidation, "update", s.path, fmt.Errorf("existing JSONL lacks terminal newline: %w", ErrValidation))
	}

	prospective, err := transaction(bytes.Clone(existing))
	if err != nil {
		return err
	}
	if err := validateAppendOnly(existing, prospective); err != nil {
		return err
	}
	return s.replaceWith(prospective)
}

func (s *Store) replaceWith(prospective []byte) (retErr error) {
	directory := filepath.Dir(s.path)
	temp, err := os.CreateTemp(directory, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return classifyError("create-temp", directory, err)
	}
	tempPath := temp.Name()
	replaced := false
	defer func() {
		if closeErr := temp.Close(); retErr == nil && closeErr != nil && !replaced {
			retErr = classifyError("close-temp", tempPath, closeErr)
		}
		if !replaced {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return classifyError("chmod-temp", tempPath, err)
	}
	if err := inspectOpenPrivateFile(temp, tempPath); err != nil {
		return err
	}

	if hook := s.hooks.WriteTemp; hook != nil {
		if err := hook(temp, bytes.Clone(prospective)); err != nil {
			return classifyError("write-temp", tempPath, err)
		}
	} else if err := writeAll(temp, prospective); err != nil {
		return classifyError("write-temp", tempPath, err)
	}
	if hook := s.hooks.AfterTempWrite; hook != nil {
		if err := hook(tempPath); err != nil {
			return classifyError("after-temp-write", tempPath, err)
		}
	}
	if err := temp.Sync(); err != nil {
		return classifyError("sync-temp", tempPath, err)
	}
	if hook := s.hooks.AfterTempSync; hook != nil {
		if err := hook(tempPath); err != nil {
			return classifyError("after-temp-sync", tempPath, err)
		}
	}
	if err := temp.Close(); err != nil {
		return classifyError("close-temp", tempPath, err)
	}
	if hook := s.hooks.BeforeReplace; hook != nil {
		if err := hook(tempPath, s.path); err != nil {
			return classifyError("before-replace", s.path, err)
		}
	}
	if err := atomicReplace(tempPath, s.path); err != nil {
		return classifyError("replace", s.path, err)
	}
	replaced = true
	if hook := s.hooks.AfterReplace; hook != nil {
		if err := hook(s.path); err != nil {
			return classifyError("after-replace", s.path, err)
		}
	}
	if hook := s.hooks.BeforeDirectorySync; hook != nil {
		if err := hook(directory); err != nil {
			return classifyError("before-directory-sync", directory, err)
		}
	}
	if err := syncDirectory(directory); err != nil {
		return classifyError("sync-directory", directory, err)
	}
	return nil
}

func (s *Store) prepareForWrite() error {
	if err := rejectSymlinkComponents(s.path); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(s.lockPath); err != nil {
		return err
	}
	if err := validateStateRootAncestors(s.root); err != nil {
		return err
	}
	if err := rejectGitWorktree(s.root); err != nil {
		return err
	}
	if s.privateRoot == s.root {
		if err := ensurePrivateDir(s.root); err != nil {
			return err
		}
	} else {
		if err := ensureStateRoot(s.root); err != nil {
			return err
		}
		if err := ensurePrivateDir(s.privateRoot); err != nil {
			return err
		}
	}

	relativeParent, err := filepath.Rel(s.privateRoot, filepath.Dir(s.path))
	if err != nil || relativeParent == ".." || strings.HasPrefix(relativeParent, ".."+string(filepath.Separator)) {
		return storeError(CategoryUnsafePath, "prepare", s.path, ErrUnsafePath)
	}
	current := s.privateRoot
	if relativeParent != "." {
		for _, component := range strings.Split(relativeParent, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			if err := ensurePrivateDir(current); err != nil {
				return err
			}
		}
	}
	return s.inspectOwnedPaths()
}

func (s *Store) inspectForRead() error {
	if err := rejectSymlinkComponents(s.path); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(s.lockPath); err != nil {
		return err
	}
	if err := validateStateRootAncestors(s.root); err != nil {
		return err
	}
	if err := rejectGitWorktree(s.root); err != nil {
		return err
	}
	if s.privateRoot == s.root {
		if err := inspectPrivateDir(s.root); err != nil {
			return err
		}
	} else {
		if err := inspectStateRoot(s.root); err != nil {
			return err
		}
		if err := inspectPrivateDir(s.privateRoot); err != nil {
			return err
		}
	}
	if err := s.inspectParentDirs(); err != nil {
		return err
	}
	return inspectPrivateFile(s.path)
}

func (s *Store) inspectOwnedPaths() error {
	if err := rejectSymlinkComponents(s.path); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(s.lockPath); err != nil {
		return err
	}
	if err := validateStateRootAncestors(s.root); err != nil {
		return err
	}
	if err := rejectGitWorktree(s.root); err != nil {
		return err
	}
	if s.privateRoot == s.root {
		if err := inspectPrivateDir(s.root); err != nil {
			return err
		}
	} else {
		if err := inspectStateRoot(s.root); err != nil {
			return err
		}
		if err := inspectPrivateDir(s.privateRoot); err != nil {
			return err
		}
	}
	if err := s.inspectParentDirs(); err != nil {
		return err
	}
	if err := inspectPrivateFile(s.path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := inspectPrivateFile(s.lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) inspectParentDirs() error {
	relativeParent, err := filepath.Rel(s.privateRoot, filepath.Dir(s.path))
	if err != nil || relativeParent == ".." || strings.HasPrefix(relativeParent, ".."+string(filepath.Separator)) {
		return storeError(CategoryUnsafePath, "inspect", s.path, ErrUnsafePath)
	}
	current := s.privateRoot
	if relativeParent == "." {
		return nil
	}
	for _, component := range strings.Split(relativeParent, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		if err := inspectPrivateDir(current); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) readData() ([]byte, error) {
	return readPrivateFile(s.path)
}

func (s *Store) readExistingForAppend() ([]byte, error) {
	if err := inspectPrivateFile(s.path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return readPrivateFile(s.path)
}

func validateRow(row []byte) error {
	if len(row) == 0 || !bytes.Equal(row, bytes.TrimSpace(row)) || bytes.ContainsAny(row, "\r\n") || !json.Valid(row) {
		return storeError(CategoryValidation, "validate-row", "", fmt.Errorf("row must be one canonical JSON value without surrounding whitespace: %w", ErrValidation))
	}
	var value any
	if err := json.Unmarshal(row, &value); err != nil {
		return storeError(CategoryValidation, "validate-row", "", fmt.Errorf("%w: %v", ErrValidation, err))
	}
	if _, ok := value.(map[string]any); !ok {
		return storeError(CategoryValidation, "validate-row", "", fmt.Errorf("row must be a JSON object: %w", ErrValidation))
	}
	return nil
}

func validateAppendOnly(existing, prospective []byte) error {
	if len(prospective) <= len(existing) || !bytes.Equal(existing, prospective[:len(existing)]) {
		return storeError(CategoryValidation, "validate-transaction", "", fmt.Errorf("transaction must preserve exact existing bytes: %w", ErrValidation))
	}
	appended := prospective[len(existing):]
	if len(appended) < 2 || appended[len(appended)-1] != '\n' || bytes.Count(appended, []byte{'\n'}) != 1 {
		return storeError(CategoryValidation, "validate-transaction", "", fmt.Errorf("transaction must append exactly one JSONL row: %w", ErrValidation))
	}
	return validateRow(appended[:len(appended)-1])
}

func validateRootSyntax(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", storeError(CategoryUnsafePath, "configure", root, ErrUnsafePath)
	}
	clean := filepath.Clean(root)
	if clean != root || isFilesystemRoot(clean) {
		return "", storeError(CategoryUnsafePath, "configure", root, ErrUnsafePath)
	}
	return clean, nil
}

func validateRelativePath(relative string) (string, error) {
	normalized := filepath.FromSlash(relative)
	if normalized == "" || filepath.IsAbs(normalized) || filepath.Clean(normalized) != normalized || normalized == "." {
		return "", storeError(CategoryUnsafePath, "configure", relative, ErrUnsafePath)
	}
	if normalized == ".." || strings.HasPrefix(normalized, ".."+string(filepath.Separator)) {
		return "", storeError(CategoryUnsafePath, "configure", relative, ErrUnsafePath)
	}
	return normalized, nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isFilesystemRoot(path string) bool {
	parent := filepath.Dir(path)
	return parent == path
}

func writeAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return errors.New("zero-byte write")
		}
		data = data[written:]
	}
	return nil
}
