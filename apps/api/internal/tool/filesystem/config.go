// Package filesystem supplies fixed, Workspace-scoped file operations. It does
// not execute code and must not be used as a sandbox for arbitrary processes.
package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
)

const maxConfigBytes = 64 * 1024

type RootConfig struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	WriteAccess string `json:"write_access,omitempty"`
}
type rootRecord struct {
	RootConfig
	Identity string
}

// Store is deliberately limited to persisted execution identity and lifecycle.
// Current Agent/Tool grants are checked separately by the shared Executor.
type Store interface {
	GetRun(string) (domain.Run, bool, error)
	CheckWorkspaceExecution(string) error
}
type Service struct {
	configPath string
	roots      []rootRecord
	revision   string
	store      Store
	writes     writeOperations
}

func New(configPath string, storage Store) (*Service, error) {
	if storage == nil {
		return nil, errors.New("filesystem tools require a Run authorization store")
	}
	roots := []rootRecord{}
	var err error
	if configPath != "" {
		roots, err = loadRoots(configPath)
		if err != nil {
			return nil, err
		}
	}
	data, _ := json.Marshal(roots)
	return &Service{configPath: configPath, roots: roots, revision: fmt.Sprintf("filesystem-read-v1:%x", sha256.Sum256(data)), store: storage, writes: defaultWriteOperations()}, nil
}

func loadRoots(path string) ([]rootRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("filesystem root configuration unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return nil, errors.New("filesystem root configuration exceeds its read limit")
	}
	var config struct {
		Roots []RootConfig `json:"roots"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return nil, errors.New("invalid filesystem root configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("filesystem configuration requires one JSON object")
	}
	if len(config.Roots) > 64 {
		return nil, errors.New("too many filesystem roots")
	}
	seen := map[string]bool{}
	roots := []rootRecord{}
	for _, entry := range config.Roots {
		id, parseErr := strconv.ParseInt(entry.WorkspaceID, 10, 64)
		key := entry.WorkspaceID + ":" + entry.ID
		if !rootIDPattern.MatchString(entry.ID) || parseErr != nil || id < 1 || strconv.FormatInt(id, 10) != entry.WorkspaceID || seen[key] || !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path {
			return nil, errors.New("filesystem roots require unique logical IDs, decimal Workspace IDs and canonical absolute directories")
		}
		if validatePath(strings.TrimPrefix(filepath.ToSlash(entry.Path), "/")) != nil {
			return nil, errors.New("filesystem roots cannot grant the whole filesystem or sensitive directories")
		}
		seen[key] = true
		if entry.WriteAccess != "" && entry.WriteAccess != "managed_output" {
			return nil, errors.New("filesystem writes require managed_output; shared project roots are read-only")
		}
		if entry.WriteAccess == "managed_output" {
			if err := validateOutputDirectory(entry.Path); err != nil {
				return nil, errors.New("filesystem output roots must be private directories owned by the API OS user")
			}
		}
		identity, err := directoryIdentity(entry.Path)
		if err != nil {
			return nil, errors.New("filesystem root directory is unavailable or unsafe")
		}
		roots = append(roots, rootRecord{RootConfig: entry, Identity: identity})
	}
	for _, output := range roots {
		if output.WriteAccess != "managed_output" {
			continue
		}
		configPath, configErr := filepath.EvalSymlinks(path)
		if configErr == nil {
			configPath, configErr = filepath.Abs(configPath)
		}
		outputPath, outputErr := filepath.EvalSymlinks(output.Path)
		if configErr != nil || outputErr != nil || configPath == outputPath || strings.HasPrefix(configPath, outputPath+string(os.PathSeparator)) {
			return nil, errors.New("filesystem root configuration must remain outside model-writable output directories")
		}
		for _, other := range roots {
			otherPath, pathErr := filepath.EvalSymlinks(other.Path)
			if pathErr != nil {
				return nil, errors.New("filesystem root directory changed during validation")
			}
			if output.WorkspaceID != other.WorkspaceID && (output.Identity == other.Identity || strings.HasPrefix(outputPath, otherPath+string(os.PathSeparator)) || strings.HasPrefix(otherPath, outputPath+string(os.PathSeparator))) {
				return nil, errors.New("managed output roots cannot overlap another Workspace's roots")
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].WorkspaceID+":"+roots[i].ID < roots[j].WorkspaceID+":"+roots[j].ID
	})
	return roots, nil
}

var rootIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (s *Service) workspace(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	scope := event.ScopeFromContext(ctx)
	run, ok, err := s.store.GetRun(scope.RunID)
	if err != nil {
		return "", err
	}
	if !ok || scope.RunID == "" || scope.ConversationID == "" || run.ConversationID != scope.ConversationID || run.WorkspaceID == "" {
		return "", failure(tool.ErrorSecurityScopeInvalid, "Filesystem access requires a matching persisted Run and Conversation")
	}
	if err = s.store.CheckWorkspaceExecution(run.WorkspaceID); err != nil {
		return "", failure(tool.ErrorSecurityPolicyDenied, "Filesystem Workspace access is unavailable")
	}
	return run.WorkspaceID, nil
}

func (s *Service) authorizedRoot(ctx context.Context, id string) (rootRecord, error) {
	workspace, err := s.workspace(ctx)
	if err != nil {
		return rootRecord{}, err
	}
	return s.authorizedRootForWorkspace(workspace, id)
}

func (s *Service) authorizedRootForWorkspace(workspace, id string) (rootRecord, error) {
	var frozen *rootRecord
	for i := range s.roots {
		if s.roots[i].ID == id && s.roots[i].WorkspaceID == workspace {
			frozen = &s.roots[i]
			break
		}
	}
	if frozen == nil {
		return rootRecord{}, failure(tool.ErrorSecurityPolicyDenied, "Filesystem root is not authorized for this Workspace")
	}
	current, err := loadRoots(s.configPath)
	if err != nil {
		return rootRecord{}, failure("filesystem_root_changed", "Filesystem roots are unavailable; operator review and a new Run are required")
	}
	for _, entry := range current {
		if entry == *frozen {
			return entry, nil
		}
	}
	return rootRecord{}, failure("filesystem_root_changed", "Filesystem root changed or was revoked; operator review and a new Run are required")
}

func failure(code tool.ErrorCode, message string) error {
	return &tool.ExecutionError{Code: code, Message: message}
}

func (s *Service) availability() string {
	current, err := loadRoots(s.configPath)
	if err != nil {
		return "filesystem_root_changed"
	}
	data, _ := json.Marshal(current)
	if fmt.Sprintf("filesystem-read-v1:%x", sha256.Sum256(data)) != s.revision {
		return "filesystem_root_changed"
	}
	return ""
}

func (s *Service) open(ctx context.Context, root rootRecord, path string) (*os.File, error) {
	current, err := s.authorizedRoot(ctx, root.ID)
	if err != nil {
		return nil, err
	}
	if current != root {
		return nil, failure("filesystem_root_changed", "Filesystem root identity changed")
	}
	file, err := openFile(root, path)
	if err != nil {
		return nil, failure(tool.ErrorSecurityPolicyDenied, "Filesystem object is unavailable or unsafe")
	}
	return file, nil
}
