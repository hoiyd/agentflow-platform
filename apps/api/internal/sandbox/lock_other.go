//go:build !darwin && !linux

package sandbox

import (
	"errors"
	"os"
)

func lockDirectory(string) (*os.File, error) {
	return nil, errors.New("local sbx integration supports Linux and macOS only")
}
