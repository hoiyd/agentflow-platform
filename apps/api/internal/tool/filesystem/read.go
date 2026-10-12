package filesystem

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
)

const maxFileBytes = 1024 * 1024

type textFile struct {
	content, hash, identity string
	bytes, redactions       int
}
type ReadResult struct {
	Root          string `json:"root"`
	Path          string `json:"path"`
	RootRevision  string `json:"root_revision"`
	FileID        string `json:"file_id"`
	ContentHash   string `json:"content_hash"`
	Content       string `json:"content"`
	Offset        int    `json:"offset"`
	NextOffset    int    `json:"next_offset"`
	TotalBytes    int    `json:"total_bytes"`
	ObservedBytes int    `json:"observed_bytes"`
	Truncated     bool   `json:"truncated"`
	Redactions    int    `json:"redactions"`
	TrustBoundary string `json:"trust_boundary"`
}

func (s *Service) readText(ctx context.Context, root rootRecord, path string, byteLimit int) (textFile, error) {
	if err := ctx.Err(); err != nil {
		return textFile{}, err
	}
	file, err := s.open(ctx, root, path)
	if err != nil {
		return textFile{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return textFile{}, failure(tool.ErrorExecutionFailed, "Filesystem file metadata unavailable")
	}
	if !before.Mode().IsRegular() {
		return textFile{}, failure(tool.ErrorSecurityPolicyDenied, "Only regular text files are readable")
	}
	byteLimit = min(byteLimit, maxFileBytes)
	if before.Size() > int64(byteLimit) {
		return textFile{}, failure("filesystem_file_limit", "File exceeds the bounded text read limit")
	}
	data := make([]byte, 0, int(before.Size()))
	buffer := make([]byte, 32768)
	for {
		if err = ctx.Err(); err != nil {
			return textFile{}, err
		}
		n, readErr := file.Read(buffer[:min(len(buffer), byteLimit-len(data)+1)])
		data = append(data, buffer[:n]...)
		if len(data) > byteLimit {
			return textFile{bytes: len(data)}, failure("filesystem_file_limit", "File exceeds the bounded text read limit")
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return textFile{}, failure(tool.ErrorExecutionFailed, "Filesystem text read failed")
		}
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return textFile{}, failure("filesystem_file_changed", "File changed during reading; retry from offset 0")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return textFile{bytes: len(data)}, failure("filesystem_binary_unsupported", "Only UTF-8 text without NUL bytes is supported")
	}
	identity, err := fileIdentity(file)
	if err != nil {
		return textFile{}, failure(tool.ErrorExecutionFailed, "Filesystem file identity unavailable")
	}
	content, count := redaction.Text(string(data))
	return textFile{content: content, hash: fmt.Sprintf("%x", sha256.Sum256(data)), identity: identity, bytes: len(data), redactions: count}, nil
}

func (s *Service) read(ctx context.Context, input arguments) (any, error) {
	root, err := s.authorizedRoot(ctx, input.Root)
	if err != nil {
		return nil, err
	}
	if input.Offset > 0 && input.ExpectedHash == "" {
		return nil, failure(tool.ErrorInvalidArgs, "Continuation pages require expected_hash")
	}
	file, err := s.readText(ctx, root, input.Path, maxFileBytes)
	if err != nil {
		return nil, err
	}
	if input.ExpectedHash != "" && input.ExpectedHash != file.hash {
		return nil, failure("filesystem_file_changed", "File content changed; restart reading at offset 0")
	}
	if input.Limit == 0 {
		input.Limit = 4096
	}
	if input.Offset < 0 || input.Offset > len(file.content) || input.Offset < len(file.content) && !utf8.RuneStart(file.content[input.Offset]) {
		return nil, failure(tool.ErrorInvalidArgs, "Filesystem page offset is invalid")
	}
	end := min(len(file.content), input.Offset+input.Limit)
	for end > input.Offset && end < len(file.content) && !utf8.RuneStart(file.content[end]) {
		end--
	}
	return ReadResult{Root: input.Root, Path: input.Path, RootRevision: s.revision, FileID: file.identity, ContentHash: file.hash, Content: file.content[input.Offset:end], Offset: input.Offset, NextOffset: end, TotalBytes: len(file.content), ObservedBytes: file.bytes, Truncated: end < len(file.content), Redactions: file.redactions, TrustBoundary: trustBoundary}, nil
}
