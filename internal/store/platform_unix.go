//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func checkPrivatePath(path string, info os.FileInfo, want os.FileMode) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != want {
		return storeError(CategoryPermission, "inspect-private-metadata", path, ErrPermission)
	}
	if info.Mode().IsRegular() && stat.Nlink != 1 {
		return storeError(CategoryUnsafePath, "inspect-hard-link", path, ErrUnsafePath)
	}
	return nil
}

func checkStateRoot(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
		return storeError(CategoryPermission, "inspect-state-root", path, ErrPermission)
	}
	return nil
}

func checkOpenPrivateFile(file *os.File, want os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return checkPrivatePath(file.Name(), info, want)
}

func createPrivateDir(path string) error { return os.Mkdir(path, 0o700) }

func normalizeSystemRootAlias(path string) (string, error) {
	remainder := strings.TrimPrefix(path, string(filepath.Separator))
	if remainder == "" {
		return path, nil
	}
	first := strings.SplitN(remainder, string(filepath.Separator), 2)[0]
	candidate := filepath.Join(string(filepath.Separator), first)
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", classifyError("normalize-system-root", candidate, err)
	}
	if info.Mode()&os.ModeSymlink == 0 || !trustedSystemSymlink(candidate, info) {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", classifyError("normalize-system-root", candidate, err)
	}
	relative, err := filepath.Rel(candidate, path)
	if err != nil {
		return "", classifyError("normalize-system-root", candidate, err)
	}
	if relative == "." {
		return resolved, nil
	}
	return filepath.Join(resolved, relative), nil
}

func trustedSystemSymlink(path string, info os.FileInfo) bool {
	if filepath.Dir(path) != string(filepath.Separator) {
		return false
	}
	linkStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || linkStat.Uid != 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(resolved) || resolved == path {
		return false
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || !resolvedInfo.IsDir() {
		return false
	}
	resolvedStat, ok := resolvedInfo.Sys().(*syscall.Stat_t)
	if !ok || resolvedStat.Uid != 0 {
		return false
	}
	worldWritable := resolvedInfo.Mode().Perm()&0o022 != 0
	return !worldWritable || resolvedInfo.Mode()&os.ModeSticky != 0
}

func validateStateRootAncestors(path string) error {
	remainder := strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator))
	current := string(filepath.Separator)
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return classifyError("inspect-state-ancestors", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return storeError(CategoryUnsafePath, "inspect-state-ancestors", current, ErrUnsafePath)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return storeError(CategoryPermission, "inspect-state-ancestors", current, ErrPermission)
		}
		if info.Mode().Perm()&0o022 != 0 {
			trustedSticky := stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
			if !trustedSticky {
				return storeError(CategoryPermission, "inspect-state-ancestors", current, ErrPermission)
			}
		}
	}
	return nil
}

func openPrivateFile(path string, readWrite, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if readWrite {
		flags = unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	}
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, storeError(CategoryUnsafePath, "open-no-follow", path, ErrUnsafePath)
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func createPrivateTemp(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func acquireFileLock(ctx context.Context, file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrLockTimeout
		}
		wait := lockPollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %v", ErrLockTimeout, ctx.Err())
		case <-timer.C:
		}
	}
}

func releaseFileLock(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }

func syncDirectory(directory *os.File) (bool, error) {
	if err := directory.Sync(); err != nil {
		return false, err
	}
	return true, nil
}
