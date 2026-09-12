//go:build linux

package store

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func createArtifactDirectory(path string) error { return os.Mkdir(path, 0700) }

func publishArtifactDirectory(parent *os.Root, staging, destination string, _ fs.FileInfo) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return unix.Renameat2(int(directory.Fd()), staging, int(directory.Fd()), destination, unix.RENAME_NOREPLACE)
}
