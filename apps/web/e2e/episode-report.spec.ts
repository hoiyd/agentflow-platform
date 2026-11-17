import fs from "node:fs";
import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

// Real browser/API/Postgres gate: report is opt-in, export uses Run evidence,
// and a failed report cannot hide Replay or prevent retry after reload.
test("Episode reports load on demand without blocking Replay", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  await createWorkspaceChatAgent(page, workspace, "Episode reader");
  const question = "Summarize this synthetic release evidence.";
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(question);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  const trace = page.getByRole("link", { name: "View trace", exact: true });
  await expect(trace).toBeVisible();
  const href = (await trace.getAttribute("href"))!;
  const runID = href.split("/").at(-1)!;
  let reads = 0;
  page.on("request", request => { if (request.url().endsWith(`/runs/${runID}/episode`)) reads++; });
  await trace.click();
  await expect(page.getByRole("heading", { name: "Run replay" })).toBeVisible();
  const panel = page.locator("#run-verification-evidence");
  await expect(panel).not.toHaveAttribute("open");
  expect(reads).toBe(0);
  await page.screenshot({ path: testInfo.outputPath("episode-collapsed.png"), fullPage: true });
  await panel.locator("summary").click();
  await expect(panel.getByText(question, { exact: true })).toBeVisible();
  expect(reads).toBe(1);
  await page.screenshot({ path: testInfo.outputPath("episode-expanded.png"), fullPage: true });
  const downloaded = page.waitForEvent("download");
  await panel.getByRole("button", { name: "Export JSON" }).click();
  const download = await downloaded;
  const reportPath = testInfo.outputPath("episode.json");
  await download.saveAs(reportPath);
  const report = JSON.parse(fs.readFileSync(reportPath, "utf8"));
  expect(report.run.id).toBe(runID);
  expect(report.task).toBe(question);
  expect(report.messages).toHaveLength(1);
  expect(report.messages[0].content).toBe(report.final_output);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Run replay" })).toBeVisible();
  expect(reads).toBe(1);
  await page.route(`**/runs/${runID}/episode`, route => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "Synthetic report outage" }) }), { times: 1 });
  await panel.locator("summary").click();
  await expect(panel.getByRole("alert")).toContainText("Synthetic report outage");
  await expect(page.getByRole("heading", { name: "Run replay" })).toBeVisible();
  await panel.getByRole("button", { name: "Retry report" }).click();
  await expect(panel.getByText(question, { exact: true })).toBeVisible();
  expect(errors).toEqual([]);
  await testInfo.attach("episode-evidence.json", { body: JSON.stringify({ schema: "episode-browser-v1", workspace, run_id: runID,
    question, report, report_requests: reads, outcomes: ["closed and reloaded Replay does not fetch report", "explicit expansion and JSON export", "local failure and retry"],
    limitations: ["synthetic task, controlled Provider, disposable Postgres; not live quality evidence"] }, null, 2), contentType: "application/json" });
});
