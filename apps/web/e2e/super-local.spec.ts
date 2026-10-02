import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_IDENTITY_TEST === "1" || process.env.AGENTFLOW_KEYCLOAK_TEST === "1", "requires trusted-local production composition");

test("Super owns local Workspaces without adding authentication or changing shared configuration access", async ({ page }, testInfo) => {
  const api = "http://127.0.0.1:18080";
  const headers = { Origin: "http://127.0.0.1:13000" };
  await page.goto("/workspace");
  await expect(page.getByText("Super (local)", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toHaveCount(0);
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  expect(session.mode).toBe("local");
  expect(session.authenticated).toBe(false);
  const original = await (await page.request.get(`${api}/api/workspaces`)).json();
  expect(original).toHaveLength(1);
  expect(original[0].owner_user_id).toBe("super");
  const created = await page.request.post(`${api}/api/workspaces`, { headers, data: { name: "Super lifecycle evidence" } });
  expect(created.status()).toBe(201);
  const workspace = await created.json();
  expect(workspace.owner_user_id).toBe("super");
  expect((await page.request.post(`${api}/api/agents`, { headers, data: { name: "Super shared config evidence" } })).status()).toBe(201);
  await page.reload();
  await expect(page.getByText("Super (local)", { exact: true })).toBeVisible();
  const retained = await (await page.request.get(`${api}/api/workspaces`)).json();
  expect(retained.map((item: { id: string; owner_user_id: string }) => [item.id, item.owner_user_id])).toEqual([[original[0].id, "super"], [workspace.id, "super"]]);
  await testInfo.attach("super-identity-evidence.json", {
    body: JSON.stringify({ mode: "local", owner: "super", workspaces: retained.map((item: { id: string }) => item.id), checks: ["local mode remains unauthenticated", "Super display", "persistent ownership", "shared Agent config remains writable", "reload"], store: "disposable-postgres", limitation: "not an OIDC role or a full authorization audit" }, null, 2),
    contentType: "application/json"
  });
});
