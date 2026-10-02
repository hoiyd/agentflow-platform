import { expect, test } from "@playwright/test";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";
test.skip(process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires isolated OIDC and Postgres");

test("owned Workspace lifecycle retains resource identity and enforces read-only/deleted boundaries", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("button", { name: "Workspace settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Workspaces", exact: true });
  await dialog.getByRole("button", { name: "New workspace", exact: true }).click();
  await dialog.getByLabel("Name", { exact: true }).fill("Lifecycle evidence");
  await dialog.getByLabel("Description", { exact: true }).fill("Disposable lifecycle test");
  await dialog.getByRole("button", { name: "Create workspace", exact: true }).click();
  await expect(dialog.getByRole("row").filter({ hasText: "Lifecycle evidence" })).toBeVisible();
  const response = await page.request.get(`${api}/api/workspaces`);
  expect(response.status()).toBe(200);
  const created = (await response.json()).find((item: { name: string }) => item.name === "Lifecycle evidence");
  expect(created.id).toMatch(/^[1-9]\d*$/);
  const headers = { Origin: web, "X-Workspace-ID": created.id };
  const conversation = await (await page.request.post(`${api}/api/conversations`, { headers, data: { title: "Stable resource" } })).json();
  const row = dialog.getByRole("row").filter({ hasText: "Lifecycle evidence" });
  await row.getByRole("button", { name: "Edit workspace", exact: true }).click();
  await dialog.getByLabel("Name", { exact: true }).fill("Renamed evidence");
  await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
  const renamed = dialog.getByRole("row").filter({ hasText: "Renamed evidence" });
  await renamed.getByRole("button", { name: "Make default workspace", exact: true }).click();
  await expect(renamed.getByText("Default", { exact: true })).toBeVisible();
  await renamed.getByRole("button", { name: "Archive workspace", exact: true }).click();
  const originalDefault = (await response.json()).find((item: { is_default: boolean }) => item.is_default);
  await dialog.getByRole("combobox", { name: "Replacement default workspace", exact: true }).selectOption(originalDefault.id);
  await dialog.getByRole("button", { name: "Confirm archive", exact: true }).click();
  await expect(renamed.getByRole("button", { name: "Restore workspace", exact: true })).toBeVisible();
  expect((await page.request.get(`${api}/api/conversations`, { headers })).status()).toBe(200);
  expect((await page.request.post(`${api}/api/conversations`, { headers, data: { title: "Rejected" } })).status()).toBe(409);
  await dialog.getByRole("button", { name: "Close workspace settings", exact: true }).click();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await page.getByRole("option", { name: "Renamed evidence Archived", exact: true }).click();
  await expect(page.getByRole("button", { name: "New conversation", exact: true })).toBeDisabled();
  await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toBeDisabled();
  await page.getByRole("button", { name: "Workspace settings", exact: true }).first().click();
  await renamed.getByRole("button", { name: "Restore workspace", exact: true }).click();
  await expect(renamed.getByRole("button", { name: "Archive workspace", exact: true })).toBeVisible();
  expect((await page.request.get(`${api}/api/conversations/${conversation.id}/messages`, { headers })).status()).toBe(200);
  await renamed.getByRole("button", { name: "Delete workspace", exact: true }).click();
  const [deletion] = await Promise.all([
    page.waitForResponse(response => response.request().method() === "DELETE" && response.url() === `${api}/api/workspaces/${created.id}`),
    dialog.getByRole("button", { name: "Confirm deletion", exact: true }).click()
  ]);
  expect(deletion.status()).toBe(204);
  await expect(renamed).toHaveCount(0);
  expect((await page.request.get(`${api}/api/conversations`, { headers })).status()).toBe(404);
  // Deleting the selected space navigates automatically. Await the destination
  // selection before requesting another reload; do not race two navigations.
  await expect(page.getByRole("button", { name: `Workspace: ${originalDefault.name}`, exact: true })).toBeVisible();
  await expect(dialog).toHaveCount(0);
  await page.reload();
  const retained = await (await page.request.get(`${api}/api/workspaces`)).json();
  expect(retained.some((item: { id: string }) => item.id === created.id)).toBe(false);
  await testInfo.attach("workspace-lifecycle-evidence.json", { body: JSON.stringify({ user: created.owner_user_id, workspace: created.id, conversation: conversation.id, checks: ["create", "rename preserves ID", "archive read-only", "restore", "soft delete", "stale requests rejected", "reload"], store: "disposable-postgres", provider: "signed-oidc-fixture", limitation: "not a complete PROD-002 object authorization audit" }, null, 2), contentType: "application/json" });
});

