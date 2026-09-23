# llama.cpp Compatibility Evidence

`INF-001` validates the existing OpenAI-compatible model route against a real
`llama.cpp` server. It does not add another Provider, scheduler, limiter, or
inference gateway.

## Reference Profile

The initial reproducible profile is:

| Field | Value |
| --- | --- |
| llama.cpp | build `10050`, commit `b15ca938a` |
| Reference host | Apple M1 Pro; Darwin x86_64 `llama-server` binary |
| Model artifact | `Qwen/Qwen2.5-0.5B-Instruct-GGUF` commit `9217f5db79a29953eb74d5343926648285ec7e67`, file `qwen2.5-0.5b-instruct-q4_k_m.gguf` |
| Quantization | `Q4_K_M` |
| Context / output | `4096` / `256` Tokens |
| Route capabilities | streaming and structured output `true`; Tool Calling `false` |

Hardware remains an explicit run input because the same model and binary do not
imply comparable latency across machines. A different backend commit, model,
quantization, chat template, context limit, or hardware produces a different
compatibility identity and must generate a new report.

## Run The Evidence Suite

Start the server with the same limits represented by the route contract:

```bash
llama-server \
  -hf Qwen/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M \
  --host 127.0.0.1 --port 18081 \
  --ctx-size 4096 --parallel 1 --metrics --jinja
```

Then run the protocol checks:

```bash
export LLAMA_CPP_MODEL=Qwen/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
export LLAMA_CPP_MODEL_ARTIFACT=Qwen/Qwen2.5-0.5B-Instruct-GGUF@9217f5db79a29953eb74d5343926648285ec7e67/qwen2.5-0.5b-instruct-q4_k_m.gguf
export LLAMA_CPP_QUANTIZATION=Q4_K_M
export LLAMA_CPP_CONTEXT_WINDOW_TOKENS=4096
export LLAMA_CPP_MAX_OUTPUT_TOKENS=256
export LLAMA_CPP_HARDWARE='Apple M1 Pro; Darwin x86_64 llama-server'
./scripts/llama-compatibility-evidence.sh
```

The script writes `.cache/llama-cpp-compatibility/report.json`. With AgentFlow
inputs it enforces the full compatibility gate; protocol-only runs are recorded
but cannot pass that gate without Run/Replay evidence. It verifies:

- route validation and explicit rejection of undeclared capabilities;
- non-stream completion with exact provider usage;
- SSE deltas, `[DONE]`, and stream usage;
- typed Context length rejection and bounded timeout;
- cancellation while waiting for an AgentFlow model permit, before the first
  Token, and during generation;
- a successful model probe after every cancellation, proving that client and
  backend request capacity remains usable;
- explicit AgentFlow cancellation after `model.started`, with durable
  `model.failed`, `stage.failed`, `run.cancel_requested`, and `run.canceled` in
  Replay, with no open Stage;
- forced Tool Calling only when the route explicitly declares support.
- JSON-schema output when the route explicitly declares support.

`LLAMA_CPP_REQUEST_TIMEOUT` bounds each evidence check and mode (default `3m`);
it does not change the route's per-request `request_timeout_seconds`.

The cancellation claim is limited to transport cancellation, the Run/Event
terminal state, and a successful capacity probe. This does not prove that the
backend reclaimed a specific KV Cache allocation. That stronger claim requires corroborating
`llama.cpp` logs or metrics for the same request.

## Single, Multi, And Loop Evidence

Use [the llama.cpp route example](../../apps/api/config/model-routes.llama-cpp.example.json)
as `MODEL_ROUTE_CONFIG_PATH`, set `LLAMA_CPP_API_KEY=local`, and start AgentFlow.
The example allows 120 seconds per model request because CPU decoding of a
256-token response can exceed 30 seconds on the reference machine.
The selected Agent must only require capabilities declared by the route. Use
an Agent with no user-configured Tools for this profile. The Runtime leaves
its harness Tools out of a text-only route and streams the answer directly.
The simple `get_current_time` call succeeded, but this llama.cpp build rejected
the full Task State Tool schema with a grammar parse error, so Tool Calling is
declared unsupported for this fixed profile. A strict JSON-schema response
passed and is separately checked before declaring structured output.

Add the live Runtime inputs before running the same script:

```bash
export AGENTFLOW_BASE_URL=http://127.0.0.1:8080
export AGENTFLOW_WORKSPACE_ID=llama-compatibility
export AGENTFLOW_AGENT_ID=<agent-id>
./scripts/llama-compatibility-evidence.sh
```

The report then runs Single, Multi, and Loop (`autonomous`) through the same
route. Each supported mode must finish `completed`; Replay must retain the
expected model route, a successful `model.route_decided` event, non-zero Usage
Ledger tokens, and the terminal Run status. Multi includes its explicit
continue step with a no-tool routing constraint, avoiding unrelated built-in
Agents whose user-configured Tools exceed this route's capabilities.

This is protocol and lifecycle evidence, not an answer-quality score. The
0.5B model may return unusable Loop decision JSON; the existing bounded
decision fallback can still finish that Run. Evaluate task quality separately.

If a chosen model and chat template genuinely support Tool Calling, update the
route contract and set `LLAMA_CPP_TOOL_CALLING=true`. Do not enable the flag to
make the test pass: an unsupported capability remains `false`, and the report
records that check as explicitly skipped rather than silently falling back.
