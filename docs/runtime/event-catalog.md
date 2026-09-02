# Run Event Catalog

This file is generated from `apps/api/internal/eventcatalog`. Producers must use a registered event and typed payload contract; durable stores reject live-only and unregistered events.

| Event | Durability | Schema | Scope | Producer | Payload schema | Lifecycle | Terminal for | Consumers |
| --- | --- | ---: | --- | --- | --- | --- | --- | --- |
| `artifact.expired` | durable | 1 | run | tools | `event.ToolArtifactPayload` | none | `` | artifact_governance, replay |
| `artifact.read` | durable | 1 | run | tools | `event.ToolArtifactPayload` | none | `` | artifact_governance, replay |
| `budget.exceeded` | durable | 1 | run | budget | `event.BudgetExceededPayload` | none | `` | run_projection, usage_ledger, replay |
| `checkpoint.captured` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | checkpoint:transition | `` | recovery, replay |
| `checkpoint.compensation_completed` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | compensation:terminal | `checkpoint.compensation_started` | recovery, replay |
| `checkpoint.compensation_failed` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | compensation:terminal | `checkpoint.compensation_started` | recovery, replay |
| `checkpoint.compensation_started` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | compensation:start | `` | recovery, replay |
| `checkpoint.restored` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | checkpoint:transition | `` | recovery, replay |
| `checkpoint.stale` | durable | 1 | run+stage | checkpoint/recovery | `event.TracePayload` | checkpoint:transition | `` | recovery, replay |
| `citation.resolved` | durable | 1 | run | agent/rag | `event.TracePayload` | none | `` | replay |
| `context.assembled` | durable | 1 | run | requestcapture/contextassembly | `event.ContextAssembledPayload` | none | `` | request_capture, replay |
| `context.compaction_completed` | durable | 1 | run | contextcompaction | `event.ContextCompactionPayload` | compaction:terminal | `context.compaction_started` | replay |
| `context.compaction_failed` | durable | 1 | run | contextcompaction | `event.ContextCompactionPayload` | compaction:terminal | `context.compaction_started` | run_projection, replay |
| `context.compaction_started` | durable | 1 | run | contextcompaction | `event.ContextCompactionPayload` | compaction:start | `` | replay |
| `delegation.blocked` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:transition | `` | delegation, recovery, replay |
| `delegation.canceled` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:terminal | `delegation.created` | delegation, replay |
| `delegation.completed` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:terminal | `delegation.created` | delegation, replay |
| `delegation.created` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:start | `` | delegation, replay |
| `delegation.failed` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:terminal | `delegation.created` | delegation, replay |
| `delegation.started` | durable | 1 | run+stage | delegation | `event.TracePayload` | delegation:transition | `` | delegation, replay |
| `memory.candidate.accepted` | durable | 1 | run | memory | `event.MemoryCandidatePayload` | memory_candidate:terminal | `memory.candidate.proposed` | replay |
| `memory.candidate.failed` | durable | 1 | run | memory | `event.MemoryCandidatePayload` | none | `` | run_projection, replay |
| `memory.candidate.proposed` | durable | 1 | run | memory | `event.MemoryCandidatePayload` | memory_candidate:start | `` | replay |
| `memory.candidate.rejected` | durable | 1 | run | memory | `event.MemoryCandidatePayload` | memory_candidate:terminal | `memory.candidate.proposed` | replay |
| `memory.recall.failed` | durable | 1 | run | memory | `event.TracePayload` | none | `` | run_projection, replay |
| `memory.sync.completed` | durable | 1 | run | memory | `event.TracePayload` | memory_sync:terminal | `memory.sync.requested` | replay |
| `memory.sync.failed` | durable | 1 | run | memory | `event.TracePayload` | memory_sync:terminal | `memory.sync.requested` | run_projection, replay |
| `memory.sync.rejected` | durable | 1 | run | memory | `event.TracePayload` | none | `` | run_projection, replay |
| `memory.sync.requested` | durable | 1 | run | memory | `event.TracePayload` | memory_sync:start | `` | replay |
| `model.completed` | durable | 1 | run | agent/turn | `event.ModelPayload` | model:terminal | `model.started` | run_projection, replay |
| `model.delta` | live | 1 | run | agent/turn | `event.ModelPayload` | none | `` | live_ui |
| `model.failed` | durable | 1 | run | agent/turn | `event.ModelPayload` | model:terminal | `model.started` | run_projection, replay |
| `model.request_prepared` | durable | 1 | run | requestcapture/contextassembly | `event.ModelRequestPreparedPayload` | model:transition | `` | request_capture, replay |
| `model.started` | durable | 1 | run | agent/turn | `event.ModelPayload` | model:start | `` | run_projection, request_capture, replay |
| `retrieval.completed` | durable | 1 | run | agent/rag | `event.RetrievalPayload` | retrieval:terminal | `retrieval.started` | replay |
| `retrieval.failed` | durable | 1 | run | agent/rag | `event.RetrievalPayload` | retrieval:terminal | `retrieval.started` | run_projection, replay |
| `retrieval.started` | durable | 1 | run | agent/rag | `event.RetrievalPayload` | retrieval:start | `` | replay |
| `run.cancel_requested` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:transition | `` | run_projection, replay |
| `run.canceled` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:terminal | `run.created` | run_projection, replay |
| `run.completed` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:terminal | `run.created` | run_projection, replay |
| `run.created` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:start | `` | run_projection, replay |
| `run.failed` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:terminal | `run.created` | run_projection, replay |
| `run.progress` | live | 1 | run | agent/httpapi | `event.RunProgressPayload` | none | `` | live_ui |
| `run.resumed` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:transition | `` | run_projection, replay |
| `run.revision_requested` | durable | 1 | run | verification | `event.TracePayload` | run:transition | `` | run_projection, replay |
| `run.started` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:transition | `` | run_projection, replay |
| `run.waiting_for_user` | durable | 1 | run | agent/httpapi | `event.RunStatusPayload` | run:transition | `` | run_projection, replay |
| `session_history.search_completed` | durable | 1 | run | sessionhistory | `event.SessionHistorySearchPayload` | history_search:terminal | `session_history.search_started` | replay |
| `session_history.search_failed` | durable | 1 | run | sessionhistory | `event.SessionHistorySearchPayload` | history_search:terminal | `session_history.search_started` | run_projection, replay |
| `session_history.search_started` | durable | 1 | run | sessionhistory | `event.SessionHistorySearchPayload` | history_search:start | `` | replay |
| `stage.canceled` | durable | 1 | run+stage | agent/turn | `event.StagePayload` | stage:terminal | `stage.started` | run_projection, replay |
| `stage.completed` | durable | 1 | run+stage | agent/turn | `event.StagePayload` | stage:terminal | `stage.started` | run_projection, replay |
| `stage.failed` | durable | 1 | run+stage | agent/turn | `event.StagePayload` | stage:terminal | `stage.started` | run_projection, replay |
| `stage.started` | durable | 1 | run+stage | agent/turn | `event.StagePayload` | stage:start | `` | run_projection, replay |
| `task_state.updated` | durable | 1 | run | taskstate | `event.TaskStatePayload` | none | `` | replay |
| `tool.completed` | durable | 1 | run | tools | `event.ToolPayload` | tool:terminal | `tool.started` | run_projection, replay |
| `tool.failed` | durable | 1 | run | tools | `event.ToolPayload` | tool:terminal | `tool.started` | run_projection, replay |
| `tool.guard.blocked` | durable | 1 | run | tools | `event.ToolProgressPayload` | none | `` | tool_progress_guard, replay |
| `tool.guard.warned` | durable | 1 | run | tools | `event.ToolProgressPayload` | none | `` | tool_progress_guard, replay |
| `tool.policy_evaluated` | durable | 1 | run | tools | `event.ToolPolicyPayload` | none | `` | tool_policy, replay |
| `tool.result.persisted` | durable | 1 | run | tools | `event.ToolArtifactPayload` | none | `` | artifact_governance, replay |
| `tool.started` | durable | 1 | run | tools | `event.ToolPayload` | tool:start | `` | run_projection, replay |
| `turn.canceled` | durable | 1 | run+turn | agent/turn | `event.TracePayload` | turn:terminal | `turn.started` | run_projection, replay |
| `turn.completed` | durable | 1 | run+turn | agent/turn | `event.TracePayload` | turn:terminal | `turn.started` | run_projection, replay |
| `turn.failed` | durable | 1 | run+turn | agent/turn | `event.TracePayload` | turn:terminal | `turn.started` | run_projection, replay |
| `turn.no_progress` | durable | 1 | run+turn | tools | `event.ToolProgressPayload` | turn:transition | `` | tool_progress_guard, replay |
| `turn.started` | durable | 1 | run+turn | agent/turn | `event.TracePayload` | turn:start | `` | run_projection, replay |
| `usage.recorded` | durable | 1 | run | budget | `event.UsagePayload` | none | `` | usage_ledger, replay |
| `verification.blocked` | durable | 1 | run | verification | `event.TracePayload` | verifier:terminal | `verification.started` | verification_evidence, replay |
| `verification.failed` | durable | 1 | run | verification | `event.TracePayload` | verifier:terminal | `verification.started` | verification_evidence, replay |
| `verification.passed` | durable | 1 | run | verification | `event.TracePayload` | verifier:terminal | `verification.started` | verification_evidence, replay |
| `verification.requested` | durable | 1 | run | verification | `event.TracePayload` | verification_attempt:start | `` | verification_evidence, replay |
| `verification.stale` | durable | 1 | run | verification | `event.TracePayload` | verification_attempt:transition | `` | verification_evidence, replay |
| `verification.started` | durable | 1 | run | verification | `event.TracePayload` | verifier:start | `` | verification_evidence, replay |
