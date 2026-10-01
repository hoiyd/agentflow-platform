import assert from "node:assert/strict";
import test from "node:test";
import { readChatEventStream } from "./run-stream.ts";

test("reasoning batches use bounded replacement text only through the live event", async () => {
  const events = [];
  const envelope = { schema_version: 1, run_id: "run-1", turn_id: "turn-1", payload: {
    model_call_id: "call-1", format: "deepseek_reasoning_content", status: "receiving", text: "Safe live prefix"
  } };
  for (const [type, payload] of [
    ["model.reasoning_delta", envelope.payload],
    ["model.reasoning", envelope.payload],
    ["model.reasoning_delta", { ...envelope.payload, status: "complete" }],
    ["model.reasoning_delta", { ...envelope.payload, text: "x".repeat(16385) }],
    ["model.reasoning_delta", { ...envelope.payload, format: "unknown" }],
    ["model.reasoning", { ...envelope.payload, status: "complete", text: "Final copy" }]
  ]) {
    await readChatEventStream(new Response(`data: ${JSON.stringify({ ...envelope, type, payload })}\n\n`), (event) => events.push(event));
  }
  assert.equal(events[0].type, "model_reasoning");
  assert.equal(events[0].text, "Safe live prefix");
  assert.equal(events[0].status, "receiving");
  for (const event of events.slice(1, 5)) assert.deepEqual(event, { type: "model_delta", delta: "" });
  assert.equal(events[5].status, "complete");
  assert.equal(events[5].text, "Final copy");
});
