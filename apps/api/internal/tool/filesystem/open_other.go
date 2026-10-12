//go:build !linux && !darwin

package filesystem

import (
	"errors"
	"os"
)

func directoryIdentity(string) (string, error) {
	return "", errors.New("filesystem reads require Linux or macOS")
}
func openFile(rootRecord, string) (*os.File, error) {
	return nil, errors.New("filesystem reads require Linux or macOS")
}
func fileIdentity(*os.File) (string, error) {
	return "", errors.New("filesystem reads require Linux or macOS")
}
