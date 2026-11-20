import fs from "node:fs";
import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

// A Run export must not include neighboring messages. Comparison must use the
// two Run bindings, and different tasks must not yield metric deltas.
test("exports and compares evidence directly from Replay", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  await createWorkspaceChatAgent(page, workspace, "Replay reader");
  const question = "Summarize this synthetic release evidence.";
  const later = "NEIGHBOR_CANARY: outline a picnic menu.";
  const trace = page.getByRole("link", { name: "View trace", exact: true });
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(question);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  await expect(trace).toHaveAttribute("href", /\/runs\//);
  const first = (await trace.getAttribute("href"))!;
  const runID = first.split("/").at(-1)!;

  await page.getByPlaceholder("Ask AgentFlow anything...").fill(later);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  await expect(trace).toHaveAttribute("href", new RegExp(`^(?!.*${runID})/runs/`));
  const laterRun = (await trace.getAttribute("href"))!.split("/").at(-1)!;

  const runReads: string[] = [];
  page.on("request", req => {
    if (new URL(req.url()).pathname.startsWith("/api/runs/")) runReads.push(req.url());
  });
  await page.goto(first);
  const button = page.getByRole("button", { name: "Export Run JSON" });
  await expect(button).toBeVisible();
  const readsBeforeExport = runReads.filter(url => /\/(replay|episode)$/.test(url)).length;
  const downloaded = page.waitForEvent("download");
  await button.click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe(`agentflow-run-${runID}.json`);
  const reportPath = testInfo.outputPath("run.json");
  await download.saveAs(reportPath);
  const raw = fs.readFileSync(reportPath, "utf8");
  const evidence = JSON.parse(raw);
  expect(evidence.run.id).toBe(runID);
  expect(evidence.candidate.question).toBe(question);
  expect(evidence.messages).toHaveLength(1);
  expect(evidence.messages[0]).toMatchObject({ id: evidence.candidate.message_id, role: "assistant" });
  expect(raw).not.toContain("NEIGHBOR_CANARY");
  expect(runReads.filter(url => /\/(replay|episode)$/.test(url))).toHaveLength(readsBeforeExport);
  await page.screenshot({ path: testInfo.outputPath("replay-export.png") });

  await page.getByText("Evaluation", { exact: true }).click();
  await page.getByRole("link", { name: "Compare with another run" }).click();
  await expect(page.getByLabel("Current run")).toHaveValue(runID);
  await page.getByLabel("Baseline run").selectOption(laterRun);
  await page.getByRole("button", { name: "Compare evidence" }).click();
  await expect(page.getByText("Task identity differs", { exact: true })).toBeVisible();
  const task = page.getByRole("row").filter({ has: page.getByRole("rowheader", { name: "Task", exact: true }) });
  await expect(task).toContainText(question);
  await expect(task).toContainText(later);
  expect(runReads.filter(url => url.endsWith("/episode"))).toEqual([]);
  await testInfo.attach("run-replay-evidence.json", { contentType: "application/json", body: JSON.stringify({
    workspace, evidence, later_run_id: laterRun, later_task: later, reads: runReads.map(url => new URL(url).pathname),
    outcome: "Run-only export; unrelated tasks cannot produce metric deltas",
    limitations: ["synthetic tasks, controlled Provider, disposable Postgres; not live quality evidence"]
  }) });
});
