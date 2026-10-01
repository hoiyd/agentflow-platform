import assert from "node:assert/strict";
import test from "node:test";
import { apiRequest, setWorkspaceID } from "./api-client.ts";

test("authenticated requests carry cookies and selected workspace", async t => {
  const original = globalThis.fetch;
  t.after(() => { globalThis.fetch = original; setWorkspaceID("default_workspace"); });
  globalThis.fetch = async (_url, init) => {
    assert.equal(init.credentials, "include");
    assert.equal(init.headers.get("X-Workspace-ID"), "member-workspace");
    return new Response("{}");
  };
  setWorkspaceID("member-workspace");
  await apiRequest("/api/conversations", {}, { errorMessage: "List" });
});
