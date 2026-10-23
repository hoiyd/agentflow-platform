# Web Fetch Evidence Boundary

`web_fetch` reads a public page identified by a `web_search` source ID in the
current Run. It does not accept a URL, credentials or browser state.

## Use And Evidence

Enable `web_fetch` in the service config, Workspace Tool grants and Agent
allowlist. Missing service config enables it by default; explicit enabled lists
and policy rules, Workspace grants and saved Agents are not expanded. New Field Researcher
template copies include both `web_search` and `web_fetch`. No additional API key
is needed for fetch; search still requires Tavily.

```json
{"source_id":"W1"}
```

The resolver loads successful search events for the persisted Run/Conversation.
Only an existing source can supply its URL. Fetch is read-only, consumes the
ordinary Tool budget, and obeys Workspace authorization and private-context egress
denial. `run_web_sources` is its logical network policy target; it is not an
arbitrary-host wildcard. Each actual connection uses the public-only transport.

`status=read` means the extracted body is complete. `status=partial`,
`content_complete=false` and `truncated=true` explicitly identify a retained
prefix. `content_hash` hashes that retained, redacted UTF-8 text, not raw HTML or
the entire remote page. URLs, final URL, fetch call and immutable content remain
inspectable in ordinary Tool events/Replay.

Fetch keeps the search `[W#]` identity. Citation resolution requires the intact
result in the final model Manifest and prefers body evidence over a selected
snippet. A governed Artifact preview is not body evidence: `artifact_read` must
deliver the complete retained JSON from offset zero in one selected result.
Partial Artifact pages, compaction, failures and invented IDs cannot qualify.
Search snippets remain valid evidence only for their own delivered content.
The existing Chat/Replay citation contract and source links are unchanged.

## Limits

- HTTPS only; up to three same-origin redirects. Cross-origin redirects are
  rejected, even to another public host. No proxies, cookie jar or forwarded auth.
- 10-second HTTP timeout within the 12-second Tool timeout; 2 MiB each for wire
  and decompressed bytes. Only UTF-8 HTML/plain text and identity/gzip encoding.
- Prefer `main`, then `article`, then body text. No JS, embedded-page fetches,
  PDFs, login pages or browser automation; extraction is not a general reader engine.
- Extract at most 8,192 bytes, then reduce the retained prefix if needed to fit
  a 6,000-byte JSON-string recovery allowance. This reserves metadata/envelope
  space inside the default 8,000-byte Tool batch limit and prevents recursive
  Artifact spills. Results above 4,000 bytes use the existing Artifact governor.
  Smaller operator budgets may prevent a complete read; they are never bypassed.
- Text remains untrusted, including instruction-like prose. Structural exclusion
  and platform guidance do not guarantee that all prompt injection is detected.

## Failure Inventory

| Boundary | Required outcome |
| --- | --- |
| Missing, forged, failed or other-Run source | Reject before HTTP; never create a new `[W#]` |
| HTTP, URL credentials, private/reserved IP or mixed DNS answers | Deny before connecting |
| DNS rebinding | Resolve once per connection, validate every answer and dial a validated literal |
| Redirect | Revalidate HTTPS and exact original origin; reject cross-origin hops and redirect loops |
| Cookies, auth or environment proxies | Never forward or use them |
| 403/429/5xx, binary data, empty body or unsupported encoding | Return an explicit typed observation, not a successful page read |
| Oversized wire/decompressed body | Reject with no body evidence |
| Oversized extracted text | Mark partial content explicitly; never claim the full page was read |
| Timeout/cancel/body read failure | Stop the request and retain failure evidence; cancellation takes precedence over concurrent EOF or decode errors |
| Scripts, embedded pages or instruction-like body text | Never execute; treat extracted text as untrusted data |
| Result exceeds Context allowance | Reuse governed Artifact storage and bounded reads |
| Unselected fetch, incomplete Artifact read or invented citation | Do not promote it to structured evidence |

The network boundary follows the [OWASP SSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html).

## Verification

With Go 1.26.5 and `TEST_DATABASE_URL` pointing to a disposable pgvector test
server, run from `apps/api`:

```bash
EVALUATION_REPORT_DIR=/tmp/web-fetch-evidence go test ./internal/tool ./internal/egress ./internal/tool/webcitation ./internal/contextassembly ./app -run 'TestWebFetch|TestFetchEvidence|TestWebSources|TestManifest|TestPublicHTTPTransport' -count=1
```

The backend integration retains `web-fetch-{inline,artifact,forged-source}.json`
with Run/input/model/budget identities, selected calls, citations, outcomes and
limitations, plus the Git revision, dirty-tree flag and model-visible definitions.
It uses actual streaming HTTP, real Bindings, the shared Tool loop
and reopened Postgres. Remote HTTPS and model replies are controlled fixtures;
Dial rebinding is checked separately through the actual public HTTP transport.
This is not browser E2E, full Multi/Loop orchestration or live-model quality evidence.

The separate browser gate, `npm --prefix apps/web run test:e2e -- web-fetch.spec.ts`,
checks default readiness, owned Agent configuration, a real missing-source
rejection in Single/Multi/Loop and Replay reload through production composition.
Its `web-fetch-browser-evidence.json` retains all three Runs and frozen contracts.
It makes no public page request; successful acquisition belongs to the backend
gate above. Both gates use disposable Postgres, not the application database.
