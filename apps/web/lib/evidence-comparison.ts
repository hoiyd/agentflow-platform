import type { EpisodeReport, ModelRequestDebugResponse, RunReplay } from "./api";

export type EvidenceBundle = {
  replay: RunReplay;
  report: EpisodeReport;
  modelRequests: ModelRequestDebugResponse;
};

export type EvidenceIdentity = {
  task: string | null;
  materials: string[] | null;
  mode: string | null;
  agent: string | null;
  model: string | null;
  configHash: string | null;
  experimentId: string | null;
};

export type EvidenceSide = {
  runId: string;
  status: string | null;
  verification: string | null;
  stopReason: string | null;
  finalOutput: string | null;
  identity: EvidenceIdentity;
  evidenceRefs: string[];
  metrics: {
    totalTokens: number | null;
    modelCalls: number | null;
    toolCalls: number | null;
    durationMS: number | null;
    errors: number | null;
  };
};

export type EvidenceComparison = {
  comparable: boolean;
  mode: "identical" | "single_variable" | "side_by_side";
  changedVariable?: string;
  reasons: string[];
  baseline: EvidenceSide;
  current: EvidenceSide;
};

export type EvidenceMetric = keyof EvidenceSide["metrics"];

const experimentDimensions = [
  { key: "agent", label: "Agent definition", fields: ["agent", "candidate_agents"] },
  { key: "model", label: "Model", fields: ["model"] },
  { key: "tools", label: "Tool set", fields: ["tools"] },
  { key: "context_assembly", label: "Context assembly", fields: ["context_assembly"] },
  { key: "run_budget", label: "Run budget", fields: ["run_budget"] },
  { key: "tool_governance", label: "Tool governance", fields: ["tool_security_policy", "tool_progress_guard"] },
  { key: "orchestration", label: "Orchestration limits", fields: ["router_mode", "autonomous_limits", "child_run_policy"] }
] as const;

const ownedSnapshotFields = new Set([
  "schema_version", "mode", "created_at", "delegation",
  ...experimentDimensions.flatMap((dimension) => dimension.fields)
]);

export function compareEvidence(currentBundle: EvidenceBundle, baselineBundle: EvidenceBundle): EvidenceComparison {
  const current = evidenceSide(currentBundle);
  const baseline = evidenceSide(baselineBundle);
  const reasons: string[] = [];

  compareRequiredIdentity("Task identity", current.identity.task, baseline.identity.task, reasons);
  compareRequiredIdentity("Material identity", current.identity.materials, baseline.identity.materials, reasons);

  const currentSnapshot = currentBundle.replay.runtime_snapshot;
  const baselineSnapshot = baselineBundle.replay.runtime_snapshot;
  if (!currentSnapshot || !baselineSnapshot) {
    reasons.push("Runtime snapshot is unavailable");
  }

  const changed: Array<{ key: string; label: string }> = [];
  if (currentSnapshot && baselineSnapshot) {
    compareRequiredIdentity("Snapshot schema", numberOrNull(currentSnapshot.schema_version), numberOrNull(baselineSnapshot.schema_version), reasons);
    compareRequiredIdentity("Execution mode", stringOrNull(currentSnapshot.mode), stringOrNull(baselineSnapshot.mode), reasons);
    if (!sameValue(currentSnapshot.delegation, baselineSnapshot.delegation)) {
      reasons.push("Delegation identity differs");
    }
    for (const dimension of experimentDimensions) {
      if (!sameValue(selectFields(currentSnapshot, dimension.fields), selectFields(baselineSnapshot, dimension.fields))) {
        changed.push(dimension);
      }
    }
    if (!sameValue(unownedFields(currentSnapshot), unownedFields(baselineSnapshot))) {
      reasons.push("Unrecognized runtime configuration differs");
    }
  }

  if (changed.length > 1) {
    reasons.push(`Multiple experiment variables differ: ${changed.map((item) => item.label).join(", ")}`);
  }
  const comparable = reasons.length === 0 && changed.length <= 1;
  return {
    comparable,
    mode: comparable ? (changed.length === 1 ? "single_variable" : "identical") : "side_by_side",
    changedVariable: comparable && changed.length === 1 ? changed[0].label : undefined,
    reasons,
    baseline,
    current
  };
}

export function evidenceDelta(comparison: EvidenceComparison, metric: EvidenceMetric): number | null {
  const current = comparison.current.metrics[metric];
  const baseline = comparison.baseline.metrics[metric];
  return comparison.comparable && current !== null && baseline !== null ? current - baseline : null;
}

function evidenceSide(bundle: EvidenceBundle): EvidenceSide {
  const { replay, report, modelRequests } = bundle;
  return {
    runId: replay.run.id,
    status: stringOrNull(replay.run.status),
    verification: stringOrNull(report.verification.status),
    stopReason: stopReason(replay),
    finalOutput: stringOrNull(report.final_output),
    identity: {
      task: normalizedText(report.task),
      materials: materialIdentity(report),
      mode: stringOrNull(replay.runtime_snapshot?.mode),
      agent: agentIdentity(replay, report),
      model: modelIdentity(replay, report),
      configHash: runtimeSnapshotHash(modelRequests),
      experimentId: null
    },
    evidenceRefs: evidenceReferences(bundle),
    metrics: {
      totalTokens: finiteNumber(report.trace_summary.total_tokens),
      modelCalls: finiteNumber(report.trace_summary.llm_calls),
      toolCalls: finiteNumber(report.trace_summary.tool_calls),
      durationMS: finiteNumber(report.trace_summary.total_duration_ms),
      errors: finiteNumber(report.trace_summary.error_count)
    }
  };
}

