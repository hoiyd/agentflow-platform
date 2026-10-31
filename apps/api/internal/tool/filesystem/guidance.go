package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/redaction"
)

// ProjectRootRevision is used before a Run exists, with the Workspace identity
// supplied by application commands. It never creates a Stage or Tool grant.
func (s *Service) ProjectRootRevision(ctx context.Context, workspace, id string, paths []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.store.CheckWorkspaceExecution(workspace); err != nil {
		return "", err
	}
	root, err := s.authorizedRootForWorkspace(workspace, id)
	if err != nil {
		return "", err
	}
	if root.WriteAccess != "" {
		return "", fmt.Errorf("model-writable output roots cannot supply trusted project instructions")
	}
	for _, name := range paths {
		if name == "." || validatePath(name) != nil {
			return "", fmt.Errorf("project inputs require concrete relative files")
		}
		file, err := openFile(root, name)
		if err != nil {
			return "", fmt.Errorf("project input is unavailable or unsafe")
		}
		info, err := file.Stat()
		file.Close()
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("project input must be a regular file")
		}
	}
	encoded, _ := json.Marshal(root)
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func (s *Service) ReadProjectText(ctx context.Context, workspace, id, name string, limit int) (string, error) {
	if _, err := s.ProjectRootRevision(ctx, workspace, id, []string{name}); err != nil {
		return "", err
	}
	root, err := s.authorizedRootForWorkspace(workspace, id)
	if err != nil {
		return "", err
	}
	file, err := openFile(root, name)
	if err != nil {
		return "", fmt.Errorf("project instructions file is unavailable or unsafe")
	}
	text, err := readTextFile(ctx, file, limit)
	if err != nil {
		return "", err
	}
	if text.redactions > 0 || redaction.ValidateValue(text.content) != nil {
		return "", redaction.ErrCredentialContent
	}
	return text.content, nil
}

// Trust configuration must stay outside every model-writable output directory,
// even when that directory belongs to another Workspace or has a different ID.
func (s *Service) ValidateGuidanceConfig(name string) error {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("guidance trust configuration must be a regular operator file")
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return err
	}
	name, err = filepath.Abs(name)
	if err != nil {
		return err
	}
	for _, root := range s.roots {
		if root.WriteAccess != "managed_output" {
			continue
		}
		output, err := filepath.EvalSymlinks(root.Path)
		if err != nil || name == output || strings.HasPrefix(name, output+string(os.PathSeparator)) {
			return fmt.Errorf("guidance trust configuration cannot be model-writable")
		}
	}
	return nil
}
