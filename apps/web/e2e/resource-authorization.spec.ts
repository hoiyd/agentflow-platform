import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";
test.skip(process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires signed OIDC and isolated Postgres");

test("two signed identities cannot read, mutate or link another Workspace's resources", async ({ page, browser }, testInfo) => {
  await page.goto("/workspace");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  const workspace = session.personal_workspace;
  const headers = { Origin: web, "X-Workspace-ID": workspace };
  const agent = await createWorkspaceChatAgent(page, workspace, "Resource authorization Agent");
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("Private authorization evidence");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByText("Evidence saved.", { exact: true })).toBeVisible();
  const runs = await (await page.request.get(`${api}/api/runs`, { headers })).json();
  const run = runs.find((item: { agent_id: string }) => item.agent_id === agent.id);
  expect(run).toBeDefined();
  await expect.poll(async () => (await (await page.request.get(`${api}/api/runs/${run.id}`, { headers })).json()).status).toBe("completed");
  const conversation = run.conversation_id;
  const saved = await page.request.post(`${api}/api/memories`, { headers, data: { kind: "note", content: "Private authorization memory", run_id: run.id } });
  expect(saved.status()).toBe(201);
  const memory = await saved.json();
  expect(memory.conversation_id).toBe(conversation);
  const artifacts = await page.request.get(`${api}/api/runs/${run.id}/artifacts`, { headers });
  expect(artifacts.status()).toBe(200);
  expect((await artifacts.json()).artifacts).toEqual([]);
  expect((await page.request.get(`${api}/api/runs/${run.id}/artifacts/missing`, { headers })).status()).toBe(404);
  expect((await page.request.get(`${api}/api/runs/${run.id}/artifacts/missing/search?q=needle`, { headers })).status()).toBe(404);
  const ingestion = await page.request.post(`${api}/api/documents`, { headers, data: { title: "Authorization source", content: "Private retrieval evidence belongs only to its Workspace." } });
  expect(ingestion.status()).toBe(201);
  const document = await ingestion.json();
  const patch = { expected_version: 0, operations: [{ type: "set_goal", goal: "Private authorization goal" }] };
  expect((await page.request.patch(`${api}/api/conversations/${conversation}/task-state`, { headers, data: patch })).status()).toBe(200);
  const retrieval = await page.request.post(`${api}/api/rag/search`, { headers, data: { query: "Private retrieval evidence", min_similarity: 0 } });
  expect(retrieval.status()).toBe(200);
  expect(JSON.stringify(await retrieval.json())).toContain(document.id);
  const second = await page.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "Other authorized space" } });
  expect(second.status()).toBe(201);
  const sameOwner = await second.json();
  const stranger = await browser.newContext();
  try {
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-resource-stranger" } });
    const other = await stranger.newPage();
    await other.goto(`${web}/workspace`);
    await other.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(other.getByRole("button", { name: /^Workspace: / })).toBeVisible();
    const otherSession = await (await other.request.get(`${api}/api/auth/session`)).json();
    expect(otherSession.user.id).not.toBe(session.user.id);
    const foreignScope = { Origin: web, "X-Workspace-ID": otherSession.personal_workspace };
    const readPaths = [
      `/api/conversations/${conversation}/messages`, `/api/conversations/${conversation}/task-state`,
      `/api/conversations/${conversation}/task-state/revisions/1`, `/api/memories/${memory.id}`,
      `/api/documents/${document.id}`, `/api/runs/${run.id}`, `/api/runs/${run.id}/events`,
      `/api/runs/${run.id}/replay`, `/api/runs/${run.id}/projection`, `/api/runs/${run.id}/usage`,
      `/api/runs/${run.id}/model_requests?include_content=true`, `/api/runs/${run.id}/artifacts`,
      `/api/runs/${run.id}/tool-effects`, `/api/runs/${run.id}/episode`
    ];
    for (const path of readPaths) {
      expect((await page.request.get(`${api}${path}`, { headers })).status(), `owner: ${path}`).toBe(200);
      for (const [client, scope] of [[other.request, foreignScope], [page.request, { ...headers, "X-Workspace-ID": sameOwner.id }]] as const) {
        const denied = await client.get(`${api}${path}`, { headers: scope });
        expect(denied.status(), path).toBe(404);
        expect(await denied.text()).not.toContain("Private");
      }
    }
    expect((await other.request.get(`${api}/api/runs`, { headers })).status()).toBe(404);
    expect((await other.request.patch(`${api}/api/conversations/${conversation}/task-state`, { headers: foreignScope, data: patch })).status()).toBe(404);
    expect((await other.request.delete(`${api}/api/documents/${document.id}`, { headers: foreignScope })).status()).toBe(404);
    expect((await other.request.post(`${api}/api/runs/${run.id}/cancel`, { headers: foreignScope })).status()).toBe(404);
    expect((await other.request.post(`${api}/api/memories`, { headers: foreignScope, data: { kind: "note", content: "Rejected link", run_id: run.id } })).status()).toBe(404);
    expect((await other.request.post(`${api}/api/memories`, { headers: foreignScope, data: { kind: "note", content: "Rejected scope", workspace_id: workspace } })).status()).toBe(400);
    const ownedAgent = await other.request.post(`${api}/api/agents`, { headers: foreignScope, data: { name: "Stranger's private config" } });
    expect(ownedAgent.status()).toBe(201);
    expect((await ownedAgent.json()).workspace_id).toBe(otherSession.personal_workspace);
    expect((await other.request.post(`${api}/api/tools/get_current_time/disable`, { headers: foreignScope })).status()).toBe(200);
    const ownerTools = await (await page.request.get(`${api}/api/tools`, { headers })).json();
    expect(ownerTools.find((tool: { name: string }) => tool.name === "get_current_time").workspace_enabled).toBe(true);
    for (const [path, data] of [["/api/memories/search", { query: "Private authorization memory" }], ["/api/rag/search", { query: "Private retrieval evidence", min_similarity: 0 }]] as const) {
      const search = await other.request.post(`${api}${path}`, { headers: foreignScope, data });
      expect(search.status()).toBe(200);
      const body = JSON.stringify(await search.json());
      expect(body).not.toContain(memory.id);
      expect(body).not.toContain(document.id);
      expect(body).not.toContain("Private");
    }
    // Real persisted data remains unchanged after denied operations and reload.
    await page.reload();
    await expect(page.getByText("Evidence saved.", { exact: true })).toBeVisible();
    const detail = await (await page.request.get(`${api}/api/memories/${memory.id}`, { headers })).json();
    expect(detail.memory.version).toBe(1);
    expect(detail.memory.deleted_at).toBeUndefined();
    const state = await (await page.request.get(`${api}/api/conversations/${conversation}/task-state`, { headers })).json();
    expect(state.version).toBe(1);
    expect(state.goal).toBe("Private authorization goal");
    expect((await page.request.get(`${api}/api/documents/${document.id}`, { headers })).status()).toBe(200);
    await testInfo.attach("resource-authorization-evidence.json", { body: JSON.stringify({ schema: "resource-authorization-v1", provider: "signed-oidc-fixture", store: "disposable-postgres", owners: [session.user.id, otherSession.user.id], workspaces: [workspace, sameOwner.id, otherSession.personal_workspace], resources: { run: run.id, conversation, memory: memory.id, document: document.id }, checks: ["real Chat UI to Go to Postgres", "foreign object IDs", "same-owner different Workspace", "selectors cannot grant access", "provenance before commit", "scoped retrieval and chunk content", "owner-scoped Agent and Tool configuration", "denied writes preserve state", "reload"], limitations: ["fixed embeddings do not measure retrieval quality", "populated nested artifacts/effects/full captures covered by backend Postgres integration", "no live IdP"] }, null, 2), contentType: "application/json" });
  } finally {
    await stranger.close();
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-operator" } });
  }
});

