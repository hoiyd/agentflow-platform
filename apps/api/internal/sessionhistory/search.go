package sessionhistory

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

type Store interface {
	ListMessages(conversationID string) ([]domain.Message, error)
	ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error)
}

type Query struct {
	ConversationID       string
	Keywords             []string
	MessageIDs           []string
	EventIDs             []string
	EventTypes           []domain.RunEventType
	ExcludeRunID         string
	ExcludeLatestMessage bool
	Roles                []string
	From                 *time.Time
	To                   *time.Time
	NeighborWindow       int
	MaxResults           int
	MaxCharacters        int
	MaxTokens            int
	ExcludeReferences    map[string]bool
}

type Result struct {
	Items         []domain.RetrievedSessionHistory
	DirectMatches int
	Truncated     bool
}

type candidate struct {
	item     domain.RetrievedSessionHistory
	position int
	priority int
}

const maxQueryKeywords = 32

func Search(store Store, query Query) (Result, error) {
	query = normalizeQuery(query)
	messages, err := store.ListMessages(query.ConversationID)
	if err != nil {
		return Result{}, err
	}
	events, err := store.ListConversationRunEvents(query.ConversationID)
	if err != nil {
		return Result{}, err
	}

	messageCandidates := messageCandidates(messages, query)
	eventCandidates := eventCandidates(events, query)
	all := append(messageCandidates, eventCandidates...)
	all = append(all, adjacentMessageCandidates(messages, messageCandidates, query)...)
	all = append(all, adjacentEventCandidates(events, eventCandidates, query)...)
	all = deduplicate(all, query.ExcludeReferences)
	direct := 0
	for _, item := range all {
		if item.item.DirectMatch {
			direct++
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].priority != all[j].priority {
			return all[i].priority < all[j].priority
		}
		return all[i].item.CreatedAt.Before(all[j].item.CreatedAt)
	})

	selected, truncated := applyLimits(all, query)
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].CreatedAt.Equal(selected[j].CreatedAt) {
			return selected[i].Reference < selected[j].Reference
		}
		return selected[i].CreatedAt.Before(selected[j].CreatedAt)
	})
	return Result{Items: selected, DirectMatches: direct, Truncated: truncated}, nil
}

func messageCandidates(messages []domain.Message, query Query) []candidate {
	items := make([]candidate, 0)
	for index, message := range messages {
		if query.ExcludeLatestMessage && index == len(messages)-1 {
			continue
		}
		if !withinRange(message.CreatedAt, query.From, query.To) || !matchesRole(message.Role, query.Roles) {
			continue
		}
		if len(query.EventIDs) > 0 || len(query.EventTypes) > 0 || (len(query.MessageIDs) > 0 && !containsFold(query.MessageIDs, message.ID)) {
			continue
		}
		matchedKeyword := matchesKeyword(message.ID+"\n"+message.Role+"\n"+message.Content, query.Keywords)
		if len(query.Keywords) > 0 && !matchedKeyword {
			continue
		}
		reason := "filter_match"
		if len(query.MessageIDs) > 0 {
			reason = "source_id_match"
		} else if matchedKeyword {
			reason = "keyword_match"
		}
		items = append(items, candidate{position: index, priority: matchPriority(reason), item: domain.RetrievedSessionHistory{
			Reference: "message:" + message.ID, SourceKind: domain.SessionHistorySourceMessage,
			MessageID: message.ID, Role: message.Role, Content: message.Content,
			MatchReason: reason, DirectMatch: true, CreatedAt: message.CreatedAt,
		}})
	}
	return items
}

