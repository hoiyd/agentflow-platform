import assert from "node:assert/strict";
import test from "node:test";

import { getReplayPageData } from "./replay-page-data.ts";

const replay = {
  run: { id: "run-1", agent_id: "agent-1", conversation_id: "conversation-1", status: "completed" },
  conversation: { id: "conversation-1", title: "Replay" },
  summary: { run_id: "run-1" },
  messages: [],
  steps: [],
  run_events: []
};

test("episode report failure does not hide the core replay", async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => String(url).endsWith("/episode")
    ? new Response(JSON.stringify({ error: "report unavailable" }), { status: 503 })
    : new Response(JSON.stringify(replay), { status: 200 });
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await getReplayPageData("run-1");

  assert.equal(result.data.run.id, "run-1");
  assert.equal(result.report, null);
  assert.match(result.reportError, /report unavailable/);
});

test("core replay failure remains fatal", async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => String(url).endsWith("/replay")
    ? new Response(JSON.stringify({ error: "replay unavailable" }), { status: 503 })
    : new Response(JSON.stringify({}), { status: 200 });
  t.after(() => { globalThis.fetch = originalFetch; });

  await assert.rejects(() => getReplayPageData("run-1"), /replay unavailable/);
});
