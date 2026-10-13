package filesystem

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
)

type writeStore interface {
	SaveToolEffectIntent(string, []byte) error
	FailToolEffect(string, string) error
	ListToolEffects(string) ([]domain.ToolEffectRecord, error)
}

// WriteResult doubles as the pre-publication evidence. No old/new source text,
// host paths or executable payloads are copied into the durable receipt.
type WriteResult struct {
	Root         string `json:"root"`
	Path         string `json:"path"`
	RootRevision string `json:"root_revision"`
	Operation    string `json:"operation"`
	OldHash      string `json:"old_hash,omitempty"`
	NewHash      string `json:"new_hash"`
	FileID       string `json:"file_id"`
	Bytes        int    `json:"bytes"`
}

type writeOperations struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	publish func(*os.File, string, string, bool) error
}

func defaultWriteOperations() writeOperations {
	return writeOperations{
		write: func(file *os.File, data []byte) (int, error) { return file.Write(data) },
		sync:  (*os.File).Sync, publish: publishOutput,
	}
}

func validateWritePath(path string) error {
	if err := validatePath(path); err != nil {
		return err
	}
	if strings.Contains(path, "/") || strings.HasPrefix(path, ".") || len(path) > 180 {
		return failure(tool.ErrorSecurityPolicyDenied, "Writes require a flat, non-hidden report filename")
	}
	for _, character := range path {
		if unicode.IsControl(character) {
			return failure(tool.ErrorSecurityPolicyDenied, "Report filenames cannot contain control characters")
		}
	}
	if _, count := redaction.Text(path); count > 0 {
		return failure(tool.ErrorSecurityPolicyDenied, "Report filenames cannot contain recognized credentials")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".json", ".csv":
		return nil
	}
	return failure(tool.ErrorSecurityPolicyDenied, "Only .md/.txt/.json/.csv reports are writable")
}

func validateWriteText(text string) error {
	if len(text) > maxWriteBytes {
		return failure("filesystem_write_limit", "Report exceeds the 64 KiB write limit")
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return failure("filesystem_binary_unsupported", "Only UTF-8 text without NUL bytes is writable")
	}
	if _, count := redaction.Text(text); count > 0 {
		return failure(tool.ErrorSecurityPolicyDenied, "Recognized credentials cannot be written to report files")
	}
	return nil
}

