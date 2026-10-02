import assert from "node:assert/strict";
import test from "node:test";
import { apiRequest, setWorkspaceID } from "./api-client.ts";

test("authenticated requests carry cookies and selected workspace", async t => {
  const original = globalThis.fetch;
  t.after(() => { globalThis.fetch = original; setWorkspaceID(""); });
  globalThis.fetch = async (_url, init) => {
    assert.equal(init.credentials, "include");
    assert.equal(init.headers.get("X-Workspace-ID"), "11");
    return new Response("{}");
  };
  setWorkspaceID("11");
  await apiRequest("/api/conversations", {}, { errorMessage: "List" });
});
