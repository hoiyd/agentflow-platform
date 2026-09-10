# Credential Boundary and Redaction

AgentFlow treats credentials as process configuration, not application data.
`OPENAI_API_KEY` is resolved from the environment while application dependencies
are composed, wrapped in a non-serializable value, and revealed only when the
provider client is constructed. It is absent from general Config, Runtime
Snapshots, Context Manifests, Events, Artifacts, and API responses.

## Source-to-Sink Policy

| Source | Boundary action | Durable or observable sink |
| --- | --- | --- |
| Provider environment credential | Drop after provider construction | Never written to Config, Snapshot, Capture, Event, Artifact, or log |
| Chat, Agent config, Task State, Verification config | Reject credential-shaped input | No Message, Snapshot, Task State, or Completion Contract write |
| Knowledge ingest and query | Reject before embedding or storage | No Document, Chunk, Capture, or provider request |
| Legacy retrieved Knowledge chunks | Drop as restricted content | RAG security metadata records the blocked candidate |
| Memory commit, recall, and candidate proposal | Reject or drop before embedding/storage; omit legacy credential matches from recall | No new Memory or Candidate content write and no unsafe Context item |
| Provider and Tool errors | Redact credential values | Safe API error, Trace, reconciliation record, and log |
| Tool result and side-effect result | Redact observational and durable copies | Model Tool message, Artifact, Trace, and effect journal |
| Run Event payload | Redact immediately before Postgres serialization | Typed Event payload |
| Model request Capture | Metadata-only, explicit redaction, or safe full copy | Credential matches force `full` to `redacted` |
| Verification summary, details, and Artifact | Redact before record append | Verification Evidence and Artifact |
| Process logs | Log metadata instead of Tool bodies, then redact at the log writer | Standard server log output |

Hashes may be retained for equality, idempotency, and reconstruction checks.
They are one-way identifiers in this system and are not used to recover Secret
values.

## Runtime Content Rule

The policy does not silently rewrite content used for a model decision. New
credential-shaped user input is rejected before execution. Legacy retrieval
content that matches the policy is excluded as restricted context. Redaction is
reserved for observational or durable copies such as Events, Captures,
Artifacts, errors, and logs.

This distinction keeps debugging records safe without changing the meaning of
an accepted model request behind the caller's back.

## Detection and Limits

The deterministic detector covers sensitive JSON keys, Authorization schemes,
common provider token shapes, JWTs, private keys, credential-bearing database
URLs, and explicit credential assignments. Detection is intentionally
conservative and may reject a credential-shaped example even when it is not a
live Secret. Clients receive the stable `credential_content_rejected` error and
can replace the value with a non-secret placeholder.

This boundary is defense in depth, not authentication, authorization, a Secret
Manager, data-loss prevention, or a complete privacy classification system.
Workspace IDs are still caller-selected. Run AgentFlow only in a trusted
environment or behind an authenticated gateway until identity and membership
policy are implemented.
