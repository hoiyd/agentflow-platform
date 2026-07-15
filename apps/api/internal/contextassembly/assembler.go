package contextassembly

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

const messageOverheadTokens = 4

type candidate struct {
	messageIndex int
	formatted    string
	entry        domain.ContextManifestEntry
}

func Assemble(ctx context.Context, request Request) (Pack, error) {
	session, active := sessionFromContext(ctx)
	if !active {
		return Pack{Messages: cloneMessages(request.Messages)}, nil
	}
	policy := NormalizePolicy(session.Policy)
	inputBudget := policy.ContextWindowTokens - policy.OutputReserveTokens - policy.SafetyMarginTokens
	messages := normalizeMessages(mergeSessionHistory(request.Messages, session))
	entries := make([]domain.ContextManifestEntry, 0, len(messages)+len(request.Tools)+len(session.Memories)+len(session.Knowledge))
	messageCandidates := make([]candidate, 0, len(messages))
	requiredTokens := 0

	for index, message := range messages {
		tokens := estimateMessageTokens(message)
		required := isRequiredSource(message.Source)
		entry := domain.ContextManifestEntry{
			Source: message.Source, ReferenceID: message.ReferenceID, Role: message.Role,
			Selected: required, Reason: reasonForMessage(message.Source, required),
			Transformation: "original", EstimatedTokens: tokens, OriginalBytes: len(message.Content),
		}
		if required {
			entry.IncludedBytes = entry.OriginalBytes
			requiredTokens += tokens
		}
		messageCandidates = append(messageCandidates, candidate{messageIndex: index, entry: entry})
	}

	toolCandidates := make([]candidate, 0, len(request.Tools))
	for _, tool := range request.Tools {
		encoded, _ := json.Marshal(tool.Definition)
		tokens := EstimateTokens(string(encoded))
		toolCandidates = append(toolCandidates, candidate{entry: domain.ContextManifestEntry{
			Source: SourceToolDefinition, ReferenceID: tool.Name, Selected: true, Reason: "required",
			Transformation: "original", EstimatedTokens: tokens, OriginalBytes: len(encoded), IncludedBytes: len(encoded),
		}})
		requiredTokens += tokens
	}

	memoryCandidates := memoryCandidates(session.Memories)
	knowledgeCandidates := knowledgeCandidates(session.Knowledge)
	selectedTokens := requiredTokens
	if requiredTokens <= inputBudget {
		selectedTokens += selectRecentHistory(messageCandidates, policy.HistoryMaxTokens, inputBudget-selectedTokens)
		selectedTokens += selectRelevant(memoryCandidates, policy.MemoryMaxTokens, inputBudget-selectedTokens, "memory_budget_exceeded")
		selectedTokens += selectRelevant(knowledgeCandidates, policy.KnowledgeMaxTokens, inputBudget-selectedTokens, "knowledge_budget_exceeded")
	} else {
		excludeOptional(messageCandidates, "input_budget_exceeded")
		excludeOptional(memoryCandidates, "input_budget_exceeded")
		excludeOptional(knowledgeCandidates, "input_budget_exceeded")
	}

	packedMessages := make([]Message, 0, len(messages))
	for index, message := range messages {
		if messageCandidates[index].entry.Selected {
			packedMessages = append(packedMessages, message)
		}
		entries = append(entries, messageCandidates[index].entry)
	}
	for _, item := range toolCandidates {
		entries = append(entries, item.entry)
	}
	entries = appendCandidateEntries(entries, memoryCandidates)
	entries = appendCandidateEntries(entries, knowledgeCandidates)
	packedMessages = injectRetrievedContext(packedMessages, memoryCandidates, knowledgeCandidates)

	manifest := newManifest(ctx, request.Model, policy, inputBudget, selectedTokens, entries, prefixHash(messages, request.Tools))
	if err := publishManifest(ctx, session.Sink, manifest); err != nil {
		return Pack{}, fmt.Errorf("persist context manifest: %w", err)
	}
	if requiredTokens > inputBudget {
		return Pack{Messages: packedMessages, Manifest: manifest}, &InputBudgetError{RequiredTokens: requiredTokens, AvailableTokens: inputBudget}
	}
	return Pack{Messages: packedMessages, Manifest: manifest}, nil
}

