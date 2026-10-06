# Agent Instruction and Tool Data Boundaries

Editable Agent instructions are task guidance, not authorization. Fixed platform
instructions are assembled separately. Prompt separation helps the model, but
only backend authorization and execution boundaries enforce permissions.

## Failure Inventory

| Attack or failure | Required outcome |
| --- | --- |
| Editable prompt claims it overrides platform rules | Fixed platform message remains separate and required |
| Follow-up, text stage, or inactive assembly session | Fixed policy is present once; native Tool pairs remain intact |
| Context input budget is too small | Fail before the model request; never discard the platform policy |
| Model puts credential-shaped content in network Tool arguments | Deny before budget accounting, journaling, Handler or network |
| Private retrieved context is encoded or paraphrased into a query | Deny network Tools after private-context exposure, regardless of query wording |
| Local read and network call share a parallel batch | Establish the private-data boundary before any batch Handler runs |
| A later round, Stage or Resume no longer contains the original excerpt | Restore the boundary from durable Run evidence; never downgrade it |
| Prompt claims the user approved an action | Existing `ask` / `human_only` policy still returns `approval_required` |
| Private data is used by a permitted local Tool | Allow within existing scope and sandbox restrictions |
| No private context and a public search query | Permit only with the existing Agent allowlist, credentials and operator rule |
| Restoring Run evidence fails | Fail closed; do not infer that the Run has no private data |

## 1. Fixed Platform Guidance

The shared Context Assembler inserts one code-owned system message separately
from editable Agent instructions. It also applies to text stages and calls
without an assembly Session. Reassembly replaces the platform message instead
of duplicating it or merging Skill/retrieval instructions into it.

With an assembly Session, the message is required, counted against the input
budget and recorded in the Context Manifest with source `platform_policy` and
policy version `platform-security-v1`. The prefix hash includes all system
messages. An insufficient input budget fails before the model request; it cannot
silently evict the policy. Calls without a Session receive the message but do
not produce a Manifest or gain a new budget enforcement mechanism.

This is model guidance, not a guarantee that a malicious instruction will be
ignored. Agent configuration and native model continuation fields remain
unchanged. The backend, not message wording or placement, grants authority.

## 2. Tool Data and Approval Boundaries

The shared Tool Executor derives data exposure from trusted capability and
selection metadata, not Tool names or a semantic prompt classifier:

- Selected History, History Search, Memory, Knowledge, Compaction and Task State
  entries are conservatively private, including summaries of those records.
- Tools using Run, Conversation, Workspace or filesystem resources mark private
  exposure after authorization, including metadata, write receipts and failures.
- Remote Bindings, any declared network access, and external-service resources
  are data transmission exits. Operator permission is necessary but cannot
  override the private-data restriction.
- A Binding that both uses private resources and transmits data is denied. A
  batch containing private-resource and network calls is protected before any
  Handler runs, irrespective of order or parallel scheduling. Even a rejected
  private call makes that batch conservative.
- Before permitted egress, common credential-shaped arguments are rejected
  using the existing credential/redaction checks. This is shape detection, not
  exhaustive secret discovery or arbitrary encoded-secret detection.

Exposure is monotonic within an Executor. The Tool loop restores it from
existing `context.assembled`, `tool.completed` and `tool.failed` Run events
before executing a later Stage or Resume. Terminal Tool events include
`private_data`; older events without that marker use trusted local descriptors,
and unknown old Bindings are conservatively private. Missing or malformed
Manifest evidence, or an unavailable event store, fails closed.

**The default is Run-scoped, not merely request-scoped:** once private data has
been exposed in a Run, later network Tools in that Run remain denied even if the
current model request omits the original excerpt. Selected conversation history
can also protect a subsequent Run. This intentionally restricts mixed
private-data-plus-web tasks and public-only conversations whose selected history
cannot be proven public. A public lookup in an earlier round before any private
exposure remains possible; a same-batch ordering trick does not.

Denied exits return the existing `security_policy_denied` Tool error, with
`private_context_egress_denied` or `credential_content_egress_denied` as
`policy_reason`. They do not consume Tool execution budget, create an effect
receipt or invoke the Binding Handler. The model receives the denied observation
and can continue with a permitted local Tool or answer. Existing Tool Trace and
Run Replay expose the reason, including after reload; no frontend API changes
or database schema migration are required.

Existing `ask` / `human_only` actions still return `approval_required`. A prompt
claiming user consent is not authorization. There is no new approval workflow,
declassification action or prompt-level override in this change.

### Limits

- Current user text and editable Agent instructions have no inferred privacy
  label. The platform message and credential checks are not general-purpose DLP
  for arbitrary private text supplied there.
- Private context is still sent to the configured inference provider. This
  restriction governs Tool egress, not the model route itself.
- Trusted Skill metadata/instructions are not automatically labeled private by
  the Manifest. Actual Skill load/resource Bindings use Workspace resources, so
  their execution establishes the conservative boundary too.
- Public Tool results are not automatically private. Honest, complete Binding
  capability declarations and routing through the Executor are prerequisites;
  ad hoc network calls outside it are not protected by this check.
- No semantic content moderation, arbitrary prompt-attack detection, new OS
  isolation, or proof of model refusal is claimed. Scope, credential grants,
  destination policy and sandbox restrictions still apply independently.

## 3. Repeatable Attack Regressions

From `apps/api`, with the repository's supported Go environment:

```bash
go test ./internal/contextassembly ./internal/tool ./internal/agent/toolloop ./internal/httpapi \
  -run 'Test(PlatformPolicy|WebSearchRejectsCredentialContent|PrivateReadBlocks|HostileAgent|DataBoundaryRestore|RestoredPrivateEvidence|PromptConsent|ScopedKnowledgeToolsSearchReadCitedAnswerAcrossModes)' -count=1
```

These checks use real Web Search and Knowledge Bindings plus controlled model
and network fixtures. They verify zero outbound HTTP for credential/private-data
denials, mixed-batch ordering, restored evidence, selected versus excluded
sources, malformed evidence, claimed-consent approval attempts and actual
Knowledge retrieval in Single, Multi-Agent and Autonomous execution. Normal public search and local execution
remain covered by the existing positive cases.

For the narrow browser gate, set `TEST_DATABASE_URL` to a **disposable Postgres**
instance, never the application database. From `apps/web`:

```bash
AGENTFLOW_PROMPT_SECURITY_TEST=1 npm run test:e2e -- agent-security.spec.ts
```

The gate creates the Agent through UI, keeps its hostile editable instructions,
writes real Task State, denies Web Search, permits Calculator, then verifies
persisted events and Replay after reload. It uses production application
composition, independent model wire checks and no screenshots. The retained
`agent-security-evidence.json` attachment in the Playwright report includes Run
identity, configuration, Manifest, rejection and durable effect evidence.
Browser evidence checks pre-Handler denial, not packet capture or live-model
quality; controlled backend HTTP tests supply the independent zero-egress check.
