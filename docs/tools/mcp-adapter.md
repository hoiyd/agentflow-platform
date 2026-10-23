# MCP connections

Workspace owners manage remote Streamable HTTP services in **Tools → MCP connections**.
The [official MCP Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)
adapts discovered tools into ordinary Bindings governed by the shared Executor.
There is no default MCP service; DeepWiki is an optional smoke-test example.

## Workspace connections

1. Add a name, endpoint and authentication (none or Bearer). Save, then **Test**
   to discover tools. Testing never grants tools or invokes `tools/call`.
2. Edit the connection, select tools reviewed as read-only, and enable it.
   An explicitly destructive remote hint rejects selection; other hints cannot
   prove safety. The owner must review the service's actual behavior.
3. Allow the resulting tools in **Tool catalog**, then bind them to an owned
   Agent in Configure. Names are `mcp_w<connection-id>__<remote-name>`; changing
   a connection's display name does not change tool identity.

Connections belong to the selected Workspace. API reads/writes, discovery,
Agent choices and effective catalogs remain scoped; operator-managed global
connections are not editable here. Workspace grants and Agent bindings remain
separate from enabling a connection. Selected tools receive exact, locally
generated logged policy rules; an explicit operator rule still takes precedence.

`MCP_CREDENTIAL_ENCRYPTION_KEY` must contain a base64-encoded random 32-byte key
before saving Bearer tokens (for example, generate one with `openssl rand -base64 32`).
Keep the key outside the database and back it up separately. AES-GCM binds each
encrypted token to its Workspace and endpoint. APIs return only
`credential_configured`; tokens never enter prompts, Snapshots, traces or captures.
Without the key, unauthenticated connections work; existing encrypted dependencies
remain unavailable rather than blocking unrelated tools. Key replacement requires
re-entering stored tokens; this slice does not implement key rotation tooling.

User URLs require public HTTPS. `MCP_WORKSPACE_LOCAL_ORIGINS` is an operator-only,
comma-separated list of exact origins for approved private/self-hosted services.
An entered literal IP is not a private-network grant. DNS is checked at dialing,
proxies and redirects are denied, and credentials only reach the configured endpoint.
Private-origin exceptions use literal private IPs or `localhost`, following the
shared egress policy; DNS names resolving into private networks remain denied.

Connection changes take effect without restarting the API; operator key/origin
configuration requires a restart. URL/authentication/token changes
invalidate discovery: save disabled, re-test, review and re-enable. Updates use the
last observed `revision`; stale edits or discovery responses return 409. Schema
changes require a new Run; old Resume never substitutes newly discovered definitions.
Current connection/Workspace authorization is checked before each actual tool call,
including old Bindings. Disabling or soft-deleting blocks subsequent calls; it cannot
undo an already dispatched request. Delete erases the token and discovered payload,
but retains historical Run evidence. At most 8 active connection records and
64 selected tools per connection are allowed; connection tests have 4 shared slots.

The first Workspace slice excludes stdio/commands, OAuth, arbitrary headers,
query-string credentials, write tools, plugins/installation and MCP Apps. A remote
connection cannot launch a process on the browser user's computer.

## Optional operator smoke test: DeepWiki

This opt-in example tests a public endpoint without a model key. It is not a
startup prerequisite, a built-in provider, or a default Workspace connection.
The owner-facing flow above does not require an operator JSON file.

