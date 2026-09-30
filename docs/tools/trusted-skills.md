# Trusted Skills

Use [Skill Checker](skill-checker.md) to check installed packages before
trust/binding. Format-valid text can still rely on unsupported execution or
invocation semantics; Skill Checker is read-only and does not grant permissions.

## Contract

SKILL-001 separates reusable task methods from executable Tool capabilities.
Only immediate `SKILL.md` packages inside operator-configured installation
roots are trusted inputs. Agents explicitly bind package names; a Skill never grants tools,
credentials, Workspace access, or script execution.

The format follows [Agent Skills](https://agentskills.io/specification): YAML
frontmatter with `name`/`description`, Markdown instructions, and relative text
resources. Progressive metadata -> instructions -> resource loading follows
[pi Skills](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md).
This implementation deliberately does not adopt executable scripts or
`allowed-tools` as authorization.

## Setup and Invocation

Prefer [native Vercel Skills CLI from the repository root](skill-installation.md)
for remote packages. `./scripts/skill-install.sh --repo ... --path ...`
runs the existing Go CLI as an
[operator-only Go fallback](skill-installation.md#restricted-go-fallback) when Vercel cannot
be used. Download, operator review, runtime trust and Agent binding remain
separate steps; neither installer changes Loader permissions or configuration.

Run the API from `apps/api`, set this operator-owned environment variable, then
restart the API:

```bash
TRUSTED_SKILL_DIRS=../../.agents/skills
```

Each CSV entry names an operator-trusted installation root. Immediate directories
containing `SKILL.md` are discovered, without recursive repository scanning or
following package symlinks. Unset/empty configuration disables new bindings;
`.env.example` explicitly selects the shared installation root;
an existing empty root also produces an empty catalog. Missing roots, individual
package paths, duplicate names, invalid packages or more than eight packages
fail startup rather than producing a partially trusted catalog. Installing into
a configured root makes a valid package available after restart, so review its
content first. No request accepts a local directory or uploaded package.

To use only the repository's two reviewed fixture methods instead of installed
packages, set `TRUSTED_SKILL_DIRS=../../examples/skills`. This trusts that parent
root, not a list of its individual packages.

`GET /api/skills` exposes only name, description, package hash and required Tool
names. In Single mode, **Configure > Skills** binds these names to an Agent.
The compact **Invoke skill** selector adds `/skill:name` to the task. An explicit
invocation such as `/skill:<skill-name> Explain the release procedure`
loads the bound method on the first model request. Without that prefix, the
model sees bound metadata and can select a relevant method with `skill_load`.
Multi Workers and Loop Act stages use the same tools; there is no separate
Skills dashboard or Agent-editing surface in those modes.

Example methods can have different dependencies (replace package placeholders):

| Package | Required Tools | Additional setup |
| --- | --- | --- |
| `<web-research-skill>` | `web_search` | Agent allowlist, enabled/ready Tavily Binding and operator egress policy. |
| `<knowledge-skill>` | `knowledge_search`, `knowledge_read` | Existing scoped Knowledge bindings and indexed Workspace documents. |

Tool dependencies are checked against ready Agent capabilities on save and Run
creation. This does not bypass per-call policy or data-scope validation: a
ready Tool can still be denied by operator policy. No dependency is silently
enabled, installed or granted.

## Package Format and Boundaries

```markdown
---
name: <skill-name>
description: Answer using delivered evidence.
metadata:
  agentflow-required-tools: knowledge_search knowledge_read
---
Read [the checklist](references/checklist.md) when needed.
```

This is a deliberately restricted Agent Skills subset. `name` must match the
directory basename and use lowercase letters, digits and single hyphen
separators, at most 64 bytes. The required `description` is limited to 1,024
Unicode characters. YAML multiline descriptions are supported; duplicate keys,
invalid frontmatter and multiple YAML documents are rejected.

| Boundary | Limit |
| --- | --- |
| Configured / frozen packages | 8 |
| Instruction body | 8 KiB UTF-8 text |
| One resource | 8 KiB UTF-8 text |
| Package body plus resources | 32 KiB, at most 16 resources |
| Resource path | At most 256 bytes, relative `references/` or `assets/` path |
| Resource suffix | `.md`, `.txt`, `.json`, `.yaml`, `.yml`, `.csv` |
| One read | Default 2,048 bytes, maximum 4,096; UTF-8 boundary-aware offsets |
| Skill tool execution | Existing Executor: serial, 5-second timeout, 12,000-byte result cap |

All package file symlinks are rejected, including links within the package.
`os.Root` constrains file reads even if paths change while loading. Binary,
non-regular, empty, NUL-containing and recognized credential-bearing text is
rejected before persistence. Credential checks use the existing redaction
boundary and cannot detect every possible secret; operators must review content.
Other directories, including `scripts/`, are neither traversed nor executed.

## Context, Recovery and Replay

Run creation freezes the small bound packages, including full instruction and
resource text, resource identities, dependency metadata and SHA-256 hashes.
This bounded eager **storage** supports recovery; **model Context** remains
progressive:

1. Only the current frozen Agent's metadata enters Context initially.
2. Explicit invocation or a successful Agent-owned `skill_load` activation adds
   the instructions on the next assembly. The Tool result lists metadata and
   resource paths, not another copy of the body.
3. `skill_read` returns a requested frozen resource page only after activation.
   `next_offset`, `total_bytes` and `truncated` allow continuation.

Activation is derived from existing successful Tool events, isolated by Agent
identity inside the Run. Explicit activation is also retained in the durable
Manifest (`agent_id`, selected entry, frozen package hash and `activation=explicit`),
so recovery does not need a fabricated `skill_load` event or a surviving input
prefix. Activation lasts for that Agent within the Run, including subsequent
Stages; a different Agent does not inherit it. Repeated activation produces one instructions entry
per package/hash in each assembled input. Resume validates frozen content and
reuses its events; it never rereads a changed or deleted local package. Removing
a package from the live catalog prevents new bindings but does not remove a
current Run's frozen method. Missing or incompatible Tool bindings still block
Resume under the existing Runtime Snapshot protocol.

`skill_metadata` and `skill_instructions` appear in Context Manifest with a
`skill:<name>@<hash>` identity. Metadata and activated instructions are required
input under the existing total Context budget; overflow fails rather than
silently dropping a selected method. Resource pages use normal Tool result
budget/Artifact rules. There is no second Skill budget or recovery database.

Methods are JSON-escaped user-role guidance under a fixed system policy, not
system-role authority. Resources are untrusted reference data: they do not grant
Tools, Workspace access or `[S#]`/`[W#]` citation aliases. This preserves executable
permission boundaries; it is not a guarantee that every model will ignore every
malicious instruction.

Replay returns frozen packages in `runtime_snapshot.skills`. Its default-collapsed
**Skill evidence** section uses `projection.skill_evidence` from Replay and
`GET /api/runs/{id}/projection`. Rows distinguish **Bound**, **Instructions included**
and **Resource read**, grouped by frozen Agent/Stage and package identity. Input,
read and failure buttons select the retained trace event; no new dashboard or
activation table is created. Runs without frozen bindings show no section.

An included-input observation requires a selected `skill_instructions` Manifest
entry with the frozen hash **and** a matching persisted physical-request envelope
(Run, ModelCall, Stage and Turn). The row exposes the first Manifest event,
matching request ID and, when retained, the first `model.request_prepared` sequence.
This proves inclusion in the prepared transport input, not provider acceptance,
model compliance, task quality, or causal use of other Tools in the same Stage.
Explicit/model activation origin comes only from explicit Manifest metadata or
a successful Agent-owned `skill_load` receipt; unknown origin stays `not_observed`.
An `already_loaded` confirmation alone cannot establish model-origin activation.

Resource observations contain the frozen resource hash and UTF-8 byte interval;
pagination is valid partial reading, whereas an Executor-truncated/failed result
is not counted as a successful read. Typed errors remain inspectable. Package and
resource hashes are different identities. Missing request/Manifest records,
old Runs or unknown Agent attribution stay **not observed**, not inferred from
the model's answer. Merely advertising Skill metadata is not activation.

The instruction token estimate describes the **first observed input**, not total
Run cost or provider billing per Skill. Whole-request usage remains in the Ledger.
Full Capture disabled/metadata-only still supplies identity evidence without
adding user/resource body persistence. Disabling the request recorder entirely
means request-backed inclusion cannot be observed. Capture follows the existing policy.
**Snapshot content is retained independently of optional full request capture**;
only operator-reviewed, non-secret methods should be configured.

PostgreSQL persists Agent bindings in `agents.skills JSONB` through an idempotent
startup migration; immutable package contents use existing Run Snapshot JSON.
The optional fields do not change the current Snapshot protocol version. Older
current-version Runs without Skills remain unchanged and do not gain live Skills
on Resume.

## Failure Inventory

Activation evidence additionally distinguishes a frozen binding from instructions
included in a recorded request, and a successful resource read from a failed or
truncated observation. Missing Manifest/request records mean **not observed**;
neither answer text nor a configured package proves activation. Repeated calls,
Agent/Stage changes, cancellation, compaction and Resume must retain the frozen
identity without duplicating instructions or bypassing the Context budget.

| Failure | Required outcome |
| --- | --- |
| Missing package or invalid/duplicate frontmatter/name | Typed diagnostic; never partially trust a package. |
| Conflicting package names | Reject configuration rather than picking a directory by ordering. |
| Oversized, binary, credential-bearing, or non-regular content | Reject before freezing content or publishing it. |
| Absolute path, parent traversal, backslash path, or symlink | Reject; no arbitrary filesystem reads. |
| Unbound Skill, wrong Agent/Run/Conversation scope | Reject without granting package or Tool access. |
| Required Tool unavailable or absent from frozen Agent allowlist | Explicit dependency failure; never enable a Tool implicitly. |
| Skill or resource changes after Run creation | Use frozen content and hash on Resume; never reread current files. |
| Repeated activation | One instructions entry per Skill, not duplicate Context bodies. |
| Malicious instructions suggesting privilege escalation | System/user request precedence and frozen Tool policy still apply. |
| Context overflow or Artifact spill | Existing limits remain enforced; missing content is not silently treated as loaded. |
| Store/event failure | Fail closed rather than assuming a prior activation succeeded. |
| Missing or malformed Manifest/request envelope or mismatched scope/hash | Report not observed; do not infer inclusion or activation from answers. |
| Explicit activation without `skill_load`, then Resume/Stage change | Recover the same Agent's frozen method from Manifest; another Agent remains inactive. |
| Resource observations trimmed by Context assembly/compaction | Method identity survives independently; re-read the same frozen page under the existing limits. |

Deterministic checks prove loading, isolation, and recovery contracts, not
live-model task quality. TOOL-025 remains responsible for measured task benefit.

## Repeatable Checks

From `apps/api`, with the repository's configured Go version:

```bash
go test ./internal/skill ./internal/contextassembly -run 'TestSkill|TestTrusted|TestFrozen' -count=1
go test ./internal/skill -run TestExplicitSkillManifest -count=1
go test ./internal/projection -run TestSkillEvidence -count=1
go test ./internal/httpapi -run 'TestSkillCatalog|TestTrustedSkillProgressive' -count=1 -v
go test ./internal/agent -run 'TestSkillSnapshot|TestSkillResume' -count=1 -v
go test ./internal/store -run TestPostgresSkillsRoundTripAcrossRestart -count=1 -v
```

The all-mode backend integration test logs `trusted_skill_evidence` JSON with
Run identity, package hash, Tool counts, selected Manifest input and captured
request count. It uses a deterministic local HTTP model, not a browser or live
provider. The Postgres check requires `TEST_DATABASE_URL` naming a dedicated
test server with CREATEDB privileges; it creates and removes a disposable
database rather than migrating the configured source database. Without that
variable, it is explicitly skipped, not counted as persistence evidence.

The focused browser gate traverses Next.js, the real Go runtime and disposable
Postgres for Single, Multi and Loop, including persisted Skill evidence, event
navigation and page reload. With `TEST_DATABASE_URL` configured, run from the root:

```bash
bash scripts/test-browser.sh --grep 'Skill \+ reasoning'
```

Replay/request evidence is retained in Playwright `test-results` attachments.
The metadata-only explicit/no-Tool backend scenario, frozen-directory-removal
Resume, compaction/overflow, failed reads, cancellation and missing-observation
checks have separate deterministic assertions; they do not establish live task
quality or a script-execution sandbox.
