import { expectObject, isObject, numberValue, stringValue } from "./api-client.ts";
import type {
  ContractSchemas, Conversation, EpisodeReport, RecoverySummary, RunInfo, RunProjectionSnapshot, RunReplay, RunTraceSummary,
  RuntimeInvariantFailure, RunUsageLedger, RunUsageTotals, ToolArtifact, ToolEffect, SkillEvidence
} from "./api-types.ts";

export function normalizeEpisodeReport(value: unknown): EpisodeReport {
  const report = expectObject<ContractSchemas["EpisodeReport"]>(value, "episode report");
  const retrievals = expectObject<ContractSchemas["EpisodeRetrievals"]>(report.retrievals, "episode retrievals");
  const verification = expectObject<ContractSchemas["EpisodeVerification"]>(report.verification, "episode verification");
  return {
    ...report,
    messages: report.messages ?? [], steps: report.steps ?? [],
    llm_calls: report.llm_calls ?? [], tool_calls: report.tool_calls ?? [], errors: report.errors ?? [],
    retrievals: { ...retrievals, memories: retrievals.memories ?? [], chunks: retrievals.chunks ?? [] },
    verification: { ...verification, evidence: verification.evidence ?? [], warnings: verification.warnings ?? [],
      records: verification.records ?? [], artifacts: verification.artifacts ?? [] }
  };
}

export function normalizeRunReplay(data: unknown): RunReplay {
  const replay = expectObject<Record<string, unknown>>(data, "run replay");
  const run = expectRunInfo(replay.run);
  return {
    run,
    projection: normalizeRunProjection(replay.projection, run, replay.summary, replay.usage_ledger),
    runtime_snapshot: isObject(replay.runtime_snapshot) ? replay.runtime_snapshot : undefined,
    conversation: expectConversation(replay.conversation),
    summary: expectRunTraceSummary(replay.summary),
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    usage_ledger: normalizeRunUsageLedger(replay.usage_ledger, run?.id ?? ""),
    run_events: Array.isArray(replay.run_events) ? replay.run_events : [],
    stage_checkpoints: Array.isArray(replay.stage_checkpoints) ? replay.stage_checkpoints : [],
		tool_effects: Array.isArray(replay.tool_effects) ? replay.tool_effects as ToolEffect[] : [],
		tool_artifacts: Array.isArray(replay.tool_artifacts) ? replay.tool_artifacts as ToolArtifact[] : [],
    verification_evidence: Array.isArray(replay.verification_evidence) ? replay.verification_evidence : [],
    verification_artifacts: Array.isArray(replay.verification_artifacts) ? replay.verification_artifacts : [],
    task_state_revisions: Array.isArray(replay.task_state_revisions) ? replay.task_state_revisions : [],
    recovery_summary: normalizeRecoverySummary(replay.recovery_summary)
  };
}

function normalizeRecoverySummary(value: unknown): RecoverySummary | undefined {
  if (!isObject(value)) return undefined;
  const strings = (input: unknown) => Array.isArray(input)
    ? input.filter((item): item is string => typeof item === "string")
    : [];
  return {
    reason: stringValue(value.reason) ?? "",
    title: stringValue(value.title) ?? "",
    message: stringValue(value.message) ?? "",
    evidence: Array.isArray(value.evidence)
      ? value.evidence.filter(isObject).map((item) => ({
          kind: stringValue(item.kind) ?? "unknown",
          summary: stringValue(item.summary) ?? "",
          id: stringValue(item.id),
          status: stringValue(item.status),
          artifact_refs: strings(item.artifact_refs)
        }))
      : [],
    artifact_refs: strings(value.artifact_refs),
    actions: Array.isArray(value.actions)
      ? value.actions.filter(isObject).map((item) => ({
          kind: stringValue(item.kind) ?? "unknown",
          label: stringValue(item.label) ?? "Unknown action",
          enabled: item.enabled === true,
          target_id: stringValue(item.target_id),
          unavailable_reason: stringValue(item.unavailable_reason)
        }))
      : []
  };
}

