package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	tracepkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/tools"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

const (
	ExecutorNative      = "native"
	ExecutorLangChainGo = "langchaingo"

	frameworkLangChainGo = "langchaingo"
)

type ExecutorKind string

type ExecutorInput struct {
	Agent             domain.Agent
	History           []domain.Message
	Latest            string
	Catalog           *tools.Catalog
	RunID             string
	StepID            string
	RetrievedMemories []domain.RetrievedMemory
	RetrievedChunks   []domain.RetrievedDocumentChunk
}

type AgentExecutor interface {
	Kind() string
	Framework() string
	Stream(ctx context.Context, input ExecutorInput) (<-chan openai.StreamEvent, <-chan error)
}

type NativeExecutor struct {
	openAI *openai.Client
	trace  *tracepkg.Recorder
}

func (e NativeExecutor) Kind() string {
	return ExecutorNative
}

func (e NativeExecutor) Framework() string {
	return "agentflow-native"
}

func (e NativeExecutor) Stream(ctx context.Context, input ExecutorInput) (<-chan openai.StreamEvent, <-chan error) {
	return e.openAI.StreamAgentChatWithToolsTrace(
		ctx,
		input.Agent.SystemPrompt,
		input.History,
		input.Latest,
		input.Catalog,
		e.trace,
		input.RunID,
		input.StepID,
		input.RetrievedMemories,
		input.RetrievedChunks,
	)
}

type LangChainGoExecutor struct {
	openAI *openai.Client
	trace  *tracepkg.Recorder
}

func (e LangChainGoExecutor) Kind() string {
	return ExecutorLangChainGo
}

func (e LangChainGoExecutor) Framework() string {
	return frameworkLangChainGo
}

func (e LangChainGoExecutor) Stream(ctx context.Context, input ExecutorInput) (<-chan openai.StreamEvent, <-chan error) {
	events := make(chan openai.StreamEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		template := prompts.NewPromptTemplate(langChainGoPromptTemplate, []string{"system", "history", "memories", "knowledge", "input"})
		chain := chains.NewLLMChain(langChainGoModel{client: e.openAI}, template)
		values := map[string]any{
			"system":    strings.TrimSpace(input.Agent.SystemPrompt),
			"history":   formatExecutorHistory(input.History),
			"memories":  formatRuntimeRetrievedMemories(input.RetrievedMemories),
			"knowledge": formatRuntimeRetrievedChunks(input.RetrievedChunks),
			"input":     strings.TrimSpace(input.Latest),
		}

		promptText, err := template.Format(values)
		if err != nil {
			errs <- err
			return
		}
		startPayload := map[string]any{
			"executor":       e.Kind(),
			"framework":      e.Framework(),
			"framework_path": "chains.LLMChain",
			"input":          truncateRuntimeText(input.Latest, 1200),
			"input_chars":    len(input.Latest),
			"prompt":         truncateRuntimeText(promptText, 4000),
		}
		if len(input.RetrievedMemories) > 0 {
			startPayload["retrieved_memories"] = retrievalTracePayload(input.RetrievedMemories, nil)["retrieved_memories"]
		}
		if len(input.RetrievedChunks) > 0 {
			startPayload["retrieved_chunks"] = retrievalTracePayload(nil, input.RetrievedChunks)["retrieved_chunks"]
		}
		span := e.trace.LLMStart(ctx, input.RunID, input.StepID, startPayload)

		startedAt := time.Now()
		result, err := chain.Call(ctx, values)
		if err != nil {
			e.trace.Error(ctx, input.RunID, input.StepID, map[string]any{
				"source":    "langchaingo_executor",
				"executor":  e.Kind(),
				"framework": e.Framework(),
				"error":     err.Error(),
			})
			errs <- err
			return
		}

		output, ok := result["text"].(string)
		if !ok {
			err := errors.New("langchaingo executor returned non-string output")
			errs <- err
			return
		}
		output = strings.TrimSpace(output)
		if output == "" {
			output = "No response generated."
		}
		e.streamText(ctx, output, events)
		e.trace.LLMEnd(ctx, span, map[string]any{
			"executor":       e.Kind(),
			"framework":      e.Framework(),
			"framework_path": "chains.LLMChain",
			"output":         truncateRuntimeText(output, 4000),
			"output_chars":   len(output),
			"duration_ms":    time.Since(startedAt).Milliseconds(),
		})
	}()

	return events, errs
}

func (e LangChainGoExecutor) streamText(ctx context.Context, output string, events chan<- openai.StreamEvent) {
	for _, token := range strings.Fields(output) {
		select {
		case <-ctx.Done():
			return
		case events <- openai.StreamEvent{Type: "delta", Delta: token + " "}:
		}
	}
}

type langChainGoModel struct {
	client *openai.Client
}

func (m langChainGoModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	prompt := flattenLangChainGoMessages(messages)
	completion, err := m.client.CompleteTextDetailed(ctx, "", prompt)
	if err != nil {
		return nil, err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: completion.Text}},
	}, nil
}

func (m langChainGoModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, m, prompt, options...)
}

func flattenLangChainGoMessages(messages []llms.MessageContent) string {
	var builder strings.Builder
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				builder.WriteString(text.Text)
				builder.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func formatExecutorHistory(history []domain.Message) string {
	if len(history) == 0 {
		return "No prior messages."
	}
	var builder strings.Builder
	for _, message := range history {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(truncateRuntimeText(content, 1000))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

const langChainGoPromptTemplate = `{{.system}}

You are executing this single agent step through LangChainGo.
Use the retrieved memories and knowledge only when they are relevant.

Conversation history:
{{.history}}

{{.memories}}

{{.knowledge}}

User query:
{{.input}}`

func NormalizeExecutorKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ExecutorNative:
		return ExecutorNative
	case ExecutorLangChainGo, "langchain_go", "langchain-go":
		return ExecutorLangChainGo
	default:
		return ExecutorNative
	}
}
