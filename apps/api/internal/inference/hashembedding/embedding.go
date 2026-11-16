// Package hashembedding provides the deterministic offline evaluation baseline.
// Its vectors are not evidence of semantic retrieval quality.
package hashembedding

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strings"

	"agentflow-platform/apps/api/internal/inference/provider"
)

type Embedder struct{}

func (Embedder) EmbedText(_ context.Context, input string) (provider.Embedding, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return provider.Embedding{}, errors.New("embedding input is required")
	}
	vector := make([]float64, 1536)
	words := strings.Fields(strings.ToLower(input))
	if len(words) == 0 {
		words = []string{input}
	}
	for _, word := range words {
		sum := sha256.Sum256([]byte(word))
		for i := 0; i < len(sum); i += 2 {
			index := (int(sum[i])<<8 + int(sum[i+1])) % len(vector)
			sign := 1.0
			if sum[(i+1)%len(sum)]%2 == 0 {
				sign = -1
			}
			vector[index] += sign
		}
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm != 0 {
		norm = math.Sqrt(norm)
		for i := range vector {
			vector[i] /= norm
		}
	}
	return provider.Embedding{Vector: vector, Model: "local_hash_embedding", Provider: "simulated", Estimated: true, Dimensions: len(vector)}, nil
}
