package provider

import (
	"context"
	"encoding/json"
)

const OutputFormatOpenAIJSONSchema = "openai_json_schema"

// StructuredOutput is scoped to final-answer generation, never router decisions,
// summaries or Tool arguments. Schema comes from the frozen verifier definition.
type StructuredOutput struct {
	Mode         string
	Version      string
	ContractHash string
	Schema       map[string]any
}

func (o StructuredOutput) Guidance() string {
	encoded, _ := json.Marshal(o.Schema)
	return "OUTPUT_CONTRACT " + o.Version + " " + o.ContractHash + ". Final answer must be one JSON object matching this schema; no Markdown fences or commentary. Tool calls remain intermediate actions, not final objects. Schema: " + string(encoded)
}

type structuredOutputKey struct{}

func WithStructuredOutput(ctx context.Context, output StructuredOutput) context.Context {
	return context.WithValue(ctx, structuredOutputKey{}, output)
}

func StructuredOutputFromContext(ctx context.Context) (StructuredOutput, bool) {
	output, ok := ctx.Value(structuredOutputKey{}).(StructuredOutput)
	return output, ok
}
