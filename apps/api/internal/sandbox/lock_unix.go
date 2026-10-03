//go:build darwin || linux

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func lockDirectory(directory string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("sandbox state directory already has an API owner")
	}
	return file, nil
}