test("different OIDC owners cannot access or close each other's spaces; defaults remain owner scoped", async ({ page, browser }, testInfo) => {
  await page.goto("/workspace");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  const original = await (await page.request.get(`${api}/api/workspaces`)).json();
  const created = await page.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "Owner boundary evidence" } });
  expect(created.status()).toBe(201);
  const space = await created.json();
  expect((await page.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "Forged owner", owner_user_id: "super" } })).status()).toBe(400);
  expect((await page.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "   " } })).status()).toBe(400);
  const selected = { Origin: web, "X-Workspace-ID": space.id };
  const conversation = await (await page.request.post(`${api}/api/conversations`, { headers: selected, data: { title: "Only this owner" } })).json();
  expect((await page.request.patch(`${api}/api/workspaces/${space.id}`, { headers: { Origin: web }, data: { make_default: true } })).status()).toBe(200);
  expect((await (await page.request.get(`${api}/api/conversations`)).json()).some((item: { id: string }) => item.id === conversation.id)).toBe(true);
  const stranger = await browser.newContext();
  try {
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-other-owner" } });
    const other = await stranger.newPage();
    await other.goto(`${web}/workspace`);
    await other.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(other.getByRole("button", { name: /^Workspace: / })).toBeVisible();
    const otherSpaces = await (await other.request.get(`${api}/api/workspaces`)).json();
    expect(otherSpaces).toHaveLength(1);
    expect((await other.request.get(`${api}/api/workspaces/${space.id}`)).status()).toBe(404);
    expect((await other.request.get(`${api}/api/conversations/${conversation.id}/messages`, { headers: { "X-Workspace-ID": space.id } })).status()).toBe(404);
    expect((await other.request.delete(`${api}/api/workspaces/${space.id}`, { headers: { Origin: web } })).status()).toBe(404);
    expect((await other.request.delete(`${api}/api/workspaces/${otherSpaces[0].id}`, { headers: { Origin: web } })).status()).toBe(409);
    const secondSpace = await other.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "Second owned workspace" } });
    expect(secondSpace.status()).toBe(201);
    expect((await other.request.patch(`${api}/api/workspaces/${otherSpaces[0].id}`, { headers: { Origin: web }, data: { status: "archived", replacement_workspace_id: space.id } })).status()).toBe(409);
    // Service-wide configuration cannot be changed by an ordinary OIDC owner.
    expect((await other.request.post(`${api}/api/agents`, { headers: { Origin: web }, data: { name: "Global config write" } })).status()).toBe(403);
    await page.request.patch(`${api}/api/workspaces/${original.find((item: { is_default: boolean }) => item.is_default).id}`, { headers: { Origin: web }, data: { make_default: true } });
    await testInfo.attach("workspace-owner-boundary-evidence.json", { body: JSON.stringify({ store: "disposable-postgres", provider: "signed-oidc-fixture", owner: space.owner_user_id, other_owner: otherSpaces[0].owner_user_id, checks: { forged_owner: 400, invalid_name: 400, foreign_space_read: 404, foreign_space_delete: 404, foreign_resource_read: 404, last_active_delete: 409, owner_default_resolution: true, service_configuration_write: 403 }, limitation: "not a complete PROD-002 object authorization audit" }, null, 2), contentType: "application/json" });
  } finally {
    await stranger.close();
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-operator" } });
  }
});