function compareRequiredIdentity(label: string, current: unknown, baseline: unknown, reasons: string[]) {
  if (current === null || baseline === null) {
    reasons.push(`${label} is unknown`);
  } else if (!sameValue(current, baseline)) {
    reasons.push(`${label} differs`);
  }
}

function materialIdentity(report: EpisodeReport): string[] | null {
  const items = [...report.retrievals.memories, ...report.retrievals.chunks];
  if (items.length === 0) return [];
  const refs = items.map(materialReference);
  return refs.some((item) => item === null) ? null : [...new Set(refs as string[])].sort();
}

function materialReference(item: Record<string, unknown>): string | null {
  const parts: string[] = [];
  for (const key of ["document_id", "document_version", "chunk_id", "id", "reference_id"]) {
    const value = stringOrNull(item[key]);
    if (value) parts.push(`${key}:${value}`);
  }
  const sourceChunks = Array.isArray(item.source_chunk_ids)
    ? item.source_chunk_ids.filter((value): value is string => typeof value === "string" && value !== "").sort()
    : [];
  if (sourceChunks.length > 0) parts.push(`source_chunk_ids:${sourceChunks.join(",")}`);
  if (parts.length > 0) return parts.join("|");
  const sourceID = stringOrNull(item.source_id);
  return sourceID ? `source_id:${sourceID}` : null;
}

function agentIdentity(replay: RunReplay, report: EpisodeReport): string | null {
  const agent = recordOrNull(replay.runtime_snapshot?.agent);
  return stringOrNull(agent?.id) ?? stringOrNull(report.agent.id);
}

function modelIdentity(replay: RunReplay, report: EpisodeReport): string | null {
  const model = recordOrNull(replay.runtime_snapshot?.model);
  const provider = stringOrNull(model?.provider);
  const name = stringOrNull(model?.model) ?? report.llm_calls.map((call) => stringOrNull(call.model)).find(Boolean) ?? null;
  return name ? [provider, name].filter(Boolean).join(" / ") : null;
}

function runtimeSnapshotHash(requests: ModelRequestDebugResponse): string | null {
  const hashes = [...new Set(requests.records
    .map((record) => stringOrNull(record.envelope.runtime_snapshot_hash))
    .filter((value): value is string => value !== null))];
  return hashes.length === 1 ? hashes[0] : null;
}

function evidenceReferences(bundle: EvidenceBundle): string[] {
  const refs = new Set<string>();
  for (const record of bundle.modelRequests.records) {
    if (record.envelope.id) refs.add(`model-request:${record.envelope.id}`);
    if (record.envelope.context_manifest_id) refs.add(`context-manifest:${record.envelope.context_manifest_id}`);
  }
  for (const artifact of bundle.replay.tool_artifacts) {
    if (artifact.id) refs.add(`tool-artifact:${artifact.id}`);
  }
  for (const artifact of bundle.report.verification.artifacts) {
    const id = stringOrNull(artifact.id);
    if (id) refs.add(`verification-artifact:${id}`);
  }
  for (const message of bundle.report.messages) {
    for (const citation of message.citations ?? []) {
      if (citation.source_id) refs.add(`source:${citation.source_id}`);
    }
  }
  return [...refs].sort();
}

function stopReason(replay: RunReplay): string | null {
  if (stringOrNull(replay.run.error)) return replay.run.error as string;
  for (let index = replay.run_events.length - 1; index >= 0; index--) {
    const payload = replay.run_events[index].payload;
    const reason = stringOrNull(payload.stop_reason) ?? stringOrNull(payload.reason);
    if (reason) return reason;
  }
  return stringOrNull(replay.run.status);
}

function selectFields(snapshot: Record<string, unknown>, fields: readonly string[]) {
  return Object.fromEntries(fields.map((field) => [field, snapshot[field] ?? null]));
}

function unownedFields(snapshot: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(snapshot).filter(([key]) => !ownedSnapshotFields.has(key)));
}

function sameValue(left: unknown, right: unknown) {
  return JSON.stringify(canonicalValue(left)) === JSON.stringify(canonicalValue(right));
}

function canonicalValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalValue);
  const record = recordOrNull(value);
  if (!record) return value ?? null;
  return Object.fromEntries(Object.keys(record).sort().map((key) => [key, canonicalValue(record[key])]));
}

function recordOrNull(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function normalizedText(value: unknown): string | null {
  const text = stringOrNull(value);
  return text ? text.replace(/\s+/g, " ") : null;
}

function stringOrNull(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function numberOrNull(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function finiteNumber(value: unknown): number | null {
  return numberOrNull(value);
}