test("logout denies active Chat delivery while admitted execution remains durable", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  const headers = { Origin: web, "X-Workspace-ID": session.personal_workspace };
  const agent = await createWorkspaceChatAgent(page, session.personal_workspace, "Stream authorization Agent");
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("stream-gate authorization continuity");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByText("First token", { exact: true })).toBeVisible();
  const runs = await (await page.request.get(`${api}/api/runs`, { headers })).json();
  const run = runs.find((item: { status: string; agent_id: string }) => item.status === "running" && item.agent_id === agent.id);
  expect(run).toBeDefined();
  try {
    expect((await page.request.post(`${api}/api/auth/logout`, { headers: { Origin: web } })).status()).toBe(204);
  } finally {
    await page.request.post(`${api}/__fixture/release`);
  }
  // The SSE authorization error follows the same reauthentication path as 401.
  await expect(page.getByRole("heading", { name: "Fixture sign in", exact: true })).toBeVisible();
  expect((await page.request.get(`${api}/api/runs/${run.id}`, { headers })).status()).toBe(401);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  await expect.poll(async () => (await (await page.request.get(`${api}/api/runs/${run.id}`, { headers })).json()).status).toBe("completed");
  const messages = await (await page.request.get(`${api}/api/conversations/${run.conversation_id}/messages`, { headers })).json();
  expect(messages.some((message: { role: string; content: string }) => message.role === "assistant" && message.content.includes("then finished."))).toBe(true);
  await testInfo.attach("stream-authorization-evidence.json", { body: JSON.stringify({ store: "disposable-postgres", provider: "signed-oidc-fixture", user: session.user.id, workspace: session.personal_workspace, run: run.id, checks: ["partial answer visible before logout", "active Chat reauthenticates", "logged-out reads rejected", "execution completes independently", "persisted answer available after sign-in"], limitation: "revocation takes effect on the next SSE write, not data already delivered" }, null, 2), contentType: "application/json" });
});
