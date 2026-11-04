//go:build !linux && !darwin

package filesystem

import (
	"errors"
	"os"
)

func openUndoDirectory(*os.File, bool) (*os.File, error) {
	return nil, errors.New("unsupported output platform")
}
func openPrivateEntry(*os.File, string) (*os.File, error) {
	return nil, errors.New("unsupported output platform")
}
