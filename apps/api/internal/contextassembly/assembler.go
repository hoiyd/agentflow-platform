package contextassembly

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type candidate struct {
	messageIndex   int
	formatted      string
	entry          domain.ContextManifestEntry
	selectedReason string
}

func Assemble(ctx context.Context, request Request) (Pack, error) {
	session, active := sessionFromContext(ctx)
	if !active {
		return Pack{Messages: cloneMessages(request.Messages)}, nil
	}
	config := NormalizeConfig(session.Config)
	inputBudget := config.ContextWindowTokens - config.OutputReserveTokens - config.SafetyMarginTokens
	var taskState *domain.TaskState
	if session.LoadTaskState != nil {
		loaded, ok, err := session.LoadTaskState()
		if err != nil {
			return Pack{}, fmt.Errorf("load structured task state: %w", err)
		}
		if ok {
			taskState = &loaded
		}
	}
	messages := normalizeMessages(mergeSessionHistory(request.Messages, session))
	messages = applyRetrievedContextTrustPolicy(messages)
	entries := make([]domain.ContextManifestEntry, 0, len(messages)+len(request.Tools)+len(session.Memories)+len(session.Knowledge)+1)
	messageCandidates := make([]candidate, 0, len(messages))
	requiredTokens := 0

	for index := range messages {
		transformation := "original"
		originalBytes := len(messages[index].Content)
		artifactIDs := toolArtifactIDs(messages[index])
		if len(artifactIDs) > 0 {
			transformation = "tool_result_artifact_preview"
		}
		if (messages[index].Source == SourceToolResult || messages[index].Role == "tool") && EstimateTokens(messages[index].Content) > config.ToolResultMaxTokens {
			messages[index].Content = compactText(messages[index].Content, config.ToolResultMaxTokens)
			if len(artifactIDs) > 0 {
				transformation = "tool_result_artifact_preview_compacted"
			} else {
				transformation = "tool_result_compacted"
			}
		}
		message := messages[index]
		tokens := estimateMessageTokens(message)
		required := isRequiredSource(message.Source)
		entry := domain.ContextManifestEntry{
			Source: message.Source, ReferenceID: message.ReferenceID, Role: message.Role,
			Selected: required, Reason: reasonForMessage(message.Source, required),
			Transformation: transformation, EstimatedTokens: tokens, OriginalBytes: originalBytes,
			ArtifactIDs: artifactIDs,
		}
		if required {
			entry.IncludedBytes = len(message.Content)
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
	historySearchCandidates := historySearchCandidates(session.HistorySearch)
	compactionCandidate := contextCompactionCandidate(session.Compaction)
	taskStateCandidate := structuredTaskStateCandidate(taskState)
	selectedTokens := requiredTokens
	if compactionCandidate != nil {
		selectedTokens += compactionCandidate.entry.EstimatedTokens
		requiredTokens += compactionCandidate.entry.EstimatedTokens
	}
	if taskStateCandidate != nil {
		selectedTokens += taskStateCandidate.entry.EstimatedTokens
		requiredTokens += taskStateCandidate.entry.EstimatedTokens
	}
	if requiredTokens <= inputBudget {
		selectedTokens += selectRelevant(historySearchCandidates, config.HistoryRetrievalMaxTokens, inputBudget-selectedTokens, "history_retrieval_budget_exceeded")
		suppressRetrievedHistoryDuplicates(messageCandidates, historySearchCandidates)
		selectedTokens += selectRecentHistory(messageCandidates, config.HistoryMaxTokens, inputBudget-selectedTokens)
		selectedTokens += selectRelevant(memoryCandidates, config.MemoryMaxTokens, inputBudget-selectedTokens, "memory_budget_exceeded")
		selectedTokens += selectRelevant(knowledgeCandidates, config.KnowledgeMaxTokens, inputBudget-selectedTokens, "knowledge_budget_exceeded")
	} else {
		excludeOptional(messageCandidates, "input_budget_exceeded")
		excludeOptional(historySearchCandidates, "input_budget_exceeded")
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
	entries = appendCandidateEntries(entries, historySearchCandidates)
	if compactionCandidate != nil {
		entries = append(entries, compactionCandidate.entry)
	}
	if taskStateCandidate != nil {
		entries = append(entries, taskStateCandidate.entry)
	}
	packedMessages = injectSelectedContext(packedMessages, taskStateCandidate, compactionCandidate, historySearchCandidates, memoryCandidates, knowledgeCandidates)

	manifest := newManifest(ctx, request.Model, config, inputBudget, selectedTokens, entries, prefixHash(messages, request.Tools), session.Compaction)
	if err := publishManifest(ctx, session.Sink, manifest); err != nil {
		return Pack{}, fmt.Errorf("persist context manifest: %w", err)
	}
	if requiredTokens > inputBudget {
		return Pack{Messages: packedMessages, Manifest: manifest}, &InputBudgetError{RequiredTokens: requiredTokens, AvailableTokens: inputBudget}
	}
	return Pack{Messages: packedMessages, Manifest: manifest}, nil
}

func toolArtifactIDs(message Message) []string {
	if message.Source != SourceToolResult && message.Role != "tool" {
		return nil
	}
	var envelope struct {
		Artifact *struct {
			ID string `json:"id"`
		} `json:"artifact"`
		Result struct {
			Artifact *struct {
				ID string `json:"id"`
			} `json:"artifact"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(message.Content), &envelope) != nil {
		return nil
	}
	ids := make([]string, 0, 2)
	if envelope.Artifact != nil && strings.TrimSpace(envelope.Artifact.ID) != "" {
		ids = append(ids, envelope.Artifact.ID)
	}
	if envelope.Result.Artifact != nil && strings.TrimSpace(envelope.Result.Artifact.ID) != "" &&
		(len(ids) == 0 || ids[0] != envelope.Result.Artifact.ID) {
		ids = append(ids, envelope.Result.Artifact.ID)
	}
	return ids
}

func suppressRetrievedHistoryDuplicates(messages []candidate, retrieved []candidate) {
	selected := make(map[string]bool)
	for _, item := range retrieved {
		if item.entry.Selected && strings.HasPrefix(item.entry.ReferenceID, "message:") {
			selected[strings.TrimPrefix(item.entry.ReferenceID, "message:")] = true
		}
	}
	for index := range messages {
		if messages[index].entry.Source == SourceHistory && selected[messages[index].entry.ReferenceID] {
			messages[index].entry.Reason = "superseded_by_history_retrieval"
			messages[index].entry.EstimatedTokens = 0
		}
	}
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
	merged := messages
	for _, message := range messages {
		if message.Source == SourceHistory {
			return excludeCompactedHistory(merged, session.Compaction)
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
		return excludeCompactedHistory(merged, session.Compaction)
	}
	insertAt := len(messages)
	for index, message := range messages {
		if message.Source == SourceCurrentInput || message.Role == "user" {
			insertAt = index
			break
		}
	}
	merged = make([]Message, 0, len(messages)+len(prior))
	merged = append(merged, messages[:insertAt]...)
	merged = append(merged, prior...)
	merged = append(merged, messages[insertAt:]...)
	return excludeCompactedHistory(merged, session.Compaction)
}

func excludeCompactedHistory(messages []Message, compaction *domain.ContextCompaction) []Message {
	if compaction == nil || len(compaction.SourceMessageIDs) == 0 {
		return messages
	}
	covered := make(map[string]bool, len(compaction.SourceMessageIDs))
	for _, id := range compaction.SourceMessageIDs {
		covered[id] = true
	}
	filtered := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Source == SourceHistory && covered[message.ReferenceID] {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
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
		entry.Reason = items[index].selectedReason
		if entry.Reason == "" {
			entry.Reason = "relevant"
		}
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
