//go:build windows

package store

import (
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createArtifactDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := privateSecurityAttributes()
	if err != nil {
		return err
	}
	return windows.CreateDirectory(name, attributes)
}

type artifactRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func publishArtifactDirectory(parent *os.Root, staging, destination string, expected fs.FileInfo) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	parentHandle := windows.Handle(directory.Fd())
	objectName, err := windows.NewNTUnicodeString(staging)
	if err != nil {
		return err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parentHandle,
		ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var sourceHandle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(&sourceHandle,
		windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES,
		attributes, &status, &allocationSize, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|
			windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return err
	}
	source := os.NewFile(uintptr(sourceHandle), staging)
	if source == nil {
		windows.CloseHandle(sourceHandle)
		return windows.ERROR_INVALID_HANDLE
	}
	defer source.Close()
	actual, err := source.Stat()
	if err != nil {
		return err
	}
	if expected == nil || !os.SameFile(expected, actual) {
		return windows.ERROR_FILE_INVALID
	}
	name, err := windows.UTF16FromString(destination)
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	var layout artifactRenameInformation
	buffer := make([]byte, int(unsafe.Offsetof(layout.FileName))+len(name)*2)
	information := (*artifactRenameInformation)(unsafe.Pointer(&buffer[0]))
	information.ReplaceIfExists = 0
	information.RootDirectory = parentHandle
	information.FileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&information.FileName[0], len(name)), name)
	return windows.NtSetInformationFile(sourceHandle, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
}