func eventCandidates(events []domain.RunEvent, query Query) []candidate {
	items := make([]candidate, 0)
	for index, event := range events {
		if event.RunID == query.ExcludeRunID {
			continue
		}
		if len(query.EventIDs) == 0 && len(query.EventTypes) == 0 && query.From == nil && query.To == nil && len(query.Keywords) > 0 && !defaultSearchableEventType(event.Type) {
			continue
		}
		if !withinRange(event.Timestamp, query.From, query.To) || !matchesEventType(event.Type, query.EventTypes) {
			continue
		}
		payload, _ := json.Marshal(event.Payload)
		content := string(payload)
		if len(query.MessageIDs) > 0 || len(query.Roles) > 0 || (len(query.EventIDs) > 0 && !containsFold(query.EventIDs, event.ID)) {
			continue
		}
		matchedKeyword := matchesKeyword(event.ID+"\n"+string(event.Type)+"\n"+content, query.Keywords)
		if len(query.Keywords) > 0 && !matchedKeyword {
			continue
		}
		reason := "filter_match"
		if len(query.EventIDs) > 0 {
			reason = "source_id_match"
		} else if len(query.EventTypes) > 0 {
			reason = "event_type_match"
		} else if matchedKeyword {
			reason = "keyword_match"
		}
		items = append(items, candidate{position: index, priority: matchPriority(reason), item: domain.RetrievedSessionHistory{
			Reference: "event:" + event.ID, SourceKind: domain.SessionHistorySourceEvent,
			EventID: event.ID, RunID: event.RunID, EventType: event.Type, Content: content,
			MatchReason: reason, DirectMatch: true, CreatedAt: event.Timestamp,
		}})
	}
	return items
}

func defaultSearchableEventType(eventType domain.RunEventType) bool {
	switch eventType {
	case domain.EventToolStarted, domain.EventToolCompleted, domain.EventToolFailed,
		domain.EventModelFailed, domain.EventStageCompleted, domain.EventStageFailed,
		domain.EventRunFailed, domain.EventVerificationFailed, domain.EventVerificationBlocked,
		domain.EventCompactionFailed, domain.EventBudgetExceeded:
		return true
	default:
		return false
	}
}

func adjacentMessageCandidates(messages []domain.Message, direct []candidate, query Query) []candidate {
	if query.NeighborWindow == 0 {
		return nil
	}
	items := make([]candidate, 0, len(direct)*query.NeighborWindow*2)
	for _, match := range direct {
		for offset := -query.NeighborWindow; offset <= query.NeighborWindow; offset++ {
			index := match.position + offset
			if offset == 0 || index < 0 || index >= len(messages) || (query.ExcludeLatestMessage && index == len(messages)-1) {
				continue
			}
			message := messages[index]
			if !withinRange(message.CreatedAt, query.From, query.To) {
				continue
			}
			items = append(items, candidate{position: index, priority: 3 + abs(offset), item: domain.RetrievedSessionHistory{
				Reference: "message:" + message.ID, SourceKind: domain.SessionHistorySourceMessage,
				MessageID: message.ID, Role: message.Role, Content: message.Content,
				MatchReason: "adjacent_window", CreatedAt: message.CreatedAt,
			}})
		}
	}
	return items
}

func adjacentEventCandidates(events []domain.RunEvent, direct []candidate, query Query) []candidate {
	if query.NeighborWindow == 0 {
		return nil
	}
	items := make([]candidate, 0, len(direct)*query.NeighborWindow*2)
	for _, match := range direct {
		for offset := -query.NeighborWindow; offset <= query.NeighborWindow; offset++ {
			index := match.position + offset
			if offset == 0 || index < 0 || index >= len(events) {
				continue
			}
			event := events[index]
			if event.RunID == query.ExcludeRunID || !withinRange(event.Timestamp, query.From, query.To) {
				continue
			}
			payload, _ := json.Marshal(event.Payload)
			items = append(items, candidate{position: index, priority: 3 + abs(offset), item: domain.RetrievedSessionHistory{
				Reference: "event:" + event.ID, SourceKind: domain.SessionHistorySourceEvent,
				EventID: event.ID, RunID: event.RunID, EventType: event.Type, Content: string(payload),
				MatchReason: "adjacent_window", CreatedAt: event.Timestamp,
			}})
		}
	}
	return items
}