func (s *Service) write(ctx context.Context, operation string, input writeArguments) (value any, err error) {
	identity := tool.EffectIdentityFromContext(ctx)
	storage, ok := s.store.(writeStore)
	scope := event.ScopeFromContext(ctx)
	if !ok || identity.Key == "" || identity.RunID != scope.RunID || identity.StageID != scope.StageID || identity.TurnID != scope.TurnID || identity.Tool != operation {
		return nil, failure(tool.ErrorIdempotencyRequired, "Filesystem writes require the prepared Executor identity and matching execution scope")
	}
	published := false
	defer func() {
		if err != nil && !published && ctx.Err() == nil {
			_ = storage.FailToolEffect(identity.Key, err.Error())
		}
	}()
	if err = validateWritePath(input.Path); err != nil {
		return nil, err
	}
	root, err := s.authorizedRoot(ctx, input.Root)
	if err != nil {
		return nil, err
	}
	if root.WriteAccess != "managed_output" {
		return nil, failure(tool.ErrorSecurityPolicyDenied, "Root is read-only; an operator must grant a dedicated managed output root")
	}
	dir, err := lockOutput(ctx, root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	before, err := inspectOutput(dir, input.Path)
	if err != nil {
		return nil, err
	}
	content := input.Content
	if input.ExpectedHash == "" {
		if operation == "file_patch" || before.exists {
			return nil, failure("filesystem_write_conflict", "Create target already exists, or patch is missing expected_hash")
		}
	} else {
		if !before.exists || before.hash != input.ExpectedHash {
			return nil, failure("filesystem_write_conflict", "Report changed; read current content and use its expected_hash")
		}
		if operation == "file_patch" {
			if input.OldText == "" || strings.Count(before.content, input.OldText) != 1 {
				return nil, failure("filesystem_write_conflict", "Patch requires exactly one literal occurrence")
			}
			content = strings.Replace(before.content, input.OldText, input.NewText, 1)
		}
	}
	if err = validateWriteText(content); err != nil {
		return nil, err
	}
	tempName := fmt.Sprintf(".agentflow-%x.tmp", randomBytes())
	temp, err := createOutputTemp(dir, tempName)
	if err != nil {
		return nil, failure(tool.ErrorExecutionFailed, "Cannot create private staging file")
	}
	defer temp.Close()
	defer func() { _ = removeOutputTemp(dir, tempName) }()
	data := []byte(content)
	if n, writeErr := s.writes.write(temp, data); writeErr != nil || n != len(data) {
		return nil, failure(tool.ErrorExecutionFailed, "Report staging write failed or was incomplete")
	}
	if err = s.writes.sync(temp); err != nil {
		return nil, failure(tool.ErrorExecutionFailed, "Report staging sync failed")
	}
	fileID, err := fileIdentity(temp)
	if err != nil {
		return nil, failure(tool.ErrorExecutionFailed, "Report staging identity unavailable")
	}
	result := WriteResult{Root: input.Root, Path: input.Path, RootRevision: s.revision, OldHash: before.hash, NewHash: contentHash(data), FileID: fileID, Bytes: len(data), Operation: "create"}
	if before.exists {
		result.Operation = "replace"
	}
	if operation == "file_patch" {
		result.Operation = "patch"
	}
	intent, _ := json.Marshal(result)
	if err = storage.SaveToolEffectIntent(identity.Key, intent); err != nil {
		return nil, failure(tool.ErrorEffectJournal, "Cannot persist report intent before publication")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if _, err = s.authorizedRoot(ctx, input.Root); err != nil {
		return nil, err
	}
	// Hash plus object identity is rechecked while all managed writers share the
	// root lock. Noncooperating editors are outside this exclusive-root contract.
	current, err := inspectOutput(dir, input.Path)
	if err != nil {
		return nil, err
	}
	if current.exists != before.exists || current.hash != before.hash || current.identity != before.identity {
		return nil, failure("filesystem_write_conflict", "Report target changed before publication")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// Once publication is attempted, an I/O failure is not proof of no effect.
	// Only the kernel's create-only EEXIST result is a confirmed rejection.
	published = true
	if err = s.writes.publish(dir, tempName, input.Path, !before.exists); err != nil {
		if errors.Is(err, os.ErrExist) {
			published = false
			return nil, failure("filesystem_write_conflict", "Report target appeared before publication")
		}
		return nil, failure(tool.ErrorEffectReconciliation, "Report publication could not be confirmed; reconciliation is required")
	}
	if err = s.writes.sync(dir); err != nil {
		return nil, failure(tool.ErrorEffectReconciliation, "Report was published but directory durability is uncertain")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func randomBytes() []byte {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic("filesystem staging entropy unavailable")
	}
	return data
}

type outputState struct {
	exists                  bool
	content, hash, identity string
}

func readOutputFile(file *os.File) (outputState, error) {
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0 {
		return outputState{}, failure(tool.ErrorSecurityPolicyDenied, "Report target must be a non-executable regular file")
	}
	if info.Size() > maxWriteBytes {
		return outputState{}, failure("filesystem_write_limit", "Existing report exceeds the write limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWriteBytes+1))
	if err != nil {
		return outputState{}, failure(tool.ErrorExecutionFailed, "Cannot inspect report target")
	}
	if err = validateWriteText(string(data)); err != nil {
		return outputState{}, err
	}
	identity, err := fileIdentity(file)
	if err != nil {
		return outputState{}, failure(tool.ErrorExecutionFailed, "Report target identity unavailable")
	}
	return outputState{exists: true, content: string(data), hash: contentHash(data), identity: identity}, nil
}
