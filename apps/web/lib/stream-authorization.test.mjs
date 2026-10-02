import assert from "node:assert/strict";
import test from "node:test";
import { APIError } from "./api-client.ts";
import { observeRunEvents, readChatEventStream } from "./run-stream.ts";

// Failure inventory: a permanent authorization error must not reconnect, deliver
// subsequent frames or leave a reader open. Provider errors retain their contract.
for (const [code, status] of [["unauthenticated", 401], ["forbidden", 403], ["not_found", 404]]) {
  test(`Chat closes denied stream (${code}) without accepting later content`, async () => {
    let canceled = false;
    const denied = { type: "error", code, source: "http_api", category: "authentication", error: "Access denied", retryable: false };
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(`event: error\ndata: ${JSON.stringify(denied)}\n\ndata: {"type":"model_delta","delta":"must not arrive"}\n\n`));
      },
      cancel() { canceled = true; }
    });
    const received = [];
    await assert.rejects(readChatEventStream(new Response(body), event => received.push(event)), error => {
      assert.ok(error instanceof APIError);
      assert.equal(error.status, status);
      assert.equal(error.code, code);
      assert.equal(error.retryable, false);
      return true;
    });
    assert.equal(canceled, true);
    assert.deepEqual(received, []);
  });
}

test("Run observation does not reconnect a denied SSE response", async t => {
  const original = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = async () => {
    requests++;
    return new Response('event: error\ndata: {"type":"error","code":"not_found","source":"http_api","error":"Access revoked","retryable":false}\n\n');
  };
  t.after(() => { globalThis.fetch = original; });
  await assert.rejects(observeRunEvents("owned-run", { onEvent() { assert.fail("must not deliver resource events"); }, onSnapshot() { assert.fail("must not deliver snapshot"); } }), error => error instanceof APIError && error.status === 404);
  assert.equal(requests, 1);
});

test("provider errors still reach Chat instead of being mistaken for access revocation", async () => {
  const event = { type: "error", source: "model_provider", code: "not_found", error: "Model unavailable" };
  const received = [];
  await readChatEventStream(new Response(`data: ${JSON.stringify(event)}\n\n`), value => received.push(value));
  assert.deepEqual(received, [event]);
});
