package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func rejectSymlinkComponents(path string) error {
	if !filepath.IsAbs(path) {
		return storeError(CategoryUnsafePath, "symlink-check", path, ErrUnsafePath)
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(clean, root)
	current := root
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
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
	return createPrivatePath(path)
}

func createPrivatePath(path string) error {
	var missing []string
	current := path
	for {
		info, err := os.Lstat(current)
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
		if err := createPrivateDir(component); err != nil && !errors.Is(err, fs.ErrExist) {
			return classifyError("create-directory", component, err)
		}
		info, err := os.Lstat(component)
		if err != nil {
			return classifyError("inspect-directory", component, err)
		}
		if err := validatePrivateDir(component, info); err != nil {
			return err
		}
	}
	return nil
}

func validateStateRoot(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return storeError(CategoryUnsafePath, "inspect-state-root", path, ErrUnsafePath)
	}
	return checkStateRoot(path, info)
}

func validatePrivateDir(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return storeError(CategoryUnsafePath, "inspect-directory", path, ErrUnsafePath)
	}
	return checkPrivatePath(path, info, 0o700)
}

func inspectPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-file", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return storeError(CategoryUnsafePath, "inspect-file", path, ErrUnsafePath)
	}
	return checkPrivatePath(path, info, 0o600)
}

func openOrCreatePrivateFile(path string) (*os.File, error) {
	if err := inspectPrivateFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	file, err := openPrivateFile(path, true, true)
	if err != nil {
		return nil, classifyError("open-private-file", path, err)
	}
	if err := inspectOpenPrivateFile(file, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
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
	if !openInfo.Mode().IsRegular() {
		return storeError(CategoryPermission, "inspect-open-file", path, ErrPermission)
	}
	return checkOpenPrivateFile(file, 0o600)
}

func rejectGitWorktree(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return storeError(CategoryUnsafePath, "git-worktree-check", path, fmt.Errorf("Git worktree state is forbidden: %w", ErrUnsafePath))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return classifyError("git-worktree-check", marker, err)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
