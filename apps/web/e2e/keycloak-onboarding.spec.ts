import { expect, test } from "@playwright/test";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";
test.skip(process.env.AGENTFLOW_KEYCLOAK_TEST !== "1", "requires disposable Keycloak, theme and Postgres");

test("themed OIDC registration provisions a personal Workspace without sending passwords to AgentFlow", async ({ page, context }, testInfo) => {
  const username = `operator-${Date.now()}`;
  const password = "Fixture-password-42";
  const apiBodies: string[] = [];
  page.on("request", request => { if (request.url().startsWith(api) && request.postData()) apiBodies.push(request.postData()!); });
  await page.goto("/workspace");
  await expect(page.getByRole("heading", { name: "Sign in to AgentFlow", exact: true })).toBeVisible();
  expect(new URL(page.url()).origin).toBe("http://127.0.0.1:19081");
  await page.getByRole("link", { name: "Create account", exact: true }).click();
  await expect(page.locator("#kc-header-wrapper")).toHaveText("AgentFlow");
  await expect(page.getByRole("heading", { name: "Create your account", exact: true })).toBeVisible();
  // The form posts to the IdP, not Next.js or the Go API.
  expect(new URL((await page.locator("#kc-register-form").getAttribute("action"))!).origin).toBe("http://127.0.0.1:19081");
  await page.locator("#username").fill(username);
  await page.locator("#email").fill(`${username}@example.invalid`);
  await page.locator("#firstName").fill("Fixture");
  await page.locator("#lastName").fill("Operator");
  await page.locator("#password").fill("short");
  await page.locator("#password-confirm").fill("short");
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  await expect(page.locator("#input-error-password")).toBeVisible();
  expect((await (await page.request.get(`${api}/api/auth/session`)).json()).authenticated).toBe(false);
  await page.locator("#password").fill(password);
  await page.locator("#password-confirm").fill(password);
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  await expect(page.getByRole("button", { name: "Workspace: Personal workspace", exact: true })).toBeVisible();
  const identity = await (await page.request.get(`${api}/api/auth/session`)).json();
  expect(identity.authenticated).toBe(true);
  expect(identity.workspaces).toEqual([identity.personal_workspace]);
  await page.getByRole("button", { name: "Workspace: Personal workspace", exact: true }).click();
  await expect(page.getByRole("option", { name: "Personal workspace", exact: true })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Escape");
  const foreign = await page.request.get(`${api}/api/conversations?workspace_id=default_workspace`);
  expect(foreign.status()).toBe(403);
  const created = await page.request.post(`${api}/api/conversations`, { headers: { Origin: web, "X-Workspace-ID": identity.personal_workspace }, data: { title: "Personal Workspace onboarding evidence" } });
  expect(created.status()).toBe(201);
  await page.reload();
  await expect(page.getByText("Personal Workspace onboarding evidence", { exact: true }).first()).toBeVisible();
  const cookies = await context.cookies(api);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.locator("#password")).toBeVisible();
  await context.addCookies(cookies);
  expect((await page.request.get(`${api}/api/conversations`)).status()).toBe(401);
  await expect(page.locator("#kc-header-wrapper")).toHaveText("AgentFlow");
  await expect(page.locator("#password")).toBeVisible();
  // Keycloak can retain the known username and request only its credential.
  if (await page.locator("#username").isVisible()) await page.locator("#username").fill(username);
  await page.locator("#password").fill("Wrong-fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.locator("#input-error")).toBeVisible();
  expect((await (await page.request.get(`${api}/api/auth/session`)).json()).authenticated).toBe(false);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: "Workspace: Personal workspace", exact: true })).toBeVisible();
  const relogin = await (await page.request.get(`${api}/api/auth/session`)).json();
  expect(relogin.workspaces).toEqual(identity.workspaces);
  expect(apiBodies.every(body => !body.includes(password) && !body.includes("Wrong-fixture-password"))).toBe(true);
  await testInfo.attach("onboarding-evidence.json", {
    body: JSON.stringify({ schema: "oidc-onboarding-evidence-v1", provider: "Keycloak 26.7.5", theme: "agentflow", store: "disposable-postgres", user_id: identity.user.id, workspace: identity.personal_workspace, checks: { native_registration: true, password_policy: true, wrong_password_rejected: true, personal_only: true, foreign_workspace: 403, conversation_created: 201, reload: true, logout_revocation: 401, repeat_login_same_workspace: true, no_password_sent_to_api: true }, limitations: ["disposable realm, no operator accounts modified", "not PROD-002 full object authorization or MFA coverage"] }, null, 2), contentType: "application/json"
  });
});
