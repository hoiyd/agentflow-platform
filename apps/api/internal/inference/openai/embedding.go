package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (c *Client) EmbedText(ctx context.Context, input string) (Embedding, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Embedding{}, errors.New("embedding input is required")
	}
	if c.usesOllamaEmbedEndpoint() {
		return c.embedTextWithOllama(ctx, input)
	}
	if c.apiKey == "" {
		return Embedding{
			Vector:     deterministicEmbedding(input, c.embeddingDimensions),
			Model:      "local_hash_embedding",
			Provider:   "local",
			Estimated:  true,
			Dimensions: c.embeddingDimensions,
		}, nil
	}
	payload, err := c.embeddingRequestPayload(input)
	if err != nil {
		return Embedding{}, err
	}
	const operation = "embedding.openai_compatible"
	return executeWithRetry(ctx, c.retryPolicy, operation, func() (Embedding, error) {
		resp, err := c.doEmbeddingRequest(ctx, payload)
		if err != nil {
			return Embedding{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return Embedding{}, modelErrorFromHTTPResponse(operation, resp)
		}

		var decoded embeddingResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return Embedding{}, invalidResponseError(operation, "failed to decode embedding response", err)
		}
		if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
			return Embedding{}, invalidResponseError(operation, "embedding response returned no vector", nil)
		}
		return Embedding{
			Vector:     decoded.Data[0].Embedding,
			Model:      decoded.Model,
			Provider:   "openai_compatible",
			Dimensions: len(decoded.Data[0].Embedding),
		}, nil
	})
}

func (c *Client) embedTextWithOllama(ctx context.Context, input string) (Embedding, error) {
	payload, err := c.ollamaEmbeddingRequestPayload(input)
	if err != nil {
		return Embedding{}, err
	}
	return executeWithRetry(ctx, c.retryPolicy, "embedding.ollama", func() (Embedding, error) {
		return c.doOllamaEmbeddingRequest(ctx, input, payload)
	})
}

func (c *Client) doOllamaEmbeddingRequest(ctx context.Context, input string, payload []byte) (Embedding, error) {
	resp, err := c.doRawRequest(ctx, c.embeddingBaseURL, payload)
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embedding request failed: model=%s dimensions=%d input_chars=%d error=%w", c.embeddingModel, c.embeddingDimensions, len(input), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Embedding{}, modelErrorFromHTTPResponse("embedding.ollama", resp)
	}

	var decoded ollamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Embedding{}, invalidResponseError("embedding.ollama", "failed to decode Ollama embedding response", err)
	}
	if len(decoded.Embeddings) == 0 || len(decoded.Embeddings[0]) == 0 {
		return Embedding{}, invalidResponseError("embedding.ollama", "Ollama embedding response returned no vector", nil)
	}
	model := strings.TrimSpace(decoded.Model)
	if model == "" {
		model = c.embeddingModel
	}
	return Embedding{
		Vector:     decoded.Embeddings[0],
		Model:      model,
		Provider:   "ollama",
		Dimensions: len(decoded.Embeddings[0]),
	}, nil
}

func (c *Client) embeddingRequestPayload(input string) ([]byte, error) {
	request := map[string]any{
		"model": c.embeddingModel,
		"input": input,
	}
	if c.embeddingDimensions > 0 {
		request["dimensions"] = c.embeddingDimensions
	}
	return json.Marshal(request)
}

func (c *Client) ollamaEmbeddingRequestPayload(input string) ([]byte, error) {
	request := map[string]any{
		"model": c.embeddingModel,
		"input": input,
	}
	if c.embeddingDimensions > 0 {
		request["dimensions"] = c.embeddingDimensions
	}
	return json.Marshal(request)
}

func (c *Client) usesOllamaEmbedEndpoint() bool {
	return strings.HasSuffix(strings.TrimRight(c.embeddingBaseURL, "/"), "/api/embed")
}