func matchesKeyword(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func applyLimits(candidates []candidate, query Query) ([]domain.RetrievedSessionHistory, bool) {
	selected := make([]domain.RetrievedSessionHistory, 0, min(query.MaxResults, len(candidates)))
	characters := 0
	tokens := 0
	truncated := false
	for _, candidate := range candidates {
		if len(selected) >= query.MaxResults {
			truncated = true
			break
		}
		remainingChars := query.MaxCharacters - characters
		remainingTokens := query.MaxTokens - tokens
		if remainingChars <= 0 || remainingTokens <= 0 {
			truncated = true
			break
		}
		item := candidate.item
		item.OriginalBytes = len(item.Content)
		content, clipped := boundText(item.Content, remainingChars, remainingTokens)
		if content == "" {
			truncated = true
			continue
		}
		item.Content = content
		item.Truncated = clipped
		selected = append(selected, item)
		characters += utf8.RuneCountInString(content)
		tokens += estimateTokens(content)
		truncated = truncated || clipped
	}
	return selected, truncated || len(selected) < len(candidates)
}

func boundText(value string, maxCharacters int, maxTokens int) (string, bool) {
	if utf8.RuneCountInString(value) <= maxCharacters && estimateTokens(value) <= maxTokens {
		return value, false
	}
	if maxCharacters <= 0 || maxTokens <= 0 {
		return "", true
	}
	runes := []rune(value)
	marker := "\n...[history source truncated]...\n"
	low, high := 0, min(maxCharacters, len(runes))
	for low < high {
		mid := (low + high + 1) / 2
		head := mid * 2 / 3
		candidate := string(runes[:head]) + marker + string(runes[len(runes)-(mid-head):])
		if utf8.RuneCountInString(candidate) <= maxCharacters && estimateTokens(candidate) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	keep := low
	head := keep * 2 / 3
	return string(runes[:head]) + marker + string(runes[len(runes)-(keep-head):]), true
}

func deduplicate(candidates []candidate, excluded map[string]bool) []candidate {
	seen := make(map[string]bool, len(candidates))
	items := make([]candidate, 0, len(candidates))
	for _, item := range candidates {
		if excluded[item.item.Reference] || seen[item.item.Reference] {
			continue
		}
		seen[item.item.Reference] = true
		items = append(items, item)
	}
	return items
}

func normalizeQuery(query Query) Query {
	query.ConversationID = strings.TrimSpace(query.ConversationID)
	query.Keywords = normalizedValues(query.Keywords)
	query.MessageIDs = normalizedValues(query.MessageIDs)
	query.EventIDs = normalizedValues(query.EventIDs)
	query.Roles = normalizedValues(query.Roles)
	if query.NeighborWindow < 0 {
		query.NeighborWindow = 0
	}
	if query.MaxResults <= 0 {
		query.MaxResults = 8
	}
	if query.MaxCharacters <= 0 {
		query.MaxCharacters = 12000
	}
	if query.MaxTokens <= 0 {
		query.MaxTokens = 3000
	}
	return query
}

func Keywords(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		return unicode.IsPunct(r) && r != '-' && r != '_' && r != '.' && r != ':'
	})
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 2 || stopWords[strings.ToLower(part)] {
			continue
		}
		items = append(items, part)
		if isHanText(part) && len([]rune(part)) > 3 {
			items = append(items, hanBigrams(part)...)
		}
	}
	items = normalizedValues(items)
	if len(items) > maxQueryKeywords {
		items = items[:maxQueryKeywords]
	}
	return items
}

func isHanText(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func hanBigrams(value string) []string {
	runes := []rune(value)
	items := make([]string, 0, len(runes)-1)
	for index := 0; index+1 < len(runes); index++ {
		part := string(runes[index : index+2])
		if !stopWords[part] {
			items = append(items, part)
		}
	}
	return items
}

var stopWords = map[string]bool{
	"the": true, "and": true, "that": true, "this": true, "with": true,
	"what": true, "when": true, "where": true, "please": true,
	"请": true, "帮我": true, "一下": true, "这个": true, "那个": true,
}

func matchesRole(role string, roles []string) bool {
	return len(roles) == 0 || containsFold(roles, role)
}
func matchesEventType(value domain.RunEventType, types []domain.RunEventType) bool {
	return len(types) == 0 || containsEventType(types, value)
}
func containsEventType(values []domain.RunEventType, value domain.RunEventType) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func containsFold(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}
func withinRange(value time.Time, from *time.Time, to *time.Time) bool {
	return (from == nil || !value.Before(*from)) && (to == nil || !value.After(*to))
}
func normalizedValues(values []string) []string {
	seen := map[string]bool{}
	items := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, value)
	}
	return items
}
func matchPriority(reason string) int {
	switch reason {
	case "source_id_match":
		return 0
	case "event_type_match", "filter_match":
		return 1
	default:
		return 2
	}
}
func estimateTokens(value string) int {
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
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
