import assert from "node:assert/strict";
import test from "node:test";

import { compareEvidence, evidenceDelta } from "./evidence-comparison.ts";

function bundle(overrides = {}) {
  const snapshot = overrides.snapshot === undefined ? {
    schema_version: 12,
    mode: "single",
    agent: { id: "agent-1", system_prompt: "Be precise" },
    model: { provider: "openai", model: "gpt-test" },
    tools: [{ name: "get_current_time" }],
    context_assembly: { history_max_tokens: 2000 },
    run_budget: { max_total_tokens: 4000 },
    tool_security_policy: { default_action: "allow" },
    tool_progress_guard: { enabled: true },
    created_at: overrides.createdAt ?? "2026-09-01T00:00:00Z"
  } : overrides.snapshot;
  return {
    replay: {
      run: { id: overrides.runId ?? "run-current", status: overrides.status ?? "completed" },
      runtime_snapshot: snapshot,
      run_events: overrides.events ?? [],
      tool_artifacts: overrides.toolArtifacts ?? []
    },
    report: {
      task: overrides.task ?? "Explain the refund policy",
      final_output: overrides.output ?? "Refunds are available for 30 days.",
      agent: { id: "agent-1" },
      messages: overrides.messages ?? [],
      retrievals: {
        memories: [],
        chunks: overrides.chunks ?? [{ document_id: "policy-v2", chunk_id: "refund-window" }]
      },
      llm_calls: [{ model: "gpt-test" }],
      trace_summary: {
        total_tokens: overrides.tokens ?? 100,
        llm_calls: 1,
        tool_calls: 0,
        total_duration_ms: overrides.duration ?? 500,
        error_count: overrides.errors ?? 0
      },
      verification: {
        status: overrides.verification ?? "passed",
        artifacts: overrides.verificationArtifacts ?? []
      }
    },
    modelRequests: {
      records: [{ envelope: {
        id: "request-1",
        runtime_snapshot_hash: overrides.hash ?? "sha256:runtime",
        context_manifest_id: "context-1"
      }}]
    }
  };
}

test("identical runtime inputs remain comparable despite snapshot timestamps", () => {
  const baseline = bundle({ runId: "run-baseline", tokens: 120, duration: 700, createdAt: "2026-08-01T00:00:00Z" });
  const current = bundle({ tokens: 90, duration: 450, createdAt: "2026-09-01T00:00:00Z" });

  const comparison = compareEvidence(current, baseline);

  assert.equal(comparison.comparable, true);
  assert.equal(comparison.mode, "identical");
  assert.equal(evidenceDelta(comparison, "totalTokens"), -30);
  assert.equal(evidenceDelta(comparison, "durationMS"), -250);
  assert.deepEqual(comparison.current.evidenceRefs, ["context-manifest:context-1", "model-request:request-1"]);
});

test("one predeclared runtime dimension is accepted as a single-variable comparison", () => {
  const baseline = bundle({ runId: "run-baseline" });
  const currentSnapshot = structuredClone(baseline.replay.runtime_snapshot);
  currentSnapshot.context_assembly.history_max_tokens = 1000;

  const comparison = compareEvidence(bundle({ snapshot: currentSnapshot }), baseline);

  assert.equal(comparison.comparable, true);
  assert.equal(comparison.mode, "single_variable");
  assert.equal(comparison.changedVariable, "Context assembly");
});

test("identity drift stays side-by-side and suppresses deltas", () => {
  const baseline = bundle({ runId: "run-baseline" });
  const currentSnapshot = structuredClone(baseline.replay.runtime_snapshot);
  currentSnapshot.model.model = "other-model";
  currentSnapshot.tools = [];

  const comparison = compareEvidence(bundle({ task: "A different task", snapshot: currentSnapshot }), baseline);

  assert.equal(comparison.comparable, false);
  assert.equal(comparison.mode, "side_by_side");
  assert.match(comparison.reasons.join("; "), /Task identity differs/);
  assert.match(comparison.reasons.join("; "), /Multiple experiment variables differ/);
  assert.equal(evidenceDelta(comparison, "totalTokens"), null);
});

test("different retrieved material cannot produce an improvement delta", () => {
  const comparison = compareEvidence(
    bundle({ chunks: [{ document_id: "policy-v2", chunk_id: "shipping-window" }] }),
    bundle({ runId: "run-baseline" })
  );

  assert.equal(comparison.comparable, false);
  assert.match(comparison.reasons.join("; "), /Material identity differs/);
  assert.equal(evidenceDelta(comparison, "durationMS"), null);
});

test("ephemeral citation labels do not change durable material identity", () => {
  const comparison = compareEvidence(
    bundle({ chunks: [{ source_id: "S9", document_id: "policy-v2", document_version: "2", chunk_id: "refund-window" }] }),
    bundle({ runId: "run-baseline", chunks: [{ source_id: "S1", document_id: "policy-v2", document_version: "2", chunk_id: "refund-window" }] })
  );

  assert.equal(comparison.comparable, true);
});

test("missing identity and canceled outcomes remain explicit", () => {
  const comparison = compareEvidence(
    bundle({ task: " ", snapshot: null, status: "canceled", verification: "blocked", events: [{ payload: { stop_reason: "user_cancelled" } }] }),
    bundle({ runId: "run-baseline" })
  );

  assert.equal(comparison.comparable, false);
  assert.equal(comparison.current.identity.task, null);
  assert.equal(comparison.current.status, "canceled");
  assert.equal(comparison.current.stopReason, "user_cancelled");
  assert.match(comparison.reasons.join("; "), /Task identity is unknown/);
  assert.match(comparison.reasons.join("; "), /Runtime snapshot is unavailable/);
});
