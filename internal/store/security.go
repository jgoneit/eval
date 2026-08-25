package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// rejectSymlinkComponents rejects symlinks anywhere in an absolute state path,
// not just at the final entry. Immutable system aliases are physically
// normalized by New before a Store is constructed.
func rejectSymlinkComponents(path string) error {
	if !filepath.IsAbs(path) {
		return storeError(CategoryUnsafePath, "symlink-check", path, ErrUnsafePath)
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(clean, root)
	current := root
	components := strings.Split(remainder, string(filepath.Separator))
	for index, component := range components {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return classifyError("symlink-check", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return storeError(CategoryUnsafePath, "symlink-check", current, ErrUnsafePath)
		}
		if index < len(components)-1 && !info.IsDir() {
			return storeError(CategoryUnsafePath, "symlink-check", current, ErrUnsafePath)
		}
	}
	return nil
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		return validatePrivateDir(path, info)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return classifyError("inspect-directory", path, err)
	}

	// Find a real existing ancestor first, then create every absent component
	// separately. This prevents MkdirAll from silently traversing a symlink.
	var missing []string
	current := path
	for {
		info, err = os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return storeError(CategoryUnsafePath, "create-directory", current, ErrUnsafePath)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return classifyError("inspect-directory", current, err)
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return storeError(CategoryUnsafePath, "create-directory", path, ErrUnsafePath)
		}
		current = parent
	}
	for index := len(missing) - 1; index >= 0; index-- {
		component := missing[index]
		if err := os.Mkdir(component, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return classifyError("create-directory", component, err)
		}
		createdInfo, err := os.Lstat(component)
		if err != nil {
			return classifyError("inspect-directory", component, err)
		}
		if err := validatePrivateDir(component, createdInfo); err != nil {
			return err
		}
	}
	return nil
}

func ensureStateRoot(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		return validateStateRoot(path, info)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return classifyError("inspect-state-root", path, err)
	}
	// A newly created state root is private even though an existing XDG state
	// root may intentionally be world-readable.
	return ensurePrivateDir(path)
}

func inspectStateRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-state-root", path, err)
	}
	return validateStateRoot(path, info)
}

func validateStateRoot(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return storeError(CategoryUnsafePath, "inspect-state-root", path, ErrUnsafePath)
	}
	if !stateRootMetadataOK(info) {
		return storeError(CategoryPermission, "inspect-state-root", path, ErrPermission)
	}
	return nil
}

func inspectPrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-directory", path, err)
	}
	return validatePrivateDir(path, info)
}

func validatePrivateDir(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return storeError(CategoryUnsafePath, "inspect-directory", path, ErrUnsafePath)
	}
	if !privateMetadataOK(info, 0o700) {
		return storeError(CategoryPermission, "inspect-directory", path, ErrPermission)
	}
	return nil
}

func inspectPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-file", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return storeError(CategoryUnsafePath, "inspect-file", path, ErrUnsafePath)
	}
	if !privateMetadataOK(info, 0o600) {
		return storeError(CategoryPermission, "inspect-file", path, ErrPermission)
	}
	return nil
}

func openExistingPrivateFile(path string) (*os.File, error) {
	if err := inspectPrivateFile(path); err != nil {
		return nil, err
	}
	file, err := openPrivateFile(path, true, false)
	if err != nil {
		return nil, err
	}
	if err := inspectOpenPrivateFile(file, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func openOrCreatePrivateFile(path string) (*os.File, error) {
	if err := inspectPrivateFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	file, err := openPrivateFile(path, true, true)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if err := inspectOpenPrivateFile(file, path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func readPrivateFile(path string) ([]byte, error) {
	if err := inspectPrivateFile(path); err != nil {
		return nil, err
	}
	file, err := openPrivateFile(path, false, false)
	if err != nil {
		return nil, classifyError("open-data", path, err)
	}
	defer file.Close()
	if err := inspectOpenPrivateFile(file, path); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, classifyError("read", path, err)
	}
	return data, nil
}

func inspectOpenPrivateFile(file *os.File, path string) error {
	openInfo, err := file.Stat()
	if err != nil {
		return classifyError("stat-open-file", path, err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-open-file", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openInfo, pathInfo) {
		return storeError(CategoryUnsafePath, "inspect-open-file", path, ErrUnsafePath)
	}
	if !openInfo.Mode().IsRegular() || !privateMetadataOK(openInfo, 0o600) {
		return storeError(CategoryPermission, "inspect-open-file", path, ErrPermission)
	}
	return nil
}

func rejectGitWorktree(path string) error {
	current := path
	for {
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return storeError(CategoryUnsafePath, "git-worktree-check", path, fmt.Errorf("Git worktree state is forbidden: %w", ErrUnsafePath))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return classifyError("git-worktree-check", marker, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