1. Set `MCP_CONFIG_PATH=config/mcp.deepwiki.json` in the API environment. The
   [example](../../apps/api/config/mcp.deepwiki.json) permits only
   `read_wiki_structure` on `https://mcp.deepwiki.com/mcp`, which
   [DeepWiki documents](https://docs.devin.ai/work-with-devin/deepwiki-mcp) as a
   free, unauthenticated public-repository endpoint.
2. Add `mcp_deepwiki__read_wiki_structure` to `enabled_tools` in the existing
   operator file named by `TOOL_CONFIG_PATH`. Preserve existing entries and
   policy rules. Add this exact rule to `security_policy.rules`:

   ```json
   {
     "id": "deepwiki-public-docs",
     "tool": "mcp_deepwiki__read_wiki_structure",
     "action": "allow_and_log",
     "capability": {
       "source": "remote",
       "scope": {
         "resources": [{"kind":"external_service","name":"mcp_deepwiki","access":"read"}],
         "network": {"mode":"external","targets":["https://mcp.deepwiki.com"]}
       },
       "side_effect_class": "none", "rate": "run_budgeted",
       "reversibility": "reversible", "visibility": "run",
       "approval_mode": "none", "audit_level": "basic"
     }
   }
   ```

3. Restart the API. Open Tools after discovery completes (reload if it still
   shows pending), allow the namespaced Tool in the Workspace, and bind it to
   an owned Agent in Configure. A Skill may require that same stable name;
   its readiness check uses the ordinary Catalog and grants.
4. Ask the Agent to read the documentation structure for
   `modelcontextprotocol/go-sdk`, then explain its client/server and transport
   sections. The Tool arguments are `{"repoName":"modelcontextprotocol/go-sdk"}`.
   Inspect ordinary `tool.completed` events, results and frozen `source`
   metadata through View trace / Replay. Remote data has no automatic `[W#]`
   citation aliases; only the existing web-search citation protocol assigns those.

An MCP server entry accepts `id`, `url`, `read_only_tools`, optional
`bearer_token_environment`, and `required` (default false). Server IDs are
lowercase letters/digits/hyphens, start with a letter, and have at most 20
characters; `w<decimal-id>` is reserved for Workspace connections. Remote names accept letters/digits/underscore/hyphen up to 38
characters; the combined model name stays within 64 characters. A service file
or credential change in this operator file requires restart; remote definitions are rediscovered.
Bearer credentials are loaded only from the named environment variable, granted
as logical `mcp_<server>` scopes, and sent only to the exact configured endpoint.
Authenticated servers need that same scope in their local policy rule.

## Execution and recovery

Operator-file discovery runs at startup in the background and every 30 seconds. Explicit
`required: true` servers are checked before startup succeeds. Optional failures
leave unavailable placeholders so ordinary Chat starts without a remote wait.
Connection success alone does not imply an authorized, valid or available Tool.
Workspace discovery is explicit through **Test**, not this background loop.

Each call opens a bounded session, lists tools, verifies its captured definition,
calls once, and closes. The fingerprint includes the destination, credential
reference, negotiated MCP protocol version and remote definition, including the
output schema. Rediscovery
atomically publishes a new Catalog; existing Runs keep their prior definitions.
Resume uses the existing definition comparison and current availability checks.
Rejected restoration reports the existing Snapshot-unavailable failure as a
conflict and preserves the waiting/recovery point instead of failing the Run.
Tool source metadata still uses existing Snapshot JSON, without a Snapshot
version bump; omission preserves older built-in definition hashes. Workspace
connection intent and encrypted credentials live separately in `mcp_connections`,
created idempotently at startup; existing Run data is not migrated or rewritten.

The shared Executor still owns Schema/Policy/Workspace/Agent checks, Run Budget,
Progress Guard, result Artifacts and typed events. Successful results contain
`server_id`, `tool`, text, optional `structured_content` and
`trust_boundary: untrusted_external_content`. Declared output schemas are
validated before results are accepted. RPC/transport errors use
`provider_unavailable`, remote `isError` uses `execution_failed`, invalid output
uses `result_encoding_failed`, and local timeout/cancel retain their existing
typed codes. Logs retain sanitized underlying errors; public errors do not expose
raw remote bodies or credentials.

| Limit | Value |
| --- | --- |
| Config / servers / approved tools | 64 KiB / 8 / 64 total |
| Discovery | 5 seconds per server, at most 128 listed tools |
| Input/output schema | 64 KiB each; shared Draft 2020-12 validator, no external references |
| Description | 4096 bytes |
| Call | 20 seconds including setup/list/call; cancellation bounds auxiliary requests too |
| Response / SSE event | 1 MiB each, including decompressed bodies |
| Retained model result | Existing Executor / Artifact bounds |

HTTPS is required except explicit loopback HTTP. Governed egress disables
proxies, validates DNS at dialing and denies redirects. Returned links are never
fetched. Only text and JSON structured content are supported. OAuth, stdio,
legacy SSE transport, subscriptions/list-changed push, prompts, resources,
sampling, elicitation, interactive continuation, writes and task extensions are
outside this slice. Periodic rediscovery provides the catalog update mechanism.
Read-only approval is an operator trust decision; the adapter cannot prove a
remote implementation has no side effects or prevent a server changing between
the pre-call comparison and execution.

## Failure inventory and acceptance

Tests protect these boundaries:

| Failure | Expected outcome |
| --- | --- |
| Invalid config, duplicate server/name, unsafe endpoint | Reject local configuration before starting discovery |
| Missing credential, unreachable server, failed discovery | Configured tools remain visible but unavailable; ordinary Chat still works |
| Unsupported schema dialect, external reference, oversized schema/list | Reject discovery or the affected tool; never bypass validation |
| Same remote tool name on two servers | Distinct, stable `mcp_<server>__<tool>` names |
| Missing policy/grant, invalid arguments | Shared Executor rejects before remote invocation |
| RPC failure versus `isError` | Distinct typed failures, neither becomes successful Tool evidence |
| Invalid structured output, unsupported content, excessive bytes | Reject result; no automatic resource/link fetching |
| Redirect or credential reflection | No credential forwarding; sanitize retained output and diagnostics |
| Cancellation, timeout, shutdown | Bound requests and session teardown; no automatic Tool retry |
| Input/output schema, endpoint or negotiated protocol changes | New catalog only; frozen Run/Resume definitions cannot silently upgrade |
| Optional server reconnects | Rediscover for subsequent Runs; existing Run still checks its captured definition |

Acceptance uses a deterministic MCP HTTP fixture through the real Binding and
Executor, a focused browser gate through production composition and disposable
Postgres, and an opt-in live public-repository read. Fixture results do not prove
the quality or availability of an external service.

Run focused checks from `apps/api`:

```bash
go test ./internal/tool -run 'TestMCP|TestManagerReplace' -count=1
MCP_LIVE_TEST=1 MCP_EVIDENCE_PATH=/tmp/agentflow-mcp-live.json \
  go test ./internal/tool -run '^TestMCPLiveDeepWiki$' -count=1 -v
```

The opt-in live check reads public repository metadata without a model/API key;
it verifies the real adapter and Executor, not a live LLM or full Run. Its JSON
records endpoint, task input, source hash, returned evidence, time and limitations.
The [browser gate](../operations/functional-regression-testing.md#trusted-mcp)
separately verifies production composition and persisted Replay across all modes.
