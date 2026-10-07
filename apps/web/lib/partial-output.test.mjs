import assert from "node:assert/strict";
import test from "node:test";
import { observeRunEvents } from "./run-stream.ts";

test("reconnect delivers committed replacements once and resumes at its durable cursor", async t => {
  const original = globalThis.fetch;
  t.after(() => { globalThis.fetch = original; });
  const calls = [];
  const payload = { run_id: "r", turn_id: "t", channel: "answer", round: 1,
    revision: 1, offset: 5, text: "first", status: "provisional" };
  const checkpoint = (sequence, text) => `id: ${sequence}\ndata: ${JSON.stringify({ schema_version: 1,
    type: "model.output_checkpoint", run_id: "r", turn_id: "t", sequence,
    payload: { ...payload, text, offset: text.length, revision: sequence } })}\n\n`;
  const snapshot = (sequence, status) => `event: run.snapshot\nid: ${sequence}\ndata: ${JSON.stringify({
    run: { run_id: "r", status }, as_of_sequence: sequence, partial_outputs: [payload] })}\n\n`;
  globalThis.fetch = async url => {
    calls.push(url);
    return new Response(calls.length === 1
      ? checkpoint(1, "first") + snapshot(1, "running")
      : checkpoint(1, "first") + snapshot(1, "running") + checkpoint(2, "replacement") +
        'id: 3\ndata: {"schema_version":1,"type":"run.completed","run_id":"r","payload":{"status":"completed"}}\n\n');
  };
  const received = [];
  await observeRunEvents("r", { onEvent() {}, onSnapshot() {}, onOutput: output => received.push(output) });
  assert.equal(calls.length, 2);
  assert.match(calls[1], /after=1$/);
  assert.deepEqual(received.map(item => [item.sequence, item.text]), [[1, "first"], [2, "replacement"]]);
});
