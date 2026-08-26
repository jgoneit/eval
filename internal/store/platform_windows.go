//go:build windows

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsFullControl = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func checkPrivatePath(path string, info os.FileInfo, want os.FileMode) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return storeError(CategoryUnsafePath, "inspect-private-path", path, ErrUnsafePath)
	}
	wantDirectory, err := privatePathKind(want)
	if err != nil {
		return err
	}
	if info.IsDir() != wantDirectory || (!wantDirectory && !info.Mode().IsRegular()) {
		return storeError(CategoryUnsafePath, "inspect-private-path", path, ErrUnsafePath)
	}

	file, err := openForInspection(path, wantDirectory)
	if err != nil {
		return classifyError("open-private-path", path, err)
	}
	defer file.Close()

	openInfo, err := file.Stat()
	if err != nil {
		return classifyError("stat-private-path", path, err)
	}
	if !os.SameFile(info, openInfo) {
		return storeError(CategoryUnsafePath, "inspect-private-path", path, ErrUnsafePath)
	}
	return checkOpenPrivateFile(file, want)
}

func checkStateRoot(path string, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return storeError(CategoryUnsafePath, "inspect-state-root", path, ErrUnsafePath)
	}
	file, err := openForInspection(path, true)
	if err != nil {
		return classifyError("open-state-root", path, err)
	}
	defer file.Close()
	var details windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &details); err != nil {
		return classifyError("inspect-state-root", path, err)
	}
	if details.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return storeError(CategoryUnsafePath, "inspect-state-root", path, ErrUnsafePath)
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return classifyError("read-state-root-security", path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return storeError(CategoryPermission, "read-state-root-owner", path, errors.Join(ErrPermission, err))
	}
	user, err := currentUserSID()
	if err != nil {
		return classifyError("current-user", path, err)
	}
	if !windows.EqualSid(owner, user) {
		return storeError(CategoryPermission, "check-state-root-owner", path, ErrPermission)
	}
	dacl, present, err := descriptor.DACL()
	if err != nil || !present || dacl == nil {
		return storeError(CategoryPermission, "read-state-root-dacl", path, errors.Join(ErrPermission, err))
	}
	administrator, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return classifyError("administrator-sid", path, err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return classifyError("system-sid", path, err)
	}
	writeMask := windows.ACCESS_MASK(
		windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.FILE_GENERIC_WRITE |
			windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
			windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | 0x40,
	)
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return classifyError("read-state-root-dacl-entry", path, err)
		}
		if ace == nil {
			return storeError(CategoryPermission, "check-state-root-dacl-entry", path, ErrPermission)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			trusted := sid.IsValid() && (windows.EqualSid(sid, user) || windows.EqualSid(sid, administrator) || windows.EqualSid(sid, system))
			if !trusted && ace.Mask&writeMask != 0 {
				return storeError(CategoryPermission, "check-state-root-dacl-access", path, ErrPermission)
			}
		case windows.ACCESS_DENIED_ACE_TYPE:
		default:
			return storeError(CategoryPermission, "check-state-root-dacl-entry", path, ErrPermission)
		}
	}
	return nil
}

func checkOpenPrivateFile(file *os.File, want os.FileMode) error {
	if file == nil {
		return storeError(CategoryUnsafePath, "inspect-open-file", "", ErrUnsafePath)
	}
	path := file.Name()
	wantDirectory, err := privatePathKind(want)
	if err != nil {
		return err
	}
	handle := windows.Handle(file.Fd())
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return classifyError("file-type", path, err)
	}
	if fileType != windows.FILE_TYPE_DISK {
		return storeError(CategoryUnsafePath, "file-type", path, ErrUnsafePath)
	}

	var details windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &details); err != nil {
		return classifyError("file-information", path, err)
	}
	isDirectory := details.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != wantDirectory || details.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return storeError(CategoryUnsafePath, "file-information", path, ErrUnsafePath)
	}
	if !isDirectory && details.NumberOfLinks != 1 {
		return storeError(CategoryUnsafePath, "file-information", path, ErrUnsafePath)
	}

	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return classifyError("read-security", path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return storeError(CategoryPermission, "read-owner", path, errors.Join(ErrPermission, err))
	}
	user, err := currentUserSID()
	if err != nil {
		return classifyError("current-user", path, err)
	}
	if !windows.EqualSid(owner, user) {
		return storeError(CategoryPermission, "check-owner", path, ErrPermission)
	}

	control, _, err := descriptor.Control()
	if err != nil {
		return classifyError("read-security-control", path, err)
	}
	if control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		return storeError(CategoryPermission, "check-dacl", path, ErrPermission)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return storeError(CategoryPermission, "read-dacl", path, errors.Join(ErrPermission, err))
	}

	var userAllow windows.ACCESS_MASK
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return classifyError("read-dacl-entry", path, err)
		}
		if ace == nil || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return storeError(CategoryPermission, "check-dacl-entry", path, ErrPermission)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsValid() || !windows.EqualSid(sid, user) {
				return storeError(CategoryPermission, "check-dacl-trustee", path, ErrPermission)
			}
			userAllow |= ace.Mask
		case windows.ACCESS_DENIED_ACE_TYPE:
			// A deny ACE cannot disclose data. The successful handle open and the
			// accumulated current-user allow mask below establish usability.
		default:
			return storeError(CategoryPermission, "check-dacl-entry", path, ErrPermission)
		}
	}
	if userAllow&windows.GENERIC_ALL == 0 && userAllow&windowsFullControl != windowsFullControl {
		return storeError(CategoryPermission, "check-dacl-access", path, ErrPermission)
	}
	return nil
}

