# Curated Tool Task Evaluations

TOOL-013 adds task-level evidence to the existing Tool contract/fault harness.
It reuses `artifact_search` and `artifact_read`: there is no missing Binding to
justify another Tool, MCP adapter, runtime service, or frontend page.

## Task Slice

The versioned [dataset](../../apps/api/internal/tooleval/testdata/artifact_tasks.json)
models two practical tasks against a long settlement export:

1. Verify the settled amount of one exact record.
2. Extract several amounts and source lines into a structured report.

A third case checks a missing record. Values are synthetic, not customer data.
Deterministic padding places facts beyond the initial 256-byte preview. Every
sample uses an isolated in-memory fixture, Conversation, Run, and immutable
artifact. No state is written to disk; normal application storage is not opened.
Artifact access retains the production Run scope and read-only security policy.

## Verification and Comparison

Both arms receive identical task instructions, an artifact reference, and the
same preview. `without_tools` has an empty Catalog; `with_tools` has only the two
Artifact Bindings. This is a **preview-only ablation**, not a comparison with
full-document prompting, RAG, or every alternative retrieval design.

The answer must be strict JSON with `facts` and `missing`. A fact must match its
expected ID, amount and exact complete source line. Tool-arm success additionally
requires that quote in an actual successful read/search result. Missing-record
success requires an exact-ID search with no matches and a complete scan. Duplicate,
unexpected, omitted or fabricated facts fail. No LLM judge or exact call order
is required. `completed` and `verified` are separate fields.

The runner calls the production OpenAI-compatible client, Context Assembler,
Tool Executor, recorder and Usage Ledger. It does not run full Chat/Multi/Loop
orchestration, seed Memory, or bypass Tool validation. The current client supports
one Tool-selection batch followed by the answer; these tasks fit that protocol.
This is not an evaluation of multi-round search/read planning.

## Offline Checks

From `apps/api`, using the repository Go version:

```bash
go test ./internal/tooleval ./cmd/eval ./internal/toolartifact ./internal/tools
go test -race ./internal/tooleval ./cmd/eval ./internal/toolartifact
EVALUATION_REPORT_DIR=/tmp/evaluation-reports go test ./internal/tooleval -count=1
```

The local HTTP fixture selects Tools, reads the actual returned snippets and
produces a structured answer through the production client. It checks protocol
wiring, evidence sensors, budget exhaustion, timeout/cancellation, provider
errors, invalid arguments and plausible wrong answers. It is **not a real model**
and proves no model-quality improvement. Default CI never calls an external
model, and retains `tool-task-protocol.json` tagged `offline_protocol_fixture`.

## Explicit Live Evaluation

Export `OPENAI_API_KEY` in the shell; the command does not load application `.env`
files or silently fall back to canned responses. Select a Tool-capable model:

```bash
go run ./cmd/eval tool --live \
  --base-url https://api.openai.com/v1 --model YOUR_MODEL \
  --trials 3 --max-model-calls 30 --max-total-tokens 60000 \
  --timeout 60s --enforce > /tmp/tool-task-live.json
```

The model-call/token limits cover the **whole suite**, both arms and every trial,
not each sample. Each sample has at most four Tool executions and the specified
deadline (maximum five minutes); model output reserve is 512 tokens. Model retries
are disabled to avoid hidden attempts. Existing Run Budget reservation/settlement
logic enforces remaining capacity. Usage may be estimated when providers omit it;
token estimation and provider billing are not an exact dollar-spend guarantee.
An unsettled model reservation stops subsequent samples because billed usage is
unknown. No further request is made after exhaustion; unrun cases stay recorded.

Exit codes: `0` means the report was produced; `1` with `--enforce` means at least
one Tool-arm sample did not verify; `2` means invalid setup or an infrastructure/
report-write error. Without `--enforce`, inspect findings even when exit is zero.
Do not commit credentials, temporary stores, or reports containing private data.

## Report Contract

`task-eval-v1` is the Tool-specific payload inside the shared
`agentflow-evaluation-report-v1` envelope. JSON records dataset
ID/version/hash (including materialized source), git revision, model/provider,
Context Assembly settings, Tool definition hash, suite limits, task/trial/arm,
output, successful Tool evidence, failed Tool names/error codes, findings, usage
source and latency. Latency covers model/Tool execution, excluding fixture setup.

The stderr summary compares verified success, tokens, model calls, Tool calls
and latency. Success-rate denominators include failed and unrun samples. Means
use evaluated samples only; always inspect `evaluated`, `samples`, estimated usage
and open reservations before comparing costs. No price table means monetary cost
is unavailable, not free. Repeated trials alternate arm order. Provider endpoints
and API keys are omitted; result/error text uses the existing deterministic redactor.
The same `eval` command and provenance/gate envelope are used by the offline RAG
suite; domain metrics remain typed rather than forced into a lowest-common-denominator schema.

Live-model acceptance remains manual until a budgeted report is collected. The
three small cases do not establish statistical reliability or broad Tool quality.
Add real failure examples to the dataset/sensor tests before expanding scope.
