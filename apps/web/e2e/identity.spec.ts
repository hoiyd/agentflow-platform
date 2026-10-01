import { expect, test } from "@playwright/test";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";

test.skip(process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires the isolated OIDC fixture composition");

test("OIDC login, member Workspace, persistence, forbidden access and logout", async ({ page, context }, testInfo) => {
  const anonymous = await page.request.get(`${api}/api/conversations`);
  expect(anonymous.status()).toBe(401);
  const [authorization] = await Promise.all([
    page.waitForRequest(request => new URL(request.url()).pathname === "/authorize"),
    page.goto("/workspace")
  ]);
  expect(new URL(authorization.url()).searchParams.get("prompt")).toBe("login");
  await expect(page.getByRole("heading", { name: "Fixture sign in", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: "Workspace: default_workspace", exact: true })).toBeVisible();
  const identity = await (await page.request.get(`${api}/api/auth/session`)).json();
  expect(identity.mode).toBe("oidc");
  expect(identity.authenticated).toBe(true);
  expect(identity.workspaces).toEqual(["default_workspace", "workspace-test"]);
  const forbidden = await page.request.get(`${api}/api/runs?workspace_id=foreign-workspace`);
  expect(forbidden.status()).toBe(403);
  const csrf = await page.request.post(`${api}/api/conversations`, { headers: { Origin: "https://foreign.invalid", "X-Workspace-ID": "default_workspace" }, data: { title: "must not persist" } });
  expect(csrf.status()).toBe(403);
  const switcher = page.getByRole("button", { name: "Workspace: default_workspace", exact: true });
  await switcher.focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("option", { name: "default_workspace", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(switcher).toBeFocused();
  await expect(page.getByRole("listbox", { name: "Workspace" })).toHaveCount(0);
  await switcher.click();
  const menu = page.getByRole("listbox", { name: "Workspace" });
  const triggerBounds = await switcher.boundingBox();
  const menuBounds = await menu.boundingBox();
  expect(triggerBounds).not.toBeNull();
  expect(menuBounds).not.toBeNull();
  expect(menuBounds!.y).toBeGreaterThanOrEqual(triggerBounds!.y + triggerBounds!.height);
  expect(menuBounds!.width).toBeGreaterThanOrEqual(triggerBounds!.width);
  await testInfo.attach("workspace-menu.png", { body: await menu.screenshot(), contentType: "image/png" });
  await page.getByRole("option", { name: "workspace-test", exact: true }).click();
  await expect(page.getByRole("button", { name: "Workspace: workspace-test", exact: true })).toBeVisible();
  const created = await page.request.post(`${api}/api/conversations`, { headers: { Origin: web, "X-Workspace-ID": "workspace-test" }, data: { title: "Authenticated workspace evidence" } });
  expect(created.status()).toBe(201);
  await page.reload();
  await expect(page.getByText("Authenticated workspace evidence", { exact: true }).first()).toBeVisible();
  // Tab lifetime is not session lifetime: reopen in the same browser context.
  const reopened = await context.newPage();
  await page.close();
  let loginRedirects = 0;
  reopened.on("request", request => { if (new URL(request.url()).pathname === "/authorize") loginRedirects++; });
  await reopened.goto("/workspace");
  await expect(reopened.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  expect(loginRedirects).toBe(0);
  const oldCookies = await context.cookies(api);
  const [reauthorization] = await Promise.all([
    reopened.waitForRequest(request => new URL(request.url()).pathname === "/authorize"),
    reopened.getByRole("button", { name: "Sign out", exact: true }).click()
  ]);
  expect(new URL(reauthorization.url()).searchParams.get("prompt")).toBe("login");
  await expect(reopened.getByRole("heading", { name: "Fixture sign in", exact: true })).toBeVisible();
  expect((await (await reopened.request.get(`${api}/api/auth/session`)).json()).authenticated).toBe(false);
  await context.addCookies(oldCookies);
  expect((await reopened.request.get(`${api}/api/conversations`)).status()).toBe(401);
  await reopened.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(reopened.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  expect((await (await reopened.request.get(`${api}/api/auth/session`)).json()).user.id).toBe(identity.user.id);
  await testInfo.attach("identity-evidence.json", { body: JSON.stringify({ schema: "identity-membership-evidence-v1", provider: "signed-oidc-fixture", store: "disposable-postgres", user_id: identity.user.id, workspace: "workspace-test", checks: { anonymous: 401, foreign_workspace: 403, cross_origin_write: 403, creation: 201, workspace_menu_keyboard: true, workspace_menu_geometry: true, workspace_switch_persisted: true, reload: true, tab_reopen_preserves_session: true, logout_revocation: 401, reauthentication_prompt: "login" }, limitations: ["not a live IdP or proof of its credential UI", "not PROD-002 object authorization audit"] }, null, 2), contentType: "application/json" });
});

test("authenticated nonmember cannot mount business consumers", async ({ page }, testInfo) => {
  await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-nonmember" } });
  try {
    await page.goto("/workspace");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Workspace access required" })).toBeVisible();
    await expect(page.getByRole("button", { name: "New conversation", exact: true })).toHaveCount(0);
    expect((await page.request.get(`${api}/api/conversations`)).status()).toBe(403);
    await testInfo.attach("nonmember-evidence.json", { body: JSON.stringify({ provider: "signed-oidc-fixture", subject: "fixture-nonmember", business_access: 403, workbench_mounted: false, limitation: "operator-configured membership only" }), contentType: "application/json" });
  } finally {
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-operator" } });
  }
});
