import { test, expect } from "@playwright/test";
import { defaultWorkspaceID } from "./fixtures/workspace-agent";

test.beforeEach(() => test.skip(process.env.AGENTFLOW_PROJECT_GUIDANCE_TEST !== "1", "requires reviewed project fixtures"));

test("Project file navigation rejects stale reads and keeps directory failures visible", async ({ page }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  let release!: () => void;
  let arrived!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const ready = new Promise<void>(resolve => { arrived = resolve; });
  await page.route(/\/api\/project-guidance\/projects\/project\/files\?path=\.$/, async route => {
    const response = await route.fetch();
    arrived();
    await held;
    await route.fulfill({ response });
  }, { times: 1 });
  try {
    await page.getByRole("button", { name: "Project instructions", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Project instructions", exact: true });
    await dialog.getByRole("button", { name: /^Source directory:/ }).click();
    await dialog.getByRole("option", { name: /^project / }).click();
    await ready;
    const directory = dialog.getByRole("textbox", { name: "Relative directory", exact: true });
    await directory.fill("reports");
    await directory.press("Enter");
    await expect(dialog.getByRole("checkbox", { name: "Select reports/input.md", exact: true })).toBeVisible();
    release();
    await expect(dialog.getByRole("checkbox", { name: "Select report.md", exact: true })).toHaveCount(0);
    await directory.fill("missing");
    await dialog.getByRole("button", { name: "Open directory", exact: true }).click();
    await expect(dialog.getByRole("alert")).toContainText("Task files unavailable");
    await expect(dialog.getByRole("checkbox")).toHaveCount(0);
    await dialog.getByRole("button", { name: "Parent directory", exact: true }).click();
    await expect(dialog.getByRole("checkbox", { name: "Select report.md", exact: true })).toBeVisible();
    await expect(dialog.getByRole("checkbox", { name: /\.env/ })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Project instructions", exact: true })).toBeFocused();
    await test.info().attach("project-guidance-navigation-evidence.json", { body: JSON.stringify({ workspace_id: workspace,
      checks: ["real directory query", "late root response discarded", "missing directory error", "recovered parent directory", "sensitive names omitted", "Escape returns focus"] }), contentType: "application/json" });
  } finally { release(); }
});

test("Project selection and discovery remain in their owning Workspace", async ({ page }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const original = (await page.getByRole("button", { name: /^Workspace: / }).getAttribute("aria-label"))!;
  await page.getByRole("button", { name: "Project instructions", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Project instructions", exact: true });
  await dialog.getByRole("button", { name: /^Source directory:/ }).click();
  await dialog.getByRole("option", { name: /^project / }).click();
  await dialog.getByRole("checkbox", { name: "Select report.md", exact: true }).check();
  await dialog.getByRole("button", { name: "Apply instructions", exact: true }).click();
  const created = await page.request.post("http://127.0.0.1:18080/api/workspaces", {
    headers: { Origin: "http://127.0.0.1:13000" }, data: { name: "Project guidance isolation" }
  });
  expect(created.status()).toBe(201);
  const isolated = await created.json();
  await page.reload();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await Promise.all([page.waitForEvent("domcontentloaded"), page.getByRole("option", { name: isolated.name, exact: true }).click()]);
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Project instructions", exact: true }).click();
  await expect(dialog.getByRole("status")).toHaveText("No trusted instruction sources are available in this workspace.");
  await expect(dialog.getByRole("button", { name: "Apply instructions", exact: true })).toBeDisabled();
  const denied = await page.request.get("http://127.0.0.1:18080/api/project-guidance/projects/project/files", { headers: { "X-Workspace-ID": isolated.id } });
  expect(denied.status()).toBe(400);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await Promise.all([page.waitForEvent("domcontentloaded"), page.getByRole("option", { name: original.replace(/^Workspace: /, ""), exact: true }).click()]);
  await expect(page.getByRole("button", { name: "Project instructions: project, 1 file", exact: true })).toBeVisible();
  await test.info().attach("project-guidance-workspace-evidence.json", { body: JSON.stringify({ workspace_id: workspace,
    isolated_workspace_id: isolated.id, checks: ["selection not inherited", "scoped catalog empty", "foreign project query rejected", "own selection restored"] }), contentType: "application/json" });
});
