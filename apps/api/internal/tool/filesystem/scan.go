package filesystem

import (
	"context"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/tool"
)

const maxEntries = 500
const maxScanBytes = 2 * 1024 * 1024

type Entry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	FileID string `json:"file_id"`
}
type ListResult struct {
	Roots         []string `json:"roots,omitempty"`
	Entries       []Entry  `json:"entries"`
	NextOffset    int      `json:"next_offset"`
	Truncated     bool     `json:"truncated"`
	ScanTruncated bool     `json:"scan_truncated"`
	Skipped       int      `json:"skipped"`
	RootRevision  string   `json:"root_revision"`
	TrustBoundary string   `json:"trust_boundary"`
}

type scan struct {
	entries          []Entry
	visited, skipped int
	incomplete       bool
}

func (v *scan) walk(ctx context.Context, s *Service, root rootRecord, relative string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.open(ctx, root, relative)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		return failure(tool.ErrorInvalidArgs, "Filesystem list/search path must be a directory")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, readErr := dir.ReadDir(32)
		if readErr != nil && readErr != io.EOF {
			return failure(tool.ErrorExecutionFailed, "Filesystem directory scan failed")
		}
		for _, item := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if v.visited >= maxEntries {
				v.incomplete = true
				return nil
			}
			v.visited++
			relativePath := path.Join(relative, item.Name())
			if validatePath(relativePath) != nil {
				v.skipped++
				continue
			}
			file, openErr := s.open(ctx, root, relativePath)
			if openErr != nil {
				if _, authorizationErr := s.authorizedRoot(ctx, root.ID); authorizationErr != nil {
					return authorizationErr
				}
				var issue *tool.ExecutionError
				if errors.As(openErr, &issue) && issue.Code == "filesystem_root_changed" {
					return openErr
				}
				v.skipped++
				continue
			}
			metadata, statErr := file.Stat()
			identity, identityErr := fileIdentity(file)
			file.Close()
			if statErr != nil || identityErr != nil {
				v.skipped++
				continue
			}
			kind := "file"
			if metadata.IsDir() {
				kind = "directory"
			}
			v.entries = append(v.entries, Entry{Path: relativePath, Kind: kind, Size: metadata.Size(), FileID: identity})
			if metadata.IsDir() && depth > 1 {
				if err = v.walk(ctx, s, root, relativePath, depth-1); err != nil {
					return err
				}
				if v.visited >= maxEntries {
					return nil
				}
			} else if metadata.IsDir() {
				v.incomplete = true
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}

func (s *Service) list(ctx context.Context, input arguments) (any, error) {
	if input.Root == "" {
		workspace, err := s.workspace(ctx)
		if err != nil {
			return nil, err
		}
		roots := []string{}
		for _, entry := range s.roots {
			if entry.WorkspaceID == workspace {
				if _, err := s.authorizedRoot(ctx, entry.ID); err != nil {
					return nil, err
				}
				roots = append(roots, entry.ID)
			}
		}
		return ListResult{Roots: roots, Entries: []Entry{}, RootRevision: s.revision, TrustBoundary: trustBoundary}, nil
	}
	root, err := s.authorizedRoot(ctx, input.Root)
	if err != nil {
		return nil, err
	}
	if input.Depth == 0 {
		input.Depth = 1
	}
	if input.Limit == 0 {
		input.Limit = 30
	}
	scanned := scan{entries: []Entry{}}
	if err = scanned.walk(ctx, s, root, input.Path, input.Depth); err != nil {
		return nil, err
	}
	sort.Slice(scanned.entries, func(i, j int) bool { return scanned.entries[i].Path < scanned.entries[j].Path })
	start, end, err := pageBounds(input.Offset, input.Limit, len(scanned.entries))
	if err != nil {
		return nil, err
	}
	return ListResult{Entries: scanned.entries[start:end], NextOffset: end, Truncated: end < len(scanned.entries), ScanTruncated: scanned.incomplete, Skipped: scanned.skipped, RootRevision: s.revision, TrustBoundary: trustBoundary}, nil
}

type Match struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Offset      int    `json:"offset"`
	Preview     string `json:"preview"`
	ContentHash string `json:"content_hash"`
	FileID      string `json:"file_id"`
}
type SearchResult struct {
	Matches       []Match `json:"matches"`
	NextOffset    int     `json:"next_offset"`
	Truncated     bool    `json:"truncated"`
	ScanTruncated bool    `json:"scan_truncated"`
	ScannedBytes  int     `json:"scanned_bytes"`
	Skipped       int     `json:"skipped"`
	RootRevision  string  `json:"root_revision"`
	TrustBoundary string  `json:"trust_boundary"`
}

func (s *Service) search(ctx context.Context, input arguments) (any, error) {
	root, err := s.authorizedRoot(ctx, input.Root)
	if err != nil {
		return nil, err
	}
	scanned := scan{entries: []Entry{}}
	if err = scanned.walk(ctx, s, root, input.Path, 3); err != nil {
		return nil, err
	}
	sort.Slice(scanned.entries, func(i, j int) bool { return scanned.entries[i].Path < scanned.entries[j].Path })
	matches := []Match{}
	bytes := 0
	for _, entry := range scanned.entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Kind != "file" {
			continue
		}
		if entry.Size > int64(maxScanBytes-bytes) {
			scanned.incomplete = true
			break
		}
		file, readErr := s.readText(ctx, root, entry.Path, maxScanBytes-bytes)
		bytes += file.bytes
		if readErr != nil {
			if _, authorizationErr := s.authorizedRoot(ctx, root.ID); authorizationErr != nil {
				return nil, authorizationErr
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var typed *tool.ExecutionError
			if errors.As(readErr, &typed) && typed.Code == "filesystem_root_changed" {
				return nil, readErr
			}
			if errors.As(readErr, &typed) && typed.Code == "filesystem_file_limit" {
				scanned.incomplete = true
			}
			scanned.skipped++
			continue
		}
		lineOffset := 0
		for line, content := range strings.Split(file.content, "\n") {
			offset := lineOffset
			lineOffset += len(content) + 1
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			matchOffset := strings.Index(content, input.Query)
			if matchOffset < 0 {
				continue
			}
			if len(matches) >= maxEntries {
				scanned.incomplete = true
				break
			}
			start := max(0, matchOffset-80)
			for start > 0 && !utf8.RuneStart(content[start]) {
				start--
			}
			end := min(len(content), start+240)
			for end > start && end < len(content) && !utf8.RuneStart(content[end]) {
				end--
			}
			matches = append(matches, Match{Path: entry.Path, Line: line + 1, Offset: offset + matchOffset, Preview: content[start:end], ContentHash: file.hash, FileID: file.identity})
		}
		if len(matches) >= maxEntries {
			break
		}
		if bytes >= maxScanBytes {
			scanned.incomplete = true
			break
		}
	}
	if input.Limit == 0 {
		input.Limit = 10
	}
	start, end, err := pageBounds(input.Offset, input.Limit, len(matches))
	if err != nil {
		return nil, err
	}
	return SearchResult{Matches: matches[start:end], NextOffset: end, Truncated: end < len(matches), ScanTruncated: scanned.incomplete, ScannedBytes: bytes, Skipped: scanned.skipped, RootRevision: s.revision, TrustBoundary: trustBoundary}, nil
}

func pageBounds(offset, limit, total int) (int, int, error) {
	if offset < 0 || offset > total {
		return 0, 0, failure(tool.ErrorInvalidArgs, "Filesystem result offset is invalid")
	}
	return offset, min(total, offset+limit), nil
}
