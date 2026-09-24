# Model Route Contract and Catalog

AgentFlow places deterministic Model routing between the shared Turn Engine and
the provider-neutral model client:

```text
Turn request
  -> route requirements
  -> frozen Model Route Catalog
  -> capability and capacity filter
  -> stable priority selection
  -> provider client
```

The production composition loads a complete set of peer OpenAI-compatible Chat
LLM routes from one secret-free JSON file. There is no primary or secondary
route. Adding a target changes data, not Go configuration fields. A route file
is required at startup; there is no environment-defined default Chat model.

Set `MODEL_ROUTE_CONFIG_PATH` to the route file. Its schema is demonstrated by
[model-routes.example.json](../../apps/api/config/model-routes.example.json);
each route references, but never contains, its credential environment variable.

## Route Contract

Each route has a stable ID and captures:

- provider, model, and secret-free endpoint;
- request timeout plus Tool calling, structured-output, streaming, and optional
  `seed` capabilities;
- context-window and maximum-output token limits;
- deterministic priority, pricing metadata, definition revision, and effective
  generation policy;
- the name of the environment variable that supplies credentials, never its
  value.

Catalog construction validates endpoint shape, token limits, credential
references, descriptor/client identity, duplicate IDs, and credential-like
metadata. Route and Catalog revisions are SHA-256 digests of canonical JSON.

`generation_policy` has two profiles: `answer_stream` for streamed answers and
`completion` for non-stream calls, including Tool decisions, compaction, and
other auxiliary completions. Their default temperatures remain `0.4` and
`0.2`. Each profile requires a `temperature` in `[0, 2]`; optional `top_p`
must be in `(0, 1]`. An omitted `top_p` is left to the provider, not recorded
as an assumed value. `seed` is accepted only when the route explicitly sets
`capabilities.seed: true`. Unsupported or invalid values fail Catalog
validation before a model request. Seed support is a provider claim, not a
guarantee of identical output across calls or backend revisions.
The local no-credential fallback does not sample model tokens and therefore
does not apply these profiles.

## Decision Protocol

The first model Turn derives a requirement contract containing its purpose,
capability needs, estimated input tokens, and requested output allowance. The
current policy first excludes routes that fail any hard requirement, then
chooses the remaining route with the highest explicit priority. Route ID is the
stable tie-breaker.

That first successful decision establishes Run-level route affinity. Later
Turns validate and reuse the same route rather than silently switching models.
Multi-Agent Worker Stages use the route frozen by their owning Run.

If every route is excluded, the Turn fails before a provider request with
`model_route_unavailable`. It does not silently use the default model.

Each decision emits durable `model.route_decided` evidence with candidate route
IDs, exclusion reason codes, the selected route and actual provider/model. The
event contains no endpoint credentials or secret values. Physical request
details remain owned by Model Request Capture.

The selected Chat LLM covers Turn Engine calls in Single, Multi-Agent, and
Autonomous modes, including model-assisted Agent selection. Context compaction,
adaptive Memory extraction, and conversation title generation resolve that same
Run route. They cannot fall back to another Chat LLM.

Embedding is a separate singleton service configured by `EMBEDDING_*`. Memory
and Knowledge retrieval, Memory persistence, RAG indexing, and embedding-based
verification use it directly; it never participates in Chat model routing.

## Snapshot and Resume

Runtime Snapshot v18 freezes the route policy revision, Catalog revision, all
route contracts, and the independent embedding identity required by the Run.
Effective generation profiles are part of each frozen route and its revision;
Resume sends the frozen values even if the current route file has changed.
Each physical model request records the actual sent parameters in its request
envelope and `model.request_prepared` event, so Replay can show the effective
sampling settings beside the request evidence.
Resume rebuilds a Catalog only from those frozen routes:

- routes added after Run creation are ignored;
- a removed route or changed credential reference fails closed;
- changed model configuration does not replace the frozen provider, model,
  endpoint, capabilities, limits, or pricing metadata;
- Snapshot v17 and earlier remain readable through Replay but are not resumable.

## Current Boundary

H-10A does not implement health scoring, retries across routes, failover,
load-aware selection, per-route cost settlement, or cost/quality optimization.
Provider retry remains inside the selected adapter, while concurrency limits
and Run Budget retain their existing owners. Bounded failover belongs to a
later feature backed by two real targets and evaluation evidence.
