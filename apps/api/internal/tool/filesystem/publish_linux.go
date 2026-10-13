//go:build linux

package filesystem

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishOutput(dir *os.File, from, to string, createOnly bool) error {
	fd := int(dir.Fd())
	if createOnly {
		return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
	}
	return unix.Renameat(fd, from, fd, to)
}
