# Capability-aware Agent Selection

Multi mode routes an approved plan with a two-phase policy:

```text
frozen candidates -> deterministic eligibility -> deterministic or LLM ranking -> bounded Child Run
```

## Typed Requirements And Eligibility

Eligibility is a local, fail-closed check. A candidate must have a non-empty,
unique frozen Agent ID, and every Tool declared by its frozen profile must be
present in the Run's restored Tool Catalog. Natural-language keywords are not
treated as authorization or hard capability requirements. Tool invocation
remains governed by the existing Tool Security Policy and Tool Executor.

When the user approves a Multi plan, `routing_requirements` may declare
`required_tools`, `prohibited_tools`, `require_memory`, and
`require_retrieval`. These fields are hard requirements: they can remove a
candidate but cannot grant Tool, Memory, or Retrieval authority. Conflicting
Tool requirements are rejected as `agent_route_requirements_invalid`.
`preferred_capabilities` is deliberately soft and affects ranking only.

If every candidate is excluded, the Router returns
`agent_route_no_eligible_candidate`, records a failed Router Stage, emits
`agent.selection.decided`, and does not create a Child Run.

## Ranking And Fallback

`query_match` is the deterministic baseline. Its v3 policy reads declarative
signals owned by each frozen Agent profile: `capabilities`, `task_examples`,
`exclusions`, Tool names, and low-weight name/description terms. Capability and
Tool matches add strong evidence, example overlap adds supporting evidence, and
soft preferred-capability matches add low-weight evidence. Exclusions subtract
evidence. Stable score and Agent-name ordering makes the
same frozen inputs produce the same decision. The policy does not inspect the
System Prompt and has no central task-to-Agent keyword table.

After ranking, v3 applies an explicit abstention gate. It rejects a proposed
candidate when its score, first-to-second margin, LLM confidence, or verified
hard-requirement coverage is below the policy threshold. The decision retains
the proposed Agent, observed values, thresholds, threshold source, and stable
reason codes, but clears the selected Agent so no Child Run can be created.

The versioned routing Dataset v1 recommends a minimum score/margin of `4/1`
after calibration and holdout. Production intentionally remains on the stricter
`conservative-safety-baseline-v1` values of `6/1`: the 16-case offline fixture
is regression evidence, not enough evidence to publish a broader production
policy. Decision evidence reports the active threshold source verbatim.

If every eligible candidate has a zero score, the compatibility v2 policy returns
`agent_route_no_suitable_candidate` instead of selecting an arbitrary Worker.
This is separate from `agent_route_no_eligible_candidate`: an eligible Agent is
executable, while a suitable Agent has positive evidence for this task.

`auto` asks the configured model to rank only eligible candidates using the
task, approved plan, and frozen profile descriptions. The response must score
every candidate exactly once, select a highest-scored candidate, use scores
from 0 to 100, and use confidence from 0 to 1. Unknown IDs, duplicates,
omissions, and inconsistent scores are rejected locally before delegation.

Fallback is intentionally narrow:

| Router result | Behavior |
| --- | --- |
| Router model intentionally not configured for the deployment, timeout, rate limit, temporary provider failure, or invalid structured response | Use `query_match` and record `fallback_reason_code` |
| Cancellation, Run Budget exhaustion, provider authentication, quota, route identity/configuration, or content-policy failure | Return the original typed error; do not delegate |
| No eligible candidate | Return a typed 422-class error in the continuation stream; fail the Run without a Child Run |
| No candidate with positive routing evidence | Return `agent_route_no_suitable_candidate`; fail the Run without a Child Run |

The Router model call remains a normal Turn Engine request with
`UsagePurposeRouter`, so existing request limits, Run Budget, Usage Ledger,
request capture, retry policy, and tracing remain the single owners of those
concerns.

## Frozen And Observable Decisions

New Multi Runs freeze `agent-selection-v3` and all candidate routing hints in
Runtime Snapshot v15. Snapshot v14 resumes with `agent-selection-v2`, preserving
its zero-evidence behavior without applying newer thresholds. A v14 Run rejects
non-empty routing requirements instead of silently applying only part of the
new protocol. Older snapshots
remain available for Replay but are not resumable. Configuration changes or
newly-created Agent profiles cannot enter a frozen candidate set. The approved
requirements belong to that continuation decision and are persisted in its
`agent.selection.decided` event; recovery after selection reuses the persisted
Router Step instead of selecting again.

The Router Collaboration Step remains the human-readable trace. The durable
`agent.selection.decided` adds policy revision, outcome, mode, requirements,
selected and proposed Agent IDs, fallback code, candidate scores, requirement
coverage, exclusion reasons, threshold observations, and abstention reason codes
for Replay and evaluation.

The offline routing gate reuses the production eligibility, ranking, response
validation, fallback, and abstention code. On Dataset v1, v3 records 10/10
acceptable selections, zero unsafe false routes, and 6/6 no-route recall;
v1/v2 remain diagnostic migration baselines. These numbers do not claim live
LLM quality. See [Offline evaluation](../operations/offline-evaluation.md#agent-routing-gate)
for the dataset boundary, commands, and full metrics.

## Design References

The policy follows the common code-first routing shape used by current agent
frameworks: narrow candidates before model selection, validate structured
handoff output before side effects, keep deterministic routing available, and
bound delegated work.

- [OpenAI Agents SDK handoffs](https://openai.github.io/openai-agents-python/handoffs/)
- [OpenAI Agents SDK orchestration](https://openai.github.io/openai-agents-python/multi_agent/)
- [AutoGen SelectorGroupChat](https://microsoft.github.io/autogen/dev/user-guide/agentchat-user-guide/selector-group-chat.html)
- [Anthropic multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- [Semantic Kernel agent orchestration](https://learn.microsoft.com/en-us/semantic-kernel/frameworks/agent/agent-orchestration/)

This implementation deliberately does not add dynamic Agent discovery,
recursive delegation, load-aware scheduling, a capability ontology, or a
general-purpose Agent Gateway. A new production threshold requires reviewed
live-model evidence and the H-35 policy lifecycle; semantic candidate retrieval
remains conditional on candidate scale or measured recall failure.
