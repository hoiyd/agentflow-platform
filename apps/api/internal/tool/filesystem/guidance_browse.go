package filesystem

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

// A user selecting task inputs has no Run/Stage identity. Reuse the explicit
// Workspace/root boundary and no-follow open, never fabricate a Tool execution.
func (s *Service) ListProjectFiles(ctx context.Context, workspace, id, directory string) (domain.ProjectFilePage, error) {
	result := domain.ProjectFilePage{Path: directory, Entries: []domain.ProjectFileEntry{}}
	if validatePath(directory) != nil {
		return result, fmt.Errorf("invalid project directory")
	}
	if _, err := s.ProjectRootRevision(ctx, workspace, id, nil); err != nil {
		return result, err
	}
	root, err := s.authorizedRootForWorkspace(workspace, id)
	if err != nil {
		return result, err
	}
	dir, err := openFile(root, directory)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	items, err := dir.ReadDir(maxEntries + 1)
	if err != nil && err != io.EOF {
		return result, fmt.Errorf("cannot list project directory")
	}
	if len(items) > maxEntries {
		result.Truncated = true
		items = items[:maxEntries]
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name := path.Join(directory, item.Name())
		if validatePath(name) != nil {
			continue
		}
		file, err := openFile(root, name)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		file.Close()
		if err != nil {
			continue
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		result.Entries = append(result.Entries, domain.ProjectFileEntry{Path: name, Kind: kind})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind == "directory"
		}
		return a.Path < b.Path
	})
	_, err = s.ProjectRootRevision(ctx, workspace, id, nil)
	return result, err
}
