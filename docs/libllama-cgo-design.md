# libllama CGO Design and Prototype

## Goal

This prototype proves that AgentFlow can run GGUF inference inside the Go process through the native llama.cpp C API. It stays separate from the production HTTP provider so the default server remains portable and easy to deploy.

It demonstrates:

- loading a GGUF model once and retaining it across requests
- creating request-scoped context and sampler state
- chunked prompt prefill and token-by-token decode
- streaming generated token pieces into Go
- context timeout and signal cancellation during `llama_decode`
- explicit ownership and cleanup across Go, C, and C++
- compile-time isolation with `cgo && llamacpp` build tags

## Boundary

```txt
cmd/libllama-prototype
        |
        v
internal/llamacpp Go API
        |
        v
wrapper.h stable C ABI
        |
        v
wrapper_llamacpp.cpp
        |
        v
llama.cpp C API / GGML backends / GGUF model
```

The Go layer never includes `llama.h` directly. Only the C++ adapter knows llama.cpp types such as `llama_model`, `llama_context`, `llama_sampler`, and `llama_batch`. This contains upstream API churn in one file.

## Runtime Ownership

| Resource | Lifetime | Owner |
| --- | --- | --- |
| GGML backend registry | process | llama.cpp |
| `llama_model` | engine | native wrapper |
| `llama_context` | one generation | native wrapper |
| `llama_sampler` | one generation | native wrapper |
| prompt token buffer | one generation | native wrapper |
| token piece buffer | one `Next` call | Go caller |

`Engine.Generate` holds a Go mutex. One engine therefore runs one generation at a time, while multiple engine instances can run concurrently. A production implementation should normally keep one loaded model and use the existing AgentFlow model-request semaphore rather than load one model per request.

## Streaming and Cancellation

The native API uses a pull model:

1. Go calls `af_llama_begin` with a prompt and output limit.
2. Go repeatedly calls `af_llama_next`.
3. Native code prefills the prompt in batches, decodes one token, and writes one token piece into a caller-owned buffer.
4. Go immediately invokes its token handler, which can feed the existing SSE stream.

This avoids passing Go pointers or Go callbacks into C. For cancellation, a small Go goroutine calls `af_llama_cancel`; the wrapper stores an atomic flag and exposes it through llama.cpp's decode abort callback. Cancellation is checked both during CPU decode and between generated tokens.

## Build llama.cpp as a Library

The wrapper uses llama.cpp's installed `llama.pc` file. Build and install a recent llama.cpp checkout into a local prefix:

```bash
git clone --depth 1 --branch b10050 \
  https://github.com/ggml-org/llama.cpp.git /path/to/llama.cpp
cmake -S /path/to/llama.cpp -B /path/to/llama.cpp/build \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX=/path/to/llama.cpp/install \
  -DGGML_METAL=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DLLAMA_BUILD_APP=OFF \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF \
  -DLLAMA_BUILD_TOOLS=OFF \
  -DLLAMA_BUILD_SERVER=OFF
cmake --build /path/to/llama.cpp/build --config Release -j
cmake --install /path/to/llama.cpp/build
```

The prototype was compiled and run against llama.cpp `b10050` (`b15ca938ad00aa6b3ee6c2edda7363fd02826b18`), following the C API used by its official `examples/simple` program. Update this pin deliberately because the API evolves quickly.

Go, `libllama`, and GGML must have the same CPU architecture. On Apple Silicon, verify the result before debugging undefined symbols:

```bash
go env GOARCH
file /path/to/llama.cpp/install/lib/libllama.dylib
```

An `arm64` Go binary cannot link an `x86_64` library from an Intel Homebrew installation under `/usr/local`. Use an arm64 Homebrew installation or build from source with `-DCMAKE_OSX_ARCHITECTURES=arm64`.

The command above creates a predictable CPU-only build for headless verification. Remove `-DGGML_METAL=OFF` when Metal acceleration is wanted. Setting `-gpu-layers 0` controls weight offload but does not remove a compiled Metal backend; some headless environments cannot create its command queue.

