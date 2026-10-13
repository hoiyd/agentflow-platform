//go:build darwin

package filesystem

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishOutput(dir *os.File, from, to string, createOnly bool) error {
	fd := int(dir.Fd())
	if createOnly {
		return unix.RenameatxNp(fd, from, fd, to, unix.RENAME_EXCL)
	}
	return unix.Renameat(fd, from, fd, to)
}
