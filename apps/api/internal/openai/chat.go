package openai

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	tracepkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tools"
)

func (c *Client) StreamChat(ctx context.Context, history []domain.Message, latest string) (<-chan string, <-chan error) {
	chunks := make(chan string)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		if c.apiKey == "" {
			log.Printf("chat_fallback mode=local_no_api_key latest_len=%d", len(latest))
			c.streamFallback(ctx, latest, chunks)
			return
		}

		messages := buildMessages(history)
		events := make(chan StreamEvent)
		go func() {
			defer close(events)
			if _, _, _, err := c.streamMessages(ctx, messages, events); err != nil {
				errs <- err
			}
		}()
		for event := range events {
			if event.Type == "delta" {
				chunks <- event.Delta
			}
		}
	}()

	return chunks, errs
}

func (c *Client) StreamChatWithTools(ctx context.Context, history []domain.Message, latest string, catalog *tools.Catalog) (<-chan StreamEvent, <-chan error) {
	return c.StreamAgentChatWithTools(ctx, "You are AgentFlow's assistant. Use tools when they help.", history, latest, catalog)
}

func (c *Client) StreamAgentChatWithTools(ctx context.Context, systemPrompt string, history []domain.Message, latest string, catalog *tools.Catalog) (<-chan StreamEvent, <-chan error) {
	return c.StreamAgentChatWithToolsTrace(ctx, systemPrompt, history, latest, catalog, nil, "", "", nil, nil)
}

func (c *Client) StreamAgentChatWithToolsTrace(ctx context.Context, systemPrompt string, history []domain.Message, latest string, catalog *tools.Catalog, recorder *tracepkg.Recorder, runID string, stepID string, retrievedMemories []domain.RetrievedMemory, retrievedChunks []domain.RetrievedDocumentChunk) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		if c.apiKey == "" {
			log.Printf("chat_fallback mode=local_no_api_key latest_len=%d enabled_tools=%q", len(latest), strings.Join(catalog.EnabledNames(), ","))
			rawMessages := ensureCurrentInput(buildMessagesWithSystemPrompt(systemPrompt, history, catalog.EnabledNames()), latest)
			prepared, err := c.prepareModelContextForModel(ctx, "local_fallback", rawMessages, nil)
			if err != nil {
				errs <- err
				return
			}
			output := fallbackEventResponse(latest)
			callCtx := budget.WithOperation(ctx, prepared.manifest.ModelCallID)
			callCtx = withRequestManifest(callCtx, prepared.manifest)
			reservation, err := beginBudgetedModelCall(callCtx, "local_fallback", estimateTokens(messagesToText(prepared.messages)))
			if err != nil {
				errs <- err
				return
			}
			requestPayload, err := json.Marshal(map[string]any{
				"model": "local_fallback", "messages": prepared.messages, "stream": true, "temperature": 0.4,
			})
			if err != nil {
				errs <- err
				return
			}
			if err := c.recordModelRequest(callCtx, reservation.OperationID, "local.stream", "local_fallback", requestPayload); err != nil {
				errs <- err
				return
			}
			startPayload := mergePayload(map[string]any{
				"model":       "local_fallback",
				"system":      systemPrompt,
				"input":       latest,
				"input_chars": len(latest),
			}, contextTracePayload(prepared.manifest))
			if len(retrievedMemories) > 0 {
				startPayload["retrieved_memories"] = retrievedMemoryPayload(retrievedMemories)
			}
			if len(retrievedChunks) > 0 {
				startPayload["retrieved_chunks"] = retrievedChunkPayload(retrievedChunks)
			}
			span := recorder.LLMStart(ctx, runID, stepID, startPayload)
			c.streamText(ctx, output, 45*time.Millisecond, events)
			usage := estimateUsage(messagesToText(prepared.messages), output)
			if err := settleBudgetedModelCall(callCtx, reservation, usage); err != nil {
				errs <- err
				return
			}
			recorder.LLMEnd(ctx, span, tokenPayload(map[string]any{
				"model":        "local_fallback",
				"output":       output,
				"output_chars": len(output),
			}, usage))
			return
		}

		if err := c.streamOpenAIWithTools(ctx, systemPrompt, history, latest, catalog, events, recorder, runID, stepID, retrievedMemories, retrievedChunks); err != nil {
			errs <- err
		}
	}()

	return events, errs
}

func (c *Client) CompleteText(ctx context.Context, systemPrompt string, prompt string) (string, error) {
	completion, err := c.CompleteTextDetailed(ctx, systemPrompt, prompt)
	if err != nil {
		return "", err
	}
	return completion.Text, nil
}

func (c *Client) CompleteTextDetailed(ctx context.Context, systemPrompt string, prompt string) (TextCompletion, error) {
	prepared, err := c.PrepareText(ctx, systemPrompt, prompt)
	if err != nil {
		return TextCompletion{}, err
	}
	return c.CompletePreparedText(ctx, prepared)
}

func (c *Client) PrepareText(ctx context.Context, systemPrompt string, prompt string) (PreparedText, error) {
	prompt = strings.TrimSpace(prompt)
	model := c.model
	if c.apiKey == "" {
		model = "local_fallback"
	}
	prepared, err := c.prepareModelContextForModel(ctx, model, []Message{
		{Role: "system", Content: strings.TrimSpace(systemPrompt), Source: contextassembly.SourceSystem, ReferenceID: "system_prompt"},
		{Role: "user", Content: prompt, Source: contextassembly.SourceCurrentInput, ReferenceID: "current_input"},
	}, nil)
	if err != nil {
		return PreparedText{}, err
	}
	return PreparedText{Messages: prepared.messages, Manifest: prepared.manifest}, nil
}

func (c *Client) CompletePreparedText(ctx context.Context, prepared PreparedText) (TextCompletion, error) {
	ctx = budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	ctx = withOutputTokenLimit(ctx, prepared.Manifest.OutputReserveTokens)
	ctx = withRequestManifest(ctx, prepared.Manifest)
	systemPrompt, prompt := textPromptParts(prepared.Messages)
	if c.apiKey == "" {
		text := fallbackCompletion(systemPrompt, prompt)
		reservation, err := beginBudgetedModelCall(ctx, "local_fallback", estimateTokens(messagesToText(prepared.Messages)))
		if err != nil {
			return TextCompletion{}, err
		}
		payload, err := json.Marshal(map[string]any{
			"model": "local_fallback", "messages": prepared.Messages, "temperature": 0.2,
		})
		if err != nil {
			return TextCompletion{}, err
		}
		if err := c.recordModelRequest(ctx, reservation.OperationID, "local.completion", "local_fallback", payload); err != nil {
			return TextCompletion{}, err
		}
		usage := estimateUsage(systemPrompt+"\n"+prompt, text)
		if err := settleBudgetedModelCall(ctx, reservation, usage); err != nil {
			return TextCompletion{}, err
		}
		return TextCompletion{
			Text:  text,
			Model: "local_fallback",
			Usage: usage,
		}, nil
	}

	response, err := c.complete(ctx, map[string]any{
		"model":       c.model,
		"messages":    prepared.Messages,
		"temperature": 0.2,
	})
	if err != nil {
		return TextCompletion{}, err
	}
	usage := response.Usage
	if !usage.Valid() {
		usage = estimateUsage(messagesToText(prepared.Messages), strings.TrimSpace(response.Choices[0].Message.Content))
	}
	return TextCompletion{
		Text:  strings.TrimSpace(response.Choices[0].Message.Content),
		Model: c.model,
		Usage: usage,
	}, nil
}
