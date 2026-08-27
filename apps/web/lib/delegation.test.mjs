import assert from "node:assert/strict";
import test from "node:test";

import { delegationBlockReasonLabel, delegationStatusLabel } from "./delegation.ts";

test("delegation display keeps status and recovery reason distinct", () => {
  assert.equal(delegationStatusLabel("blocked"), "Blocked");
  assert.equal(delegationBlockReasonLabel("child_recovery_required"), "Child recovery required");
});

test("delegation display safely humanizes future protocol values", () => {
  assert.equal(delegationStatusLabel("awaiting_capacity"), "Awaiting capacity");
  assert.equal(delegationBlockReasonLabel(""), "Unknown");
});
