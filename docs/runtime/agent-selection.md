# Capability-aware Agent Selection

Multi mode routes an approved plan with a two-phase policy:

```text
frozen candidates -> deterministic eligibility -> deterministic or LLM ranking -> bounded Child Run
```

## Eligibility

Eligibility is a local, fail-closed check. A candidate must have a non-empty,
unique frozen Agent ID, and every Tool declared by its frozen profile must be
present in the Run's restored Tool Catalog. Natural-language keywords are not
treated as authorization or hard capability requirements. Tool invocation
remains governed by the existing Tool Security Policy and Tool Executor.

If every candidate is excluded, the Router returns
`agent_route_no_eligible_candidate`, records a failed Router Stage, emits
`agent.selection.decided`, and does not create a Child Run.

## Ranking And Fallback

`query_match` is the deterministic baseline. Its v2 policy reads declarative
signals owned by each frozen Agent profile: `capabilities`, `task_examples`,
`exclusions`, Tool names, and low-weight name/description terms. Capability and
Tool matches add strong evidence, example overlap adds supporting evidence, and
exclusions subtract evidence. Stable score and Agent-name ordering makes the
same frozen inputs produce the same decision. The policy does not inspect the
System Prompt and has no central task-to-Agent keyword table.

If every eligible candidate has a zero score, v2 returns
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

New Multi Runs freeze `agent-selection-v2` and all candidate routing hints in
Runtime Snapshot v14. Snapshot v13 resumes with the isolated
`agent-selection-v1` central-keyword fallback, preserving its historical
behavior without letting v1 rules leak into new Runs. Configuration changes or
newly-created Agent profiles cannot enter either frozen candidate set. Once v13
Resume support expires, v1 can be removed as one self-contained implementation.

The Router Collaboration Step remains the human-readable trace. The durable
`agent.selection.decided` event adds policy revision, outcome, mode, selected
Agent, fallback code, candidate scores, and exclusion reason codes for Replay
and evaluation. Recovery after Worker execution reuses the persisted Router
Step rather than selecting a new Agent.

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
recursive delegation, load-aware scheduling, or a general-purpose Agent
Gateway. The v2 weights are deterministic policy constants, not calibrated
quality claims; score thresholds, confidence margins, and semantic retrieval
belong to the routing evaluation and calibration backlog.
