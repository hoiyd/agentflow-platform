//go:build !linux && !darwin

package filesystem

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
)

func contentHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func validateOutputDirectory(string) error {
	return errors.New("filesystem writes require Linux or macOS")
}
func lockOutput(context.Context, rootRecord) (*os.File, error) {
	return nil, errors.New("unsupported output platform")
}
func inspectOutput(*os.File, string) (outputState, error) {
	return outputState{}, errors.New("unsupported output platform")
}
func createOutputTemp(*os.File, string) (*os.File, error) {
	return nil, errors.New("unsupported output platform")
}
func removeOutputTemp(*os.File, string) error { return errors.New("unsupported output platform") }
func publishOutput(*os.File, string, string, bool) error {
	return errors.New("unsupported output platform")
}
