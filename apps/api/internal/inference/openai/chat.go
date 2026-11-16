package openai

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
)

func (c *Client) CompleteTextDetailed(ctx context.Context, systemPrompt string, prompt string) (TextCompletion, error) {
	prepared, err := c.PrepareText(ctx, systemPrompt, prompt)
	if err != nil {
		return TextCompletion{}, err
	}
	return c.CompletePreparedText(ctx, prepared)
}

func (c *Client) PrepareText(ctx context.Context, systemPrompt string, prompt string) (PreparedText, error) {
	if !c.HasAPIKey() {
		return PreparedText{}, missingCredentialError("chat.prepare")
	}
	prompt = strings.TrimSpace(prompt)
	prepared, err := c.prepareModelContextForModel(ctx, c.model, []Message{
		{Role: "system", Content: strings.TrimSpace(systemPrompt), Source: contextassembly.SourceSystem, ReferenceID: "system_prompt"},
		{Role: "user", Content: prompt, Source: contextassembly.SourceCurrentInput, ReferenceID: "current_input"},
	}, nil)
	if err != nil {
		return PreparedText{}, err
	}
	return PreparedText{Messages: prepared.messages, Manifest: prepared.manifest}, nil
}

func (c *Client) CompletePreparedText(ctx context.Context, prepared PreparedText) (TextCompletion, error) {
	if !c.HasAPIKey() {
		return TextCompletion{}, missingCredentialError("chat.completion")
	}
	ctx = budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	ctx = withOutputTokenLimit(ctx, prepared.Manifest.OutputReserveTokens)
	ctx = withRequestManifest(ctx, prepared.Manifest)
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