## Run the Prototype

From `apps/api`:

```bash
source ~/.gvm/scripts/gvm
gvm use go1.25.5

export PKG_CONFIG_PATH=/path/to/llama.cpp/install/lib/pkgconfig
export DYLD_LIBRARY_PATH=/path/to/llama.cpp/install/lib

CGO_ENABLED=1 go run -tags llamacpp ./cmd/libllama-prototype \
  -model /path/to/model.gguf \
  -prompt $'User: Explain Go channels in one sentence.\nAssistant:' \
  -max-tokens 48 \
  -context-size 2048 \
  -gpu-layers 0
```

Linux uses `LD_LIBRARY_PATH` instead of `DYLD_LIBRARY_PATH`. Set `-gpu-layers 0` for CPU-only inference.

The command writes token pieces to stdout as they are generated and prints model identity, generated token count, elapsed time, and tokens per second to stderr. `Ctrl-C` and `-timeout` both cancel generation.

Without the native build tag, the command is still buildable and reports the required build mode:

```bash
go run ./cmd/libllama-prototype
```

## Verification

Default backend regression suite:

```bash
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./...
```

Native compile check after installing llama.cpp:

```bash
PKG_CONFIG_PATH=/path/to/llama.cpp/install/lib/pkgconfig \
DYLD_LIBRARY_PATH=/path/to/llama.cpp/install/lib \
CGO_ENABLED=1 go test -tags llamacpp ./internal/llamacpp ./cmd/libllama-prototype
```

Native inference smoke test:

```bash
PKG_CONFIG_PATH=/path/to/llama.cpp/install/lib/pkgconfig \
DYLD_LIBRARY_PATH=/path/to/llama.cpp/install/lib \
CGO_ENABLED=1 go run -tags llamacpp ./cmd/libllama-prototype \
  -model /path/to/model.gguf \
  -prompt "Reply with exactly: libllama connected" \
  -temperature 0 \
  -max-tokens 24
```

Verify that output arrives incrementally, the summary contains a positive token count, `Ctrl-C` stops a long generation, and a prompt plus output budget larger than `-context-size` fails before decode.

Set `LLAMA_CPP_TEST_MODEL` to include the native integration test, which covers streaming, context overflow, and cancellation:

```bash
PKG_CONFIG_PATH=/path/to/llama.cpp/install/lib/pkgconfig \
DYLD_LIBRARY_PATH=/path/to/llama.cpp/install/lib \
LLAMA_CPP_TEST_MODEL=/path/to/model.gguf \
CGO_ENABLED=1 go test -tags llamacpp ./internal/llamacpp -run TestNativeEngine -v
```

## How It Fits AgentFlow

The prototype is an inference adapter, not another agent runtime. AgentFlow should continue to own RAG retrieval, context budgeting, tool execution, run state, tracing, timeout policy, and concurrency control.

The production integration point would be the model-client boundary:

```txt
Agent runtime -> model client interface -> OpenAI-compatible HTTP client
                                 `-----> libllama native client
```

Both clients should emit the same normalized stream events. Tool calling still requires AgentFlow to render a tool-capable chat template and parse model output into its existing tool-call contract. The prototype deliberately uses a raw prompt so model loading, decode, streaming, and cancellation remain easy to inspect in an interview.

## Prototype Limits

- text generation only; no embeddings, grammar-constrained JSON, or multimodal input
- decoder-only GGUF models only; embedding-only and encoder-decoder models are rejected during load
- raw prompts only; no `llama_chat_apply_template` integration yet
- one active generation per engine
- model loading is synchronous and is not cancellable
- no KV-cache reuse across requests
- no direct wiring into the production server executable
- no compatibility promise across arbitrary llama.cpp revisions

These limits are deliberate. The prototype proves the native integration and isolates the risky boundary without turning a short interview project into a second model server.
