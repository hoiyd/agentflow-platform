//go:build linux || darwin

package filesystem

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openUndoDirectory(output *os.File, create bool) (*os.File, error) {
	if create {
		if err := unix.Mkdirat(int(output.Fd()), undoDirectory, 0700); err != nil && err != unix.EEXIST {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(output.Fd()), undoDirectory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), undoDirectory)
	var stat, parent unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || unix.Fstat(int(output.Fd()), &parent) != nil || stat.Dev != parent.Dev || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
		dir.Close()
		return nil, errors.New("unsafe Undo directory")
	}
	return dir, nil
}

func openPrivateEntry(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat, parent unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || unix.Fstat(int(dir.Fd()), &parent) != nil || stat.Dev != parent.Dev || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0177 != 0 {
		file.Close()
		return nil, errors.New("unsafe Undo image")
	}
	return file, nil
}