func normalizeMessages(messages []Message) []Message {
	items := cloneMessages(messages)
	lastUser := -1
	for index := range items {
		if items[index].Role == "user" {
			lastUser = index
		}
	}
	for index := range items {
		if items[index].Source == "" {
			switch {
			case items[index].Role == "system":
				items[index].Source = SourceSystem
			case items[index].Role == "tool":
				items[index].Source = SourceToolResult
			case len(items[index].ToolCalls) > 0:
				items[index].Source = SourceToolCall
			case index == lastUser:
				items[index].Source = SourceCurrentInput
			default:
				items[index].Source = SourceHistory
			}
		}
		if items[index].ReferenceID == "" {
			items[index].ReferenceID = fmt.Sprintf("message_%d", index+1)
		}
	}
	return items
}

func mergeSessionHistory(messages []Message, session Session) []Message {
	for _, message := range messages {
		if message.Source == SourceHistory {
			return messages
		}
	}
	prior := make([]Message, 0, len(session.History))
	lastUser := -1
	for index := range session.History {
		if session.History[index].Role == "user" {
			lastUser = index
		}
	}
	for index, item := range session.History {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		if index == lastUser && strings.TrimSpace(item.Content) == strings.TrimSpace(session.CurrentInput) {
			continue
		}
		prior = append(prior, Message{
			Source: SourceHistory, ReferenceID: item.ID, Role: item.Role, Content: item.Content,
		})
	}
	if len(prior) == 0 {
		return messages
	}
	insertAt := len(messages)
	for index, message := range messages {
		if message.Source == SourceCurrentInput || message.Role == "user" {
			insertAt = index
			break
		}
	}
	merged := make([]Message, 0, len(messages)+len(prior))
	merged = append(merged, messages[:insertAt]...)
	merged = append(merged, prior...)
	merged = append(merged, messages[insertAt:]...)
	return merged
}

func isRequiredSource(source string) bool {
	switch source {
	case SourceSystem, SourceCurrentInput, SourceToolCall, SourceToolResult:
		return true
	default:
		return false
	}
}

func reasonForMessage(source string, required bool) string {
	if required {
		return "required"
	}
	if source == SourceHistory {
		return "history_budget_exceeded"
	}
	return "input_budget_exceeded"
}

func selectRecentHistory(items []candidate, sourceBudget int, remaining int) int {
	used := 0
	for index := len(items) - 1; index >= 0; index-- {
		entry := &items[index].entry
		if entry.Source != SourceHistory || entry.EstimatedTokens <= 0 {
			continue
		}
		if used+entry.EstimatedTokens > sourceBudget {
			entry.Reason = "history_budget_exceeded"
			continue
		}
		if entry.EstimatedTokens > remaining-used {
			entry.Reason = "input_budget_exceeded"
			continue
		}
		entry.Selected = true
		entry.Reason = "recent"
		entry.IncludedBytes = entry.OriginalBytes
		used += entry.EstimatedTokens
	}
	return used
}

func selectRelevant(items []candidate, sourceBudget int, remaining int, sourceReason string) int {
	used := 0
	for index := range items {
		entry := &items[index].entry
		if entry.EstimatedTokens <= 0 {
			entry.Reason = "empty"
			continue
		}
		if used+entry.EstimatedTokens > sourceBudget {
			entry.Reason = sourceReason
			continue
		}
		if entry.EstimatedTokens > remaining-used {
			entry.Reason = "input_budget_exceeded"
			continue
		}
		entry.Selected = true
		entry.Reason = "relevant"
		entry.IncludedBytes = len(items[index].formatted)
		used += entry.EstimatedTokens
	}
	return used
}

