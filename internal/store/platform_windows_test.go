//go:build windows

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateDirectoryAndFileAccepted(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := createPrivateDir(directory); err != nil {
		t.Fatalf("create private directory: %v", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		t.Fatalf("stat private directory: %v", err)
	}
	if err := checkStateRoot(directory, directoryInfo); err != nil {
		t.Fatalf("private directory rejected: %v", err)
	}
	assertWindowsCurrentUserProtectedDACL(t, directory)

	path := filepath.Join(directory, "journal.jsonl")
	file, err := openPrivateFile(path, true, true)
	if err != nil {
		t.Fatalf("create private file: %v", err)
	}
	defer file.Close()
	if err := checkOpenPrivateFile(file, 0o600); err != nil {
		t.Fatalf("private file rejected: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat private file: %v", err)
	}
	if err := checkPrivatePath(path, info, 0o600); err != nil {
		t.Fatalf("private path rejected: %v", err)
	}
	assertWindowsCurrentUserProtectedDACL(t, path)
}

func TestWindowsBroadenedDACLRejected(t *testing.T) {
	path := newWindowsPrivateFile(t)
	user, err := currentUserSID()
	if err != nil {
		t.Fatalf("current user SID: %v", err)
	}
	setWindowsDACL(t, path, fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FR;;;WD)", user.String()), true)

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat broadened file: %v", err)
	}
	err = checkPrivatePath(path, info, 0o600)
	if err == nil || CategoryOf(err) != CategoryPermission {
		t.Fatalf("broadened DACL error = %v, want permission rejection", err)
	}
}

func TestWindowsInheritedDACLRejected(t *testing.T) {
	path := newWindowsPrivateFile(t)
	user, err := currentUserSID()
	if err != nil {
		t.Fatalf("current user SID: %v", err)
	}
	setWindowsDACL(t, path, fmt.Sprintf("D:AI(A;ID;FA;;;%s)", user.String()), false)

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("read inherited DACL: %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read inherited DACL control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED != 0 {
		t.Fatal("test setup retained a protected DACL")
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat inherited file: %v", err)
	}
	err = checkPrivatePath(path, info, 0o600)
	if err == nil || CategoryOf(err) != CategoryPermission {
		t.Fatalf("inherited DACL error = %v, want permission rejection", err)
	}
}

func TestWindowsHardLinkRejectedWhenSupported(t *testing.T) {
	path := newWindowsPrivateFile(t)
	alias := path + ".alias"
	if err := os.Link(path, alias); err != nil {
		if optionalWindowsFilesystemFeature(err) {
			t.Skipf("hard links unavailable: %v", err)
		}
		t.Fatalf("create hard link: %v", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat hard-linked file: %v", err)
	}
	err = checkPrivatePath(path, info, 0o600)
	if err == nil || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("hard-link error = %v, want unsafe-path rejection", err)
	}
}

func TestWindowsSymlinkRejectedWhenPermitted(t *testing.T) {
	path := newWindowsPrivateFile(t)
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		if optionalWindowsFilesystemFeature(err) {
			t.Skipf("symlink creation unavailable without Developer Mode or privilege: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	err = checkPrivatePath(link, info, 0o600)
	if err == nil || CategoryOf(err) != CategoryUnsafePath {
		t.Fatalf("symlink error = %v, want unsafe-path rejection", err)
	}
}

func TestWindowsExclusiveLockTimeoutAndRelease(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := createPrivateDir(directory); err != nil {
		t.Fatalf("create private directory: %v", err)
	}
	path := filepath.Join(directory, "journal.lock")
	first, err := openPrivateFile(path, true, true)
	if err != nil {
		t.Fatalf("open first lock handle: %v", err)
	}
	defer first.Close()
	second, err := openPrivateFile(path, true, true)
	if err != nil {
		t.Fatalf("open second lock handle: %v", err)
	}
	defer second.Close()

	if err := acquireFileLock(context.Background(), first, time.Second); err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if err := acquireFileLock(context.Background(), second, 30*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second lock error = %v, want lock timeout", err)
	}
	releaseFileLock(first)
	if err := acquireFileLock(context.Background(), second, time.Second); err != nil {
		t.Fatalf("acquire released lock: %v", err)
	}
	releaseFileLock(second)
}

func TestWindowsStateRootMoveCannotRedirectCommit(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := createPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "state-moved")
	moveSucceeded := false
	journal := mustStore(t, root, Options{Hooks: Hooks{
		BeforeReplace: func(_, _ string) error {
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			moveSucceeded = true
			return createPrivateDir(root)
		},
	}})
	commit, err := journal.Update(context.Background(), appendObject(`{"value":1}`))
	if moveSucceeded {
		if err != nil || !commit.Committed {
			t.Fatalf("anchored commit = %+v err = %v", commit, err)
		}
		newPath := filepath.Join(root, filepath.FromSlash(testRelativeJournal))
		if _, err := os.Lstat(newPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("replacement root was mutated: %v", err)
		}
		movedPath := filepath.Join(moved, filepath.FromSlash(testRelativeJournal))
		if got, err := os.ReadFile(movedPath); err != nil || string(got) != "{\"value\":1}\n" {
			t.Fatalf("anchored journal = %q err = %v", got, err)
		}
		return
	}
	if err == nil || commit.Committed {
		t.Fatalf("commit = %+v err = %v, want blocked precommit move", commit, err)
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("move error = %v, want Windows sharing or access denial", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("original root unavailable after blocked move: %v", err)
	}
	if _, err := os.Lstat(moved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("moved root exists after blocked move: %v", err)
	}
	if _, err := os.Lstat(journal.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal committed after blocked move: %v", err)
	}
}

func newWindowsPrivateFile(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "private")
	if err := createPrivateDir(directory); err != nil {
		t.Fatalf("create private directory: %v", err)
	}
	path := filepath.Join(directory, "journal.jsonl")
	file, err := openPrivateFile(path, true, true)
	if err != nil {
		t.Fatalf("create private file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close private file: %v", err)
	}
	return path
}

func setWindowsDACL(t *testing.T, path, sddl string, protected bool) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("parse test DACL: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("extract test DACL: %v", err)
	}
	information := windows.SECURITY_INFORMATION(
		windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
	)
	if protected {
		information = windows.SECURITY_INFORMATION(
			windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION,
		)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		information,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatalf("set test DACL: %v", err)
	}
}

func assertWindowsCurrentUserProtectedDACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("read private security descriptor: %v", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatalf("read private owner: %v", err)
	}
	user, err := currentUserSID()
	if err != nil {
		t.Fatalf("read current user SID: %v", err)
	}
	if owner == nil || !windows.EqualSid(owner, user) {
		t.Fatal("private object owner is not the current user")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read private DACL control: %v", err)
	}
	if control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("private DACL control = %#x, want present and protected", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		t.Fatalf("private DACL is absent or empty: dacl=%v err=%v", dacl, err)
	}
}

func optionalWindowsFilesystemFeature(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}
