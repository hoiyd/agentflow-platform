import assert from "node:assert/strict";
import test from "node:test";
import { readChatEventStream } from "./run-stream.ts";

test("streamed planning stays in Trace, not in the final Chat answer", async () => {
  const events = [];
  for (const role of ["planner", ""]) {
    const event = {type:"model.delta", schema_version:1, run_id:"run", stage_id:role ? "stage" : undefined, payload:{role,delta:"output",reset:true}};
    await readChatEventStream(new Response(`data: ${JSON.stringify(event)}\n\n`), item => events.push(item));
  }
  assert.deepEqual(events[0], {type:"model_delta",delta:""});
  assert.equal(events[1].delta,"output");
  assert.equal(events[1].reset,true);
});
