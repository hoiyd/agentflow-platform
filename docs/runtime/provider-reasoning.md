# Provider Reasoning Display

## Contract and Boundaries

Reasoning display is an optional view of provider-supplied output, not a
reconstruction of private internal thoughts, a final answer, or verification
evidence. Unsupported routes keep the existing `Working...` experience.

The initial format is `deepseek_reasoning_content`: OpenAI Chat Completions SSE
`choices[0].delta.reasoning_content`, documented by
[DeepSeek](https://api-docs.deepseek.com/guides/thinking_mode/).
No Responses API reasoning events, Anthropic thinking blocks, opaque encrypted
state, or arbitrary provider fields are interpreted as display text.

Enable the format explicitly in a route's `capabilities.reasoning_display_format`.
Omission disables display. This is a frozen route capability, not a request to
enable provider thinking, and does not change sampling or generation parameters.

Use `MODEL_ROUTE_CONFIG_PATH` as described in the [route catalog](model-routing.md).
In the chosen route's existing capabilities object, add:

```json
{
  "tool_calling": true,
  "structured_output": true,
  "streaming": true,
  "reasoning_display_format": "deepseek_reasoning_content"
}
```

Restart the backend after changing route configuration. New Runs freeze the new
capability; old Runs keep the frozen display setting. There is no endpoint-name
heuristic that automatically opts an unverified OpenAI-compatible provider in.

The browser receives a content-free receiving state immediately. Text is released
only after a successfully finished model call, after redaction of the complete
text. This intentionally avoids per-chunk credential leakage: a credential can
span any number of provider chunks. The display copy is capped at 16 KiB after
redaction, with an explicit truncation marker. Interrupted/invalid calls expose
no partial reasoning text. Existing protocol and Run budgets remain authoritative.
The shared stream accumulator bounds answer, reasoning, and Tool fragments to
1 MiB per model call, regardless of display permission; fallback output-token
estimates include returned reasoning even when display is disabled. Exact
provider-reported usage remains authoritative. Display status `Received` means a
complete transport response, not successful Run budget settlement or verification.

`model.reasoning` is a typed, live-only Run event, scoped to Run, Turn, optional
Stage, and model-call identity. Chat uses a collapsed plain-text disclosure;
reasoning never appends to the assistant answer. Non-streaming internal planning
and routing calls do not produce these display events.
The browser retains at most the latest 32 calls (512 KiB maximum text), clearing
them on new submission or Conversation navigation; this is not another Run store.
The existing header Stop action is available for active Runs in all three modes,
so waiting for provider reasoning can be explicitly canceled without closing Chat.

Display content is not stored in messages, ordinary events, Task State, Memory,
or metadata-only Request Capture. Reload/reconnect cannot recover earlier display
text. Existing opt-in, redacted Request Capture may retain continuation fields in
later outgoing requests under its own retention policy; opening the UI does not
enable Capture. Redaction catches known credential patterns, not all sensitive
personal or business information: operators must explicitly trust the route.

Raw `reasoning_content` remains separate and unchanged for same-Turn Tool
continuation, including absent versus explicitly empty fields. This feature does
not implement cross-Turn or crash/Resume recovery of private continuation state.
DeepSeek's current documentation requires previous-turn reasoning for requests
carrying tools; same-Turn fixture evidence does **not** establish that broader
compatibility.

## Failure Inventory

| Failure | Expected behavior |
| --- | --- |
| Missing, empty, or unsupported reasoning | No empty disclosure; answer path unchanged |
| Interleaved answer/reasoning and multiple Tool rounds | Separate model-call entries; answer reset never resets reasoning |
| Split credentials or multiline private keys | Redact the complete display copy; serialized continuation unchanged |
| Display cap exceeded | Truncated sanitized text; required continuation retained |
| Missing finish/DONE, malformed SSE, disconnect, refusal, length stop | Interrupted display; real call/Run failure preserved; no partial text |
| Cancel or budget exhaustion | No reasoning-only success; no retry after a visible receiving event |
| Browser navigation, new Run, reload | Clear browser-only content; durable answer/recovery unchanged |
| Unknown format or format on a non-streaming route | Reject route configuration, never guess a decoder |
| Attempts to persist a reasoning event | Reject through the executable event catalog |

## Adding Another Format

Keep provider wire decoding in its adapter. Map only a documented, displayable
field into the shared typed payload; preserve separate private continuation state.
Add the supported format to route validation and frontend contract validation,
then exercise actual serialized follow-ups and privacy failures with a strict
provider fixture. The Turn engine, SSE transport, and Chat disclosure need no
provider-specific execution branches.

## Reproducible Checks

The strict local provider exercises actual serialized continuation, interleaved
answer/Tool/reasoning frames, redaction, failure, truncation, disconnect, and the
Single/Multi/Autonomous publication boundaries through real Go composition and
disposable Postgres. It is compatibility evidence, not a live-model measurement.

```bash
AGENTFLOW_REASONING_TEST=1 \
TEST_DATABASE_URL='postgres://<user>@127.0.0.1:5432/<dedicated-test-db>?sslmode=disable' \
bash scripts/test-browser.sh reasoning.spec.ts
```

Playwright retains per-case `reasoning-runtime-evidence` JSON attachments with
inputs, frozen routes, durable event types, request metadata, and fixture contract
outcomes under `apps/web/test-results/`; no screenshots or mobile tests are needed.
