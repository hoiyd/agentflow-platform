# Tokenization Calibration

`INF-008` measures the gap between AgentFlow's Context Assembly estimate and
the actual prompt tokens charged by a fixed llama.cpp model/template. It is an
offline evaluation command, not a production tokenization RPC or a replacement
for the Context Manifest or Usage Ledger.

## Run

Start the pinned server described in [llama.cpp compatibility evidence](local-inference-compatibility.md),
then run:

```bash
export LLAMA_CPP_MODEL=Qwen/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
export LLAMA_CPP_MODEL_ARTIFACT=Qwen/Qwen2.5-0.5B-Instruct-GGUF@9217f5db79a29953eb74d5343926648285ec7e67/qwen2.5-0.5b-instruct-q4_k_m.gguf
export LLAMA_CPP_CONTEXT_WINDOW_TOKENS=4096
export LLAMA_CPP_OUTPUT_RESERVE_TOKENS=256
export LLAMA_CPP_SAFETY_MARGIN_TOKENS=256
make tokenization-calibration
```

The script hashes the locally loaded GGUF using the server's `/props` model
path. For a remote server, supply `LLAMA_CPP_GGUF_SHA256` explicitly instead.
Neither the path nor request content is stored in the report. The output is
`.cache/tokenization-calibration/report.json`; `--enforce` makes a failed gate
exit non-zero. Each sample has a two-minute default deadline, configurable via
`LLAMA_CPP_CALIBRATION_TIMEOUT`.

The report identity includes the backend build, pinned model artifact, GGUF
SHA-256 (which pins its tokenizer), `/props` chat-template SHA-256, BOS/EOS
markers, Context limits, and corpus version. A changed tokenizer or template
changes the identity; do not compare the old and new errors as one experiment.

## What Is Counted

The fixed corpus contains English, Chinese, Go code, Conversation history,
retrieved RAG Context, a long JSON Tool schema, an admitted near-boundary input,
and a preflight-rejected input. Each admitted sample follows this path:

1. The production Context Assembler selects messages and records its estimated
   input tokens by source. The OpenAI adapter serializes the actual request;
   the evaluation recorder captures those bytes before network transport.
2. The **same request bytes** go to llama.cpp's
   `/v1/chat/completions/input_tokens`, `/apply-template`, and
   `/v1/chat/completions`. `/tokenize` counts the resulting template prompt
   with and without special-token insertion. Provider `usage.prompt_tokens`
   must equal both native counts; missing usage is marked as estimated-only
   evidence and fails the calibration gate.
3. The report records absolute error `|estimated - backend|` and relative
   error `absolute / backend`. It also records content-only tokens, BOS/EOS
   marker presence, template token count, and the Tool-template overhead
   measured by removing Tools from otherwise identical request messages.

The preflight-rejected sample is **not** sent for completion. The evaluator
reconstructs its required messages under a relaxed evaluation-only window and
asks the native count endpoint what that prompt would cost. This produces
`counterfactual_backend_input_tokens` and `would_fit_if_admitted`, without
bypassing production preflight. A false rejection is diagnostic, not a safety
gate failure. An admitted sample fails the gate if backend input plus the
reserved output exceeds the configured Context window. The safety margin stays
in the preflight input budget:

```text
input budget = context window - output reserve - safety margin
actual fit  = backend input + output reserve <= context window
```

The report contains hashes, counts, and failure categories, not raw prompts,
Tool arguments, credentials, or the local GGUF path. The corpus is fixed in
`apps/api/internal/evaluation/tokenization/corpus.go`; change its version when
changing its inputs.

## Reference Observation

On llama.cpp build `10050` / `b15ca938a` with Qwen2.5 0.5B Instruct `Q4_K_M`,
the eight-case gate passed: seven admitted samples had matching native/template
counts and exact provider usage, while one over-budget sample was rejected
before completion. The largest absolute error was **1,513 tokens** (115.0%) on
synthetic repeated text: the estimator was conservative. The largest
underestimate was **53 tokens** on the Tool-schema case, below this profile's
256-token safety margin. The rejected sample estimated **8,207** input tokens;
counterfactual backend counting returned **3,706**, which would fit with the
256-token output reserve. This is one measured conservative false rejection,
not evidence that the global estimator should be changed from one synthetic
profile.

For this template, the prompt contained EOS markers, no literal BOS marker,
and `add_special` changed the token count by zero. Those are observations for
this exact backend/model/template combination, not universal tokenizer rules.
The report does not measure answer quality or justify changing production
Budget settlement: exact provider usage remains authoritative there.

Native endpoint behavior is documented in the
[llama.cpp server reference](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md).
