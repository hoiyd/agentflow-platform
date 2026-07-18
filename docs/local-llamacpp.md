# Local llama.cpp Runtime

AgentFlow already talks to models through an OpenAI-compatible HTTP boundary. A local `llama-server` can therefore replace OpenAI/OpenRouter without changing the agent runtime, tool execution, RAG, context assembly, tracing, timeout, or concurrency code.

## Start llama.cpp

Build or install `llama-server`, then start it with a GGUF chat model:

```bash
llama-server \
  -m /path/to/model.gguf \
  -c 8192 \
  --host 127.0.0.1 \
  --port 8081
```

For model-driven tool calls, start the server with Jinja tool-call support:

```bash
llama-server \
  -m /path/to/tool-capable-model.gguf \
  -c 8192 \
  --host 127.0.0.1 \
  --port 8081 \
  --jinja
```

Use a model/template combination that supports function calling well, such as a recent Qwen, Llama 3.1/3.3, Hermes, or Functionary GGUF. llama.cpp also has a generic fallback handler, but native tool-call templates are more reliable.

## Configure AgentFlow

In `apps/api/.env`, use the local OpenAI-compatible endpoint:

```bash
OPENAI_API_KEY=local-no-key
OPENAI_BASE_URL=http://127.0.0.1:8081/v1
OPENAI_MODEL=local-gguf
OPENAI_REQUEST_TIMEOUT=10m

MODEL_CONTEXT_WINDOW_TOKENS=8192
MODEL_OUTPUT_RESERVE_TOKENS=1024
CONTEXT_SAFETY_MARGIN_TOKENS=512
CONTEXT_HISTORY_MAX_TOKENS=4096
CONTEXT_MEMORY_MAX_TOKENS=1024
CONTEXT_KNOWLEDGE_MAX_TOKENS=1536

MAX_CONCURRENT_MODEL_REQUESTS=1
MODEL_REQUESTS_PER_MINUTE=0
MODEL_TOKENS_PER_MINUTE=0
```

`OPENAI_API_KEY` must be non-empty because AgentFlow treats an empty key as the deterministic local fallback mode. If `llama-server` was started without an API key, any non-empty placeholder is enough. If it was started with authentication enabled, use the configured key.

Keep embeddings separate. The default RAG embedding endpoint is local Ollama:

```bash
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
EMBEDDING_DIMENSIONS=1536
```

If you run an embedding-capable llama.cpp server instead, set `EMBEDDING_BASE_URL` to its `/v1` base URL and choose the matching embedding model/dimensions.

## Why this works

- Streaming: `apps/api/internal/openai/client.go` reads Server-Sent Events from `/v1/chat/completions` with `stream: true`.
- Tool calling: AgentFlow sends OpenAI-style `tools` and `tool_choice: auto`; llama.cpp can return OpenAI-style `tool_calls` when started with a tool-capable chat template.
- RAG: retrieval runs before the model call and injects selected memory/document chunks into assembled messages.
- Context budget: `apps/api/internal/contextassembly` selects history, memories, knowledge, and tool results under the configured token budget.
- Timeout/cancel: model HTTP requests use request contexts and `OPENAI_REQUEST_TIMEOUT`.
- Concurrency: `MAX_CONCURRENT_MODEL_REQUESTS`, request buckets, and run limits are enforced before model requests are sent.

## Smoke Test

After starting `llama-server` and AgentFlow:

```bash
curl -N http://localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"Reply with exactly: llama.cpp connected","mode":"single"}'
```

You should see SSE chunks from AgentFlow. If the response is the deterministic fallback text, check that `OPENAI_API_KEY` is non-empty.

## CGO Paths

The arithmetic smoke example proves that the local Go toolchain can compile CGO:

```bash
cd apps/api
CGO_ENABLED=1 go run ./cmd/cgo-smoke
```

Expected output:

```txt
cgo add_i32(21, 21) = 42
cgo context_budget_i32(4096, 512, 128) = 3456
```

This demonstrates Go calling C functions without adding CGO to the production server path.

The real [`libllama` wrapper prototype](libllama-cgo-design.md) goes further: it loads a GGUF model through llama.cpp's C API, performs batched prompt prefill and token-by-token decode, streams token pieces into Go, and propagates timeout or signal cancellation into native inference. It remains behind the `llamacpp` build tag so normal AgentFlow builds do not require a C++ toolchain or native model library.
