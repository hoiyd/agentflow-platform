import assert from "node:assert/strict";
import test from "node:test";
import { blocksClarification, blocksToolApproval, selectRunInterrupt } from "./run-interrupts.ts";

// Failure inventory: historical approvals cannot hide a newer clarification;
// consumed approval blocks only recoverable Runs; same-time approval wins.
test("blocking statuses retain the two protocols' distinct recovery rules", () => {
  for (const status of ["pending", "answered", "expired"]) assert.equal(blocksClarification({ status }), true);
  for (const status of ["applied", "canceled"]) assert.equal(blocksClarification({ status }), false);
  for (const status of ["pending", "approved", "expired", "claimed"]) assert.equal(blocksToolApproval({ status }, "waiting_for_user"), true);
  assert.equal(blocksToolApproval({ status: "consumed" }, "waiting_for_user"), false);
  assert.equal(blocksToolApproval({ status: "consumed" }, "failed_recoverable"), true);
  for (const status of ["rejected", "revoked", "canceled"]) assert.equal(blocksToolApproval({ status }, "failed_recoverable"), false);
});

test("Chat, Replay and command admission share the current interrupt selection", () => {
  const older = "2026-11-17T00:00:00Z", newer = "2026-11-17T01:00:00Z";
  const approval = { status: "consumed", created_at: older };
  const question = { status: "pending", created_at: newer };
  assert.deepEqual(selectRunInterrupt([], [], "running"), { kind: null, approvalIsLatest: true });
  assert.equal(selectRunInterrupt([], [approval], "failed_recoverable").kind, "tool_approval");
  assert.equal(selectRunInterrupt([question], [approval], "failed_recoverable").kind, "clarification");
  assert.deepEqual(selectRunInterrupt([{ ...question, status: "applied" }], [approval], "failed_recoverable"), { kind: null, approvalIsLatest: false });
  assert.equal(selectRunInterrupt([question], [{ ...approval, created_at: newer }], "failed_recoverable").kind, "tool_approval");
  assert.equal(selectRunInterrupt([question], [{ ...approval, status: "approved", created_at: newer }], "waiting_for_user").kind, "tool_approval");
});
