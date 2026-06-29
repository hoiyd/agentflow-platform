package httpapi

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func (h *Handler) summarizeConversationTitleBestEffort(ctx context.Context, conversationID string, userMessage string, assistantMessage string) string {
	conversation, ok, err := h.store.GetConversation(conversationID)
	if err != nil || !ok {
		if err != nil {
			log.Printf("conversation_title skip=get_conversation_failed conversation_id=%s error=%v", conversationID, err)
		}
		return ""
	}
	if !shouldAutoTitle(conversation.Title, userMessage) {
		return conversation.Title
	}

	title, err := h.generateConversationTitle(ctx, userMessage, assistantMessage)
	if err != nil {
		log.Printf("conversation_title skip=llm_failed conversation_id=%s error=%v", conversationID, err)
		return conversation.Title
	}
	if title == "" || strings.EqualFold(title, conversation.Title) {
		return conversation.Title
	}
	if err := h.store.UpdateConversationTitle(conversationID, title); err != nil {
		log.Printf("conversation_title skip=update_failed conversation_id=%s error=%v", conversationID, err)
		return conversation.Title
	}
	return title
}

func (h *Handler) generateConversationTitle(ctx context.Context, userMessage string, assistantMessage string) (string, error) {
	if h.openAI == nil {
		return cleanConversationTitle(userMessage), nil
	}

	titleCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	systemPrompt := "Generate a concise conversation title. Return only the title, no quotes, no punctuation-only wrappers. Use the same language as the user when possible. Keep it under 8 words or 20 Chinese characters."
	prompt := "User message:\n" + truncateForTitle(userMessage, 900) + "\n\nAssistant response:\n" + truncateForTitle(assistantMessage, 900)
	completion, err := h.openAI.CompleteTextDetailed(titleCtx, systemPrompt, prompt)
	if err != nil {
		return "", err
	}
	if completion.Model == "local_fallback" {
		return cleanConversationTitle(userMessage), nil
	}
	return cleanConversationTitle(completion.Text), nil
}

func shouldAutoTitle(currentTitle string, userMessage string) bool {
	currentTitle = strings.TrimSpace(currentTitle)
	if currentTitle == "" || currentTitle == "New conversation" {
		return true
	}
	return currentTitle == cleanConversationTitle(userMessage) || currentTitle == truncateForTitle(userMessage, 80)
}

func cleanConversationTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Trim(title, "\"'`“”‘’ \t\r\n")
	title = strings.TrimSuffix(title, ".")
	title = strings.TrimSuffix(title, "。")
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}

	words := strings.Fields(title)
	if len(words) > 8 {
		title = strings.Join(words[:8], " ")
	}
	return truncateRunes(strings.TrimSpace(title), 80)
}

func truncateForTitle(value string, limit int) string {
	return truncateRunes(strings.TrimSpace(value), limit)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	var builder strings.Builder
	count := 0
	for _, r := range value {
		if count >= limit {
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		builder.WriteRune(r)
		count++
	}
	return strings.TrimSpace(builder.String())
}
