package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CreateArtifactBundle publishes one complete private directory without
// replacing an existing destination. Post-publication failures retain the
// complete bundle and return Committed with unconfirmed durability.
func CreateArtifactBundle(directory string, files map[string][]byte) (Commit, error) {
	return CreateArtifactBundleWithHooks(directory, files, Hooks{})
}

// CreateArtifactBundleWithHooks preserves the bundle publication contract while
// allowing callers to check cancellation and inject publication failures.
func CreateArtifactBundleWithHooks(directory string, files map[string][]byte, hooks Hooks) (result Commit, retErr error) {
	if len(files) == 0 || len(files) > 32 {
		return Commit{}, ErrValidation
	}
	names := make([]string, 0, len(files))
	var total int64
	for name, content := range files {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
			return Commit{}, ErrUnsafePath
		}
		total += int64(len(content))
		if total > 64<<20 {
			return Commit{}, ErrValidation
		}
		names = append(names, name)
	}
	sort.Strings(names)
	s, err := New(directory, names[0], Options{MaxBytes: 64 << 20})
	if err != nil {
		return Commit{}, err
	}
	directory = s.root
	for _, check := range []func(string) error{rejectSymlinkComponents, validateStateRootAncestors, rejectGitWorktree} {
		if err := check(directory); err != nil {
			return Commit{}, err
		}
	}
	if _, err := os.Lstat(directory); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = fs.ErrExist
		}
		return Commit{}, classifyError("inspect-export-destination", directory, err)
	}
	parentPath, finalName := filepath.Dir(directory), filepath.Base(directory)
	if err := createPrivatePath(parentPath); err != nil {
		return Commit{}, err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return Commit{}, classifyError("open-export-parent", directory, err)
	}
	defer parent.Close()
	if err := artifactParentBinding(parent, parentPath); err != nil {
		return Commit{}, err
	}
	var stagingName, stagingPath string
	for attempt := 0; attempt < 100; attempt++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Commit{}, err
		}
		stagingName = ".eval-review.tmp-" + hex.EncodeToString(random[:])
		stagingPath = filepath.Join(parentPath, stagingName)
		if err = createArtifactDirectory(stagingPath); errors.Is(err, fs.ErrExist) {
			continue
		}
		break
	}
	if err != nil {
		return Commit{}, classifyError("create-export-staging", stagingPath, err)
	}
	stagingInfo, err := parent.Lstat(stagingName)
	if err != nil {
		return Commit{}, err
	}
	if err := validatePrivateDir(stagingPath, stagingInfo); err != nil {
		return Commit{}, err
	}
	staging, err := parent.OpenRoot(stagingName)
	if err != nil {
		return Commit{}, err
	}
	created := make(map[string]fs.FileInfo)
	defer func() {
		if !result.Committed {
			retErr = errors.Join(retErr, cleanupArtifactStaging(parent, staging, stagingName, stagingInfo, created))
		} else {
			_ = staging.Close()
		}
	}()
	openedInfo, err := staging.Stat(".")
	if err != nil || !os.SameFile(stagingInfo, openedInfo) {
		return Commit{}, ErrUnsafePath
	}
	for _, name := range names {
		path := filepath.Join(stagingPath, name)
		f, err := createPrivateTemp(path)
		if err != nil {
			return Commit{}, classifyError("create-export-file", path, err)
		}
		info, statErr := f.Stat()
		if statErr == nil {
			created[name] = info
		}
		writeErr := func() error {
			if statErr != nil {
				return statErr
			}
			if err := inspectOpenPrivateFile(f, path); err != nil {
				return err
			}
			rooted, err := staging.Open(name)
			if err != nil {
				return err
			}
			err = sameOpenFile(f, rooted, path)
			rooted.Close()
			if err != nil {
				return err
			}
			if hooks.WriteTemp != nil {
				err = hooks.WriteTemp(f, files[name])
			} else {
				err = writeAll(f, files[name])
			}
			if err != nil {
				return err
			}
			if hooks.AfterTempWrite != nil {
				if err := hooks.AfterTempWrite(path); err != nil {
					return err
				}
			}
			if err := f.Sync(); err != nil {
				return err
			}
			if hooks.AfterTempSync != nil {
				return hooks.AfterTempSync(path)
			}
			return nil
		}()
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return Commit{}, classifyError("write-export-file", path, errors.Join(writeErr, closeErr))
		}
	}
	stagingDirectory, err := staging.Open(".")
	if err != nil {
		return Commit{}, err
	}
	stagingDurable, syncErr := syncDirectory(stagingDirectory)
	stagingDirectory.Close()
	if syncErr != nil {
		return Commit{}, syncErr
	}
	if hooks.BeforeReplace != nil {
		if err := hooks.BeforeReplace(stagingPath, directory); err != nil {
			return Commit{}, err
		}
	}
	if err := artifactParentBinding(parent, parentPath); err != nil {
		return Commit{}, err
	}
	namedInfo, err := parent.Lstat(stagingName)
	if err != nil || namedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(stagingInfo, namedInfo) {
		return Commit{}, ErrUnsafePath
	}
	if err := publishArtifactDirectory(parent, stagingName, finalName, stagingInfo); err != nil {
		return Commit{}, classifyError("publish-export", directory, err)
	}
	result.Committed = true
	if hooks.AfterReplace != nil {
		if err := hooks.AfterReplace(directory); err != nil {
			return result, err
		}
	}
	if hooks.BeforeDirectorySync != nil {
		if err := hooks.BeforeDirectorySync(parentPath); err != nil {
			return result, err
		}
	}
	parentDirectory, err := parent.Open(".")
	if err != nil {
		return result, err
	}
	defer parentDirectory.Close()
	parentDurable, err := syncDirectory(parentDirectory)
	result.DurabilityConfirmed = err == nil && stagingDurable && parentDurable
	return result, err
}

func artifactParentBinding(parent *os.Root, path string) error {
	if err := rejectSymlinkComponents(path); err != nil {
		return err
	}
	if err := rejectGitWorktree(path); err != nil {
		return err
	}
	named, err := os.Lstat(path)
	if err != nil {
		return err
	}
	opened, err := parent.Stat(".")
	if err != nil || !os.SameFile(named, opened) {
		return ErrUnsafePath
	}
	return validateStateRootAncestors(path)
}

func cleanupArtifactStaging(parent, staging *os.Root, name string, expected fs.FileInfo, created map[string]fs.FileInfo) error {
	var cleanupErr error
	for filename, expectedFile := range created {
		current, err := staging.Lstat(filename)
		if err == nil && os.SameFile(expectedFile, current) {
			cleanupErr = errors.Join(cleanupErr, staging.Remove(filename))
		}
	}
	staging.Close()
	named, err := parent.Lstat(name)
	if err == nil && named.Mode()&os.ModeSymlink == 0 && os.SameFile(expected, named) {
		cleanupErr = errors.Join(cleanupErr, parent.Remove(name))
	}
	return cleanupErr
}
