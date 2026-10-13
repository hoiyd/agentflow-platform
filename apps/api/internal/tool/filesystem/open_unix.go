//go:build linux || darwin

package filesystem

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func directoryIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("not a plain directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer dir.Close()
	var stat unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &stat); err != nil {
		return "", err
	}
	return statIdentity(stat), nil
}

func statIdentity(stat unix.Stat_t) string { return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino) }

// Open every component relative to a held directory descriptor without following
// links. os.Root alone permits in-root aliases to otherwise forbidden files.
func openFile(root rootRecord, path string) (*os.File, error) {
	capability, err := os.OpenRoot(root.Path)
	if err != nil {
		return nil, err
	}
	defer capability.Close()
	dir, err := capability.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() {
		if dir != nil {
			_ = dir.Close()
		}
	}()
	var base unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &base); err != nil {
		return nil, err
	}
	if statIdentity(base) != root.Identity {
		return nil, fmt.Errorf("root replaced")
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(dir.Fd()), part, flags, 0)
		if err != nil {
			return nil, err
		}
		next := os.NewFile(uintptr(fd), part)
		var stat unix.Stat_t
		if err = unix.Fstat(fd, &stat); err != nil {
			next.Close()
			return nil, err
		}
		kind := stat.Mode & unix.S_IFMT
		if stat.Dev != base.Dev || kind != unix.S_IFDIR && kind != unix.S_IFREG || kind == unix.S_IFREG && stat.Nlink != 1 {
			next.Close()
			return nil, fmt.Errorf("unsupported filesystem object")
		}
		dir.Close()
		dir = next
	}
	result := dir
	dir = nil
	return result, nil
}

func fileIdentity(file *os.File) (string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return "", err
	}
	return statIdentity(stat), nil
}
