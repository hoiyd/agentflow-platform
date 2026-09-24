package openai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
)

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
	if !c.HasAPIKey() && !c.simulated {
		return PreparedText{}, missingCredentialError("chat.prepare")
	}
	prompt = strings.TrimSpace(prompt)
	model := c.model
	if c.simulated {
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
	if !c.HasAPIKey() && !c.simulated {
		return TextCompletion{}, missingCredentialError("chat.completion")
	}
	ctx = budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	ctx = withOutputTokenLimit(ctx, prepared.Manifest.OutputReserveTokens)
	ctx = withRequestManifest(ctx, prepared.Manifest)
	systemPrompt, prompt := textPromptParts(prepared.Messages)
	if c.simulated {
		text := fallbackCompletion(systemPrompt, prompt)
		reservation, err := beginBudgetedModelCall(ctx, "local_fallback", estimateTokens(messagesToText(prepared.Messages)))
		if err != nil {
			return TextCompletion{}, err
		}
		payload, err := json.Marshal(map[string]any{
			"model": "local_fallback", "messages": prepared.Messages, "simulated": true,
		})
		if err != nil {
			return TextCompletion{}, err
		}
		ref, err := c.recordModelRequest(ctx, reservation.OperationID, "simulated.completion", "local_fallback", payload)
		if err != nil {
			return TextCompletion{}, err
		}
		started := time.Now()
		usage := estimateUsage(systemPrompt+"\n"+prompt, text)
		c.finishModelAttempt(ctx, ref, started, time.Time{}, usage, "simulated", ctx.Err())
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
		"model":    c.model,
		"messages": prepared.Messages,
	})
	if err != nil {
		if modelErr, ok := AsModelError(err); ok && modelErr.Kind == ErrorIncompleteOutput && len(response.Choices) > 0 {
			return TextCompletion{Text: response.Choices[0].Message.Content, Model: c.model, Usage: response.Usage}, err
		}
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