export function normalizeRunProjection(value: unknown, run: RunInfo, summaryValue: unknown, ledgerValue: unknown): RunProjectionSnapshot {
  const projection = isObject(value) ? value : {};
  const runProjection = isObject(projection.run) ? projection.run : {};
  const usage = isObject(projection.usage) ? projection.usage : {};
  const verification = isObject(projection.verification) ? projection.verification : {};
  const watermark = numberValue(projection.as_of_sequence) ?? 0;
  const strings = (input: unknown): string[] => Array.isArray(input) ? input.filter((item): item is string => typeof item === "string") : [];
  return {
    run: {
      run_id: stringValue(runProjection.run_id) ?? run.id,
      conversation_id: stringValue(runProjection.conversation_id) ?? run.conversation_id,
      status: stringValue(runProjection.status) ?? run.status,
      verification_status: stringValue(runProjection.verification_status) ?? run.verification_status ?? "not_required",
      active_stage_ids: strings(runProjection.active_stage_ids),
      active_turn_ids: strings(runProjection.active_turn_ids),
      active_model_call_ids: strings(runProjection.active_model_call_ids),
      active_tool_call_ids: strings(runProjection.active_tool_call_ids),
      summary: expectRunTraceSummary(runProjection.summary ?? summaryValue),
      as_of_sequence: numberValue(runProjection.as_of_sequence) ?? watermark
    },
    usage: {
      ledger: normalizeRunUsageLedger(usage.ledger ?? ledgerValue, run.id),
      as_of_sequence: numberValue(usage.as_of_sequence) ?? watermark
    },
    verification: {
      status: stringValue(verification.status) ?? run.verification_status ?? "not_required",
      latest_attempt: numberValue(verification.latest_attempt) ?? 0,
      current_subject_hash: stringValue(verification.current_subject_hash),
      evidence_count: numberValue(verification.evidence_count) ?? 0,
      fresh_evidence_count: numberValue(verification.fresh_evidence_count) ?? 0,
      as_of_sequence: numberValue(verification.as_of_sequence) ?? watermark
    },
    as_of_sequence: watermark,
    partial_outputs: Array.isArray(projection.partial_outputs) ? projection.partial_outputs : [],
    tool_progress: Array.isArray(projection.tool_progress) ? projection.tool_progress : [],
    skill_evidence: normalizeSkillEvidence(projection.skill_evidence),
    invariant_failures: Array.isArray(projection.invariant_failures)
      ? projection.invariant_failures.filter((item): item is RuntimeInvariantFailure => isObject(item) && typeof item.code === "string")
      : []
  };
}

function normalizeSkillEvidence(value: unknown): SkillEvidence[] {
  if (!Array.isArray(value)) return [];
  return value.filter(isObject).filter(item => typeof item.name === "string" && typeof item.hash === "string" && typeof item.agent_id === "string").map(item => ({
    name: item.name as string, hash: item.hash as string, agent_id: item.agent_id as string,
    stage_id: stringValue(item.stage_id), bound: item.bound === true,
    instructions: item.instructions === "included" ? "included" : "not_observed",
    activation: item.activation === "explicit" || item.activation === "model" ? item.activation : "not_observed",
    first_sequence: numberValue(item.first_sequence), manifest_id: stringValue(item.manifest_id),
    first_request_sequence: numberValue(item.first_request_sequence),
    event_id: stringValue(item.event_id), request_id: stringValue(item.request_id), estimated_tokens: numberValue(item.estimated_tokens),
    resources: Array.isArray(item.resources) ? item.resources.filter(isObject).filter(resource => typeof resource.path === "string" && typeof resource.hash === "string" && typeof resource.event_id === "string").map(resource => ({
      path: resource.path as string, hash: resource.hash as string, event_id: resource.event_id as string,
      offset: numberValue(resource.offset) ?? 0, next_offset: numberValue(resource.next_offset) ?? 0,
      total_bytes: numberValue(resource.total_bytes) ?? 0, sequence: numberValue(resource.sequence) ?? 0
    })) : [],
    failures: Array.isArray(item.failures) ? item.failures.filter(isObject).filter(failure => typeof failure.tool === "string" && typeof failure.code === "string" && typeof failure.event_id === "string").map(failure => ({
      tool: failure.tool as string, code: failure.code as string, event_id: failure.event_id as string,
      path: stringValue(failure.path), sequence: numberValue(failure.sequence) ?? 0
    })) : []
  }));
}

const EMPTY_RUN_USAGE_TOTALS: RunUsageTotals = {
  model_calls: 0,
  tool_calls: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  estimated_cost_micros: 0,
  open_reservations: 0,
  cost_unknown_entries: 0
};

export function normalizeRunUsageLedger(value: unknown, runId: string): RunUsageLedger {
  const ledger = isObject(value) ? value : {};
  return {
    run_id: typeof ledger.run_id === "string" ? ledger.run_id : runId,
    budget: isObject(ledger.budget) ? ledger.budget : {},
    totals: {
      ...EMPTY_RUN_USAGE_TOTALS,
      ...(isObject(ledger.totals) ? ledger.totals : {})
    },
    entries: Array.isArray(ledger.entries) ? ledger.entries : [],
    ...(typeof ledger.updated_at === "string" ? { updated_at: ledger.updated_at } : {})
  };
}

function expectRunInfo(value: unknown): RunInfo {
  const run = expectObject<Record<string, unknown>>(value, "run replay run");
  requireStringFields(run, "run replay run", ["id", "agent_id", "conversation_id", "status"]);
  return run as unknown as RunInfo;
}

function expectConversation(value: unknown): Conversation {
  const conversation = expectObject<Record<string, unknown>>(value, "run replay conversation");
  requireStringFields(conversation, "run replay conversation", ["id", "title"]);
  return conversation as unknown as Conversation;
}

function expectRunTraceSummary(value: unknown): RunTraceSummary {
  const summary = expectObject<Record<string, unknown>>(value, "run replay summary");
  requireStringFields(summary, "run replay summary", ["run_id"]);
  return summary as unknown as RunTraceSummary;
}

function requireStringFields(value: Record<string, unknown>, responseName: string, fields: string[]) {
  for (const field of fields) {
    if (typeof value[field] !== "string" || value[field] === "") {
      throw new Error(`Invalid ${responseName} response: ${field} is required`);
    }
  }
}