func createPrivateDir(path string) error {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return classifyError("create-directory", path, err)
	}
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return classifyError("create-directory-security", path, err)
	}
	created := false
	if err := windows.CreateDirectory(pathUTF16, attributes); err != nil {
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return classifyError("create-directory", path, err)
		}
	} else {
		created = true
	}
	info, err := os.Lstat(path)
	if err != nil {
		return classifyError("inspect-created-directory", path, err)
	}
	if err := checkPrivatePath(path, info, 0o700); err != nil {
		if created {
			_ = windows.RemoveDirectory(pathUTF16)
		}
		return err
	}
	return nil
}

func validateStateRootAncestors(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return storeError(CategoryUnsafePath, "inspect-state-ancestors", path, ErrUnsafePath)
	}
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return nil
	}
	current := root
	for _, component := range splitWindowsPath(relative) {
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
		file, err := openForInspection(current, true)
		if err != nil {
			return classifyError("open-state-ancestor", current, err)
		}
		var details windows.ByHandleFileInformation
		inspectErr := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &details)
		_ = file.Close()
		if inspectErr != nil {
			return classifyError("inspect-state-ancestor", current, inspectErr)
		}
		if details.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return storeError(CategoryUnsafePath, "inspect-state-ancestor", current, ErrUnsafePath)
		}
	}
	return nil
}

func normalizeSystemRootAlias(path string) (string, error) {
	return filepath.Clean(path), nil
}

func openPrivateFile(path string, readWrite, create bool) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if readWrite {
		access |= windows.GENERIC_WRITE
	}
	if !create {
		return openWindowsFile(path, access, windows.OPEN_EXISTING, nil, windows.FILE_ATTRIBUTE_NORMAL)
	}

	attributes, err := privateSecurityAttributes()
	if err != nil {
		return nil, err
	}
	file, err := openWindowsFile(path, access, windows.CREATE_NEW, attributes, windows.FILE_ATTRIBUTE_NORMAL)
	if err != nil && (errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS)) {
		file, err = openWindowsFile(path, access, windows.OPEN_EXISTING, nil, windows.FILE_ATTRIBUTE_NORMAL)
	}
	return file, err
}

func createPrivateTemp(path string) (*os.File, error) {
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return nil, err
	}
	return openWindowsFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.CREATE_NEW,
		attributes,
		windows.FILE_ATTRIBUTE_TEMPORARY,
	)
}

func acquireFileLock(ctx context.Context, file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			&windows.Overlapped{},
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
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

func releaseFileLock(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}

func syncDirectory(directory *os.File) (bool, error) {
	err := windows.FlushFileBuffers(windows.Handle(directory.Fd()))
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func privatePathKind(want os.FileMode) (bool, error) {
	switch want.Perm() {
	case 0o700:
		return true, nil
	case 0o600:
		return false, nil
	default:
		return false, storeError(CategoryUnsafePath, "private-mode", "", ErrUnsafePath)
	}
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, ErrPermission
	}
	return user.User.Sid, nil
}

func privateSecurityAttributes() (*windows.SecurityAttributes, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("D:P(A;;FA;;;%s)", user.String()),
	)
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}, nil
}

func openForInspection(path string, directory bool) (*os.File, error) {
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	return openWindowsFile(
		path,
		uint32(windows.READ_CONTROL)|windows.FILE_READ_ATTRIBUTES,
		windows.OPEN_EXISTING,
		nil,
		flags,
	)
}

func openWindowsFile(
	path string,
	access uint32,
	disposition uint32,
	attributes *windows.SecurityAttributes,
	fileAttributes uint32,
) (*os.File, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		attributes,
		disposition,
		fileAttributes|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("wrap Windows file handle")
	}
	return file, nil
}

func splitWindowsPath(path string) []string {
	var components []string
	for path != "." && path != "" {
		directory, base := filepath.Split(path)
		if base != "" {
			components = append([]string{base}, components...)
		}
		path = filepath.Clean(directory)
		path = filepath.Clean(path)
		if path == "." || path == string(filepath.Separator) {
			break
		}
	}
	return components
}