func excludeOptional(items []candidate, reason string) {
	for index := range items {
		if !items[index].entry.Selected {
			items[index].entry.Reason = reason
		}
	}
}

func memoryCandidates(memories []domain.RetrievedMemory) []candidate {
	items := make([]candidate, 0, len(memories))
	for _, memory := range memories {
		content := strings.TrimSpace(memory.Memory.Content)
		if content == "" {
			continue
		}
		formatted := fmt.Sprintf("[memory id=%s kind=%s score=%.4f]\n%s", memory.Memory.ID, memory.Memory.Kind, memory.Score, content)
		items = append(items, candidate{formatted: formatted, entry: domain.ContextManifestEntry{
			Source: SourceMemory, ReferenceID: memory.Memory.ID, Reason: "memory_budget_exceeded",
			Transformation: "injected", EstimatedTokens: EstimateTokens(formatted), OriginalBytes: len(content),
		}})
	}
	return items
}

func knowledgeCandidates(chunks []domain.RetrievedDocumentChunk) []candidate {
	items := make([]candidate, 0, len(chunks))
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk.Chunk.Content)
		if content == "" {
			continue
		}
		formatted := fmt.Sprintf("[knowledge document=%s chunk=%s score=%.4f]\n%s", chunk.Document.Title, chunk.Chunk.ID, chunk.Score, content)
		items = append(items, candidate{formatted: formatted, entry: domain.ContextManifestEntry{
			Source: SourceKnowledge, ReferenceID: chunk.Chunk.ID, Reason: "knowledge_budget_exceeded",
			Transformation: "injected", EstimatedTokens: EstimateTokens(formatted), OriginalBytes: len(content),
		}})
	}
	return items
}

func injectRetrievedContext(messages []Message, memories []candidate, knowledge []candidate) []Message {
	sections := make([]string, 0, 2)
	if selected := selectedFormatted(memories); len(selected) > 0 {
		sections = append(sections, "<memories>\n"+strings.Join(selected, "\n\n")+"\n</memories>")
	}
	if selected := selectedFormatted(knowledge); len(selected) > 0 {
		sections = append(sections, "<knowledge>\n"+strings.Join(selected, "\n\n")+"\n</knowledge>")
	}
	if len(sections) == 0 {
		return messages
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Source == SourceCurrentInput {
			messages[index].Content = strings.TrimSpace(messages[index].Content) + "\n\nContext selected by AgentFlow:\n" + strings.Join(sections, "\n\n")
			break
		}
	}
	return messages
}

func selectedFormatted(items []candidate) []string {
	selected := make([]string, 0, len(items))
	for _, item := range items {
		if item.entry.Selected {
			selected = append(selected, item.formatted)
		}
	}
	return selected
}

func newManifest(ctx context.Context, model string, policy domain.RuntimeContextPolicy, inputBudget int, selectedTokens int, entries []domain.ContextManifestEntry, hash string) domain.ContextManifest {
	scope := eventpkg.ScopeFromContext(ctx)
	excluded := 0
	for _, entry := range entries {
		if !entry.Selected {
			excluded += entry.EstimatedTokens
		}
	}
	return domain.ContextManifest{
		ID: newID("ctx"), ModelCallID: newID("call"), RunID: scope.RunID, StageID: scope.StageID,
		TurnID: scope.TurnID, Model: model, PolicyVersion: policy.Version,
		ContextWindowTokens: policy.ContextWindowTokens, OutputReserveTokens: policy.OutputReserveTokens,
		SafetyMarginTokens: policy.SafetyMarginTokens, InputBudgetTokens: inputBudget,
		EstimatedInputTokens: selectedTokens, ExcludedTokens: excluded,
		PrefixHash: hash, Entries: entries, CreatedAt: time.Now().UTC(),
	}
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
