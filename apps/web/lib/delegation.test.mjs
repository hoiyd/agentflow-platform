import assert from "node:assert/strict";
import test from "node:test";

import { delegationBlockReasonLabel, delegationStatusLabel } from "./delegation.ts";

test("delegation display keeps status and blocked reason distinct", () => {
  assert.equal(delegationStatusLabel("blocked"), "Blocked");
  assert.equal(delegationBlockReasonLabel("child_recovery_required", "parent"), "Child run requires recovery");
  assert.equal(
    delegationBlockReasonLabel("child_recovery_required", "child"),
    "This run requires recovery before its parent can continue"
  );
});

test("delegation display safely humanizes future protocol values", () => {
  assert.equal(delegationStatusLabel("awaiting_capacity"), "Awaiting capacity");
  assert.equal(delegationBlockReasonLabel("", "parent"), "Unknown");
});
