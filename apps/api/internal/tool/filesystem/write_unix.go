//go:build linux || darwin

package filesystem

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	"agentflow-platform/apps/api/internal/tool"
	"golang.org/x/sys/unix"
)

func contentHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func validateOutputDirectory(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("output root is not private")
	}
	return nil
}

func lockOutput(ctx context.Context, root rootRecord) (*os.File, error) {
	dir, err := openFile(root, ".")
	if err != nil {
		return nil, failure(tool.ErrorSecurityPolicyDenied, "Output directory identity is unsafe")
	}
	var stat unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &stat); err != nil || stat.Mode&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		dir.Close()
		return nil, failure(tool.ErrorSecurityPolicyDenied, "Output directory must remain private to the API OS user")
	}
	for {
		if err = ctx.Err(); err != nil {
			dir.Close()
			return nil, err
		}
		err = unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return dir, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			dir.Close()
			return nil, failure(tool.ErrorExecutionFailed, "Cannot lock the managed output directory")
		}
		select {
		case <-ctx.Done():
			dir.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func inspectOutput(dir *os.File, name string) (outputState, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err == unix.ENOENT {
		return outputState{}, nil
	}
	if err != nil {
		return outputState{}, failure(tool.ErrorSecurityPolicyDenied, "Report target is unsafe or unavailable")
	}
	file := os.NewFile(uintptr(fd), name)
	var stat, base unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		file.Close()
		return outputState{}, failure(tool.ErrorExecutionFailed, "Cannot inspect report identity")
	}
	if err = unix.Fstat(int(dir.Fd()), &base); err != nil || stat.Dev != base.Dev || stat.Nlink != 1 {
		file.Close()
		return outputState{}, failure(tool.ErrorSecurityPolicyDenied, "Report links or device changes are forbidden")
	}
	return readOutputFile(file)
}

func createOutputTemp(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func removeOutputTemp(dir *os.File, name string) error { return unix.Unlinkat(int(dir.Fd()), name, 0) }
