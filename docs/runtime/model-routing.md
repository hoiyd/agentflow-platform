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

The production composition currently registers one `primary` OpenAI-compatible
route. Tests register two dummy routes against the same client contract to prove
selection and failure behavior without claiming a measured quality or cost
benefit.

## Route Contract

Each route has a stable ID and captures:

- provider, model, and secret-free endpoint;
- Tool calling, structured-output, and streaming capabilities;
- context-window and maximum-output token limits;
- deterministic priority, pricing metadata, and definition revision;
- the name of the environment variable that supplies credentials, never its
  value.

Catalog construction validates endpoint shape, token limits, credential
references, descriptor/client identity, duplicate IDs, and credential-like
metadata. Route and Catalog revisions are SHA-256 digests of canonical JSON.

## Decision Protocol

Every Turn derives a requirement contract containing its purpose, capability
needs, estimated input tokens, and requested output allowance. The current
policy first excludes routes that fail any hard requirement, then chooses the
remaining route with the highest explicit priority. Route ID is the stable
tie-breaker.

If every route is excluded, the Turn fails before a provider request with
`model_route_unavailable`. It does not silently use the default model.

Each decision emits durable `model.route_decided` evidence with candidate route
IDs, exclusion reason codes, the selected route and actual provider/model. The
event contains no endpoint credentials or secret values. Physical request
details remain owned by Model Request Capture.

## Snapshot and Resume

Runtime Snapshot v16 freezes the route policy revision, Catalog revision, and
all route contracts required by the Run. Resume rebuilds a Catalog only from
those frozen routes:

- routes added after Run creation are ignored;
- a removed route or changed credential reference fails closed;
- changed model configuration does not replace the frozen provider, model,
  endpoint, capabilities, limits, or pricing metadata;
- Snapshot v15 and earlier remain readable through Replay but are not resumable.

## Current Boundary

H-10A does not implement health scoring, retries across routes, failover,
load-aware selection, or cost/quality optimization. Provider retry remains
inside the selected adapter, while concurrency limits and Run Budget retain
their existing owners. Multi-route production configuration and bounded
failover belong to a later feature backed by two real targets and evaluation
evidence.
