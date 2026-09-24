package contextassembly

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

const messageOverheadTokens = 4

func newManifest(ctx context.Context, model string, config domain.ContextAssemblyConfig, inputBudget int, selectedTokens int, entries []domain.ContextManifestEntry, hash string, compaction *domain.ContextCompaction) domain.ContextManifest {
	scope := eventpkg.ScopeFromContext(ctx)
	excluded := 0
	for _, entry := range entries {
		if !entry.Selected {
			excluded += entry.EstimatedTokens
		}
	}
	manifest := domain.ContextManifest{
		ID: newID("ctx"), ModelCallID: newID("call"), RunID: scope.RunID, StageID: scope.StageID,
		TurnID: scope.TurnID, Model: model, AssemblerVersion: config.AssemblerVersion,
		ContextWindowTokens: config.ContextWindowTokens, OutputReserveTokens: config.OutputReserveTokens,
		SafetyMarginTokens: config.SafetyMarginTokens, InputBudgetTokens: inputBudget,
		EstimatedInputTokens: selectedTokens, ExcludedTokens: excluded,
		PrefixHash: hash, Entries: entries, CreatedAt: time.Now().UTC(),
	}
	if compaction != nil {
		manifest.CompactionID = compaction.ID
		manifest.CompactionGeneration = compaction.Generation
	}
	return manifest
}

func compactText(value string, maxTokens int) string {
	marker := fmt.Sprintf("\n...[tool result compacted; original_bytes=%d]...\n", len(value))
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		head := (mid * 2) / 3
		candidate := string(runes[:head]) + marker + string(runes[len(runes)-(mid-head):])
		if EstimateTokens(candidate) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	head := (low * 2) / 3
	return string(runes[:head]) + marker + string(runes[len(runes)-(low-head):])
}

func publishManifest(ctx context.Context, sink eventpkg.Sink, manifest domain.ContextManifest) error {
	if sink == nil {
		return nil
	}
	payload, err := eventpkg.Payload(eventpkg.ContextAssembledPayload{Manifest: manifest})
	if err != nil {
		return err
	}
	scope := eventpkg.ScopeFromContext(ctx)
	return sink.Publish(ctx, domain.RunEvent{
		Type: domain.EventContextAssembled, RunID: scope.RunID, ConversationID: scope.ConversationID,
		StageID: scope.StageID, TurnID: scope.TurnID, Payload: payload, Timestamp: manifest.CreatedAt,
	})
}

func prefixHash(messages []Message, tools []Tool) string {
	hasher := sha256.New()
	for _, message := range messages {
		if message.Source == SourceSystem || message.Role == "system" {
			_, _ = hasher.Write([]byte(message.Content))
			break
		}
	}
	for _, tool := range tools {
		encoded, _ := json.Marshal(tool.Definition)
		_, _ = hasher.Write(encoded)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func estimateMessageTokens(message Message) int {
	return messageOverheadTokens + EstimateTokens(message.Role) + EstimateTokens(message.Content) + EstimateTokens(message.ToolCallID) + EstimateTokens(string(message.ToolCalls))
}

func EstimateTokens(value string) int {
	if value == "" {
		return 0
	}
	asciiBytes := 0
	nonASCII := 0
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r <= 127 {
			asciiBytes += size
		} else {
			nonASCII++
		}
		value = value[size:]
	}
	return max(1, (asciiBytes+3)/4+nonASCII)
}

func appendCandidateEntries(entries []domain.ContextManifestEntry, items []candidate) []domain.ContextManifestEntry {
	for _, item := range items {
		entries = append(entries, item.entry)
	}
	return entries
}

func cloneMessages(messages []Message) []Message {
	items := make([]Message, len(messages))
	copy(items, messages)
	for index := range items {
		items[index].ToolCalls = append(json.RawMessage(nil), items[index].ToolCalls...)
	}
	return items
}

func newID(prefix string) string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err == nil {
		return prefix + "_" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
