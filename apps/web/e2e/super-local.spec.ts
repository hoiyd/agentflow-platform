import { expect, test } from "@playwright/test";
import type { components } from "../lib/api-contract.gen";

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
  const original: components["schemas"]["Workspace"][] = await (await page.request.get(`${api}/api/workspaces`)).json();
  // Other cases may have created owned spaces; only the default is unique.
  expect(original.filter(item => item.is_default)).toHaveLength(1);
  expect(original.every(item => item.owner_user_id === "super")).toBe(true);
  const created = await page.request.post(`${api}/api/workspaces`, { headers, data: { name: "Super lifecycle evidence" } });
  expect(created.status()).toBe(201);
  const workspace = await created.json();
  expect(workspace.owner_user_id).toBe("super");
  expect((await page.request.post(`${api}/api/agents`, { headers, data: { name: "Super shared config evidence" } })).status()).toBe(201);
  await page.reload();
  await expect(page.getByText("Super (local)", { exact: true })).toBeVisible();
  const retained: components["schemas"]["Workspace"][] = await (await page.request.get(`${api}/api/workspaces`)).json();
  expect(retained).toHaveLength(original.length + 1);
  expect(retained).toEqual(expect.arrayContaining([...original, workspace]));
  await testInfo.attach("super-identity-evidence.json", {
    body: JSON.stringify({ mode: "local", owner: "super", original_workspaces: original.map(item => item.id), created_workspace: workspace.id, workspaces: retained.map(item => item.id), checks: ["local mode remains unauthenticated", "Super display", "exactly one default Workspace", "all listed Workspaces owned by Super", "existing Workspaces preserved", "persistent ownership", "shared Agent config remains writable", "reload"], store: "disposable-postgres", limitation: "not an OIDC role or a full authorization audit" }, null, 2),
    contentType: "application/json"
  });
});
