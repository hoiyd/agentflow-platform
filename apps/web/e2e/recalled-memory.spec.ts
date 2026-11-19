import { expect, test } from "@playwright/test";
import type { ChatEvent } from "../lib/api-types";
import type { RunEvent, RunReplay } from "../lib/api";
import type { MemoryInfo, MemoryMutation } from "../lib/memory-api";
import { defaultWorkspaceID } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";
const origin = "http://127.0.0.1:13000";

test("Replay corrects the current recalled memory without changing historical evidence", async ({ page, request }, info) => {
  const foreignWorkspace = await defaultWorkspaceID(page);
  const created = await request.post(`${api}/api/workspaces`, { headers: { Origin: origin }, data: { name: "Memory correction fixture" } });
  expect(created.status()).toBe(201);
  const workspace = (await created.json()).id as string;
  const headers = { Origin: origin, "X-Workspace-ID": workspace };
  const agentResponse = await request.post(`${api}/api/agents`, { headers, data: {
    name: "Release schedule reader", system_prompt: "Summarize the release schedule.", memory_enabled: true, retrieval_enabled: false, tools: []
  } });
  expect(agentResponse.status()).toBe(201);
  const agent = await agentResponse.json();
  const original = "The release day is Monday.";
  const current = "The release day is Tuesday.";
  const concurrent = "The release day is Wednesday.";
  const corrected = "The release day is Thursday.";
  const prompt = "What is the release schedule?";
  const seed = await request.post(`${api}/api/memories`, { headers, data: { kind: "fact", content: original } });
  expect(seed.status()).toBe(201);
  const memory: MemoryInfo = await seed.json();
  const memoryURL = `${api}/api/memories/${memory.id}`;
  const mutationURL = `${memoryURL}/mutations`;
  const replace = (version: number, content: string): MemoryMutation => ({
    operation_id: `fixture-${version}`, expected_version: version, action: "replace", content, actor: "fixture", reason: "Concurrent fixture update"
  });
  async function replay(runID: string): Promise<RunReplay> {
    const response = await request.get(`${api}/api/runs/${runID}/replay`, { headers });
    expect(response.status()).toBe(200);
    return response.json();
  }
  async function recall() {
    const response = await request.post(`${api}/api/chat`, { headers, data: { agent_id: agent.id, mode: "single", message: prompt } });
    expect(response.status()).toBe(200);
    const stream = await response.text();
    expect(stream).not.toContain("event: error");
    const frame = stream.match(/^event: done\r?\ndata: (.+)$/m);
    expect(frame).not.toBeNull();
    const done: ChatEvent = JSON.parse(frame![1]);
    expect(done.status).toBe("completed");
    expect(done.run_id).toBeTruthy();
    return replay(done.run_id!);
  }
  const recalled = (event: RunEvent) => (event.payload.retrieved_memories as Array<{ id: string; content: string }> | undefined)?.find(item => item.id === memory.id);
  const before = await recall();
  const event = before.run_events.find(event => event.type === "retrieval.completed" && recalled(event));
  expect(event && recalled(event)?.content).toBe(original);
  expect((await request.post(mutationURL, { headers, data: replace(1, current) })).status()).toBe(200);

  await page.goto("/workspace");
  await page.evaluate(id => sessionStorage.setItem("agentflow-workspace", id), workspace);
  await page.goto(`/runs/${before.run.id}?event=${event!.id}`);
  const row = page.locator(".retrieved-item").filter({ hasText: original });
  const action = row.getByRole("button", { name: "Correct memory", exact: true });
  await expect(action).toBeEnabled();
  await action.scrollIntoViewIfNeeded();
  await info.attach("replay-correct-memory.png", { body: await page.screenshot(), contentType: "image/png" });
  await action.click();
  const dialog = page.getByRole("dialog", { name: "Correct memory" });
  const draft = dialog.getByRole("textbox", { name: "Corrected content" });
  const save = dialog.getByRole("button", { name: "Save correction" });
  await expect(dialog).toContainText("Version 2");
  await expect(draft).toHaveValue(current);
  await draft.fill(corrected);
  await dialog.getByRole("textbox", { name: "Reason", exact: true }).fill("Confirm the current release schedule");
  await info.attach("replay-memory-correction-dialog.png", { body: await page.screenshot(), contentType: "image/png" });

  expect((await request.post(mutationURL, { headers, data: replace(2, concurrent) })).status()).toBe(200);
  const [conflict] = await Promise.all([page.waitForResponse(response => response.url() === mutationURL && response.request().method() === "POST"), save.click()]);
  expect(conflict.status()).toBe(409);
  await expect(dialog.getByRole("status")).toContainText("Your draft is preserved");
  await expect(save).toBeDisabled();
  await dialog.getByRole("button", { name: "Refresh current version" }).click();
  await expect(dialog).toContainText(`Current: ${concurrent}`);
  await expect(draft).toHaveValue(corrected);
  await save.click();
  await expect(page.getByRole("dialog", { name: "Memory history" })).toContainText("Version 4");
  await page.getByRole("button", { name: "Close memory dialog" }).click();
  await page.reload();
  await expect(row).toContainText(original);
  expect((await replay(before.run.id)).run_events.find(item => item.id === event!.id)).toEqual(event);
  const detail = await (await request.get(memoryURL, { headers })).json();
  expect(detail.memory).toMatchObject({ id: memory.id, version: 4, content: corrected });
  const after = await recall();
  expect(after.run_events.find(item => recalled(item) !== undefined)?.payload.retrieved_memories).toEqual(expect.arrayContaining([expect.objectContaining({ id: memory.id, content: corrected })]));

  const foreign = { ...headers, "X-Workspace-ID": foreignWorkspace };
  expect((await request.get(memoryURL, { headers: foreign })).status()).toBe(404);
  expect((await request.post(mutationURL, { headers: foreign, data: replace(4, "Denied correction") })).status()).toBe(404);
  const workspaceURL = `${api}/api/workspaces/${workspace}`;
  expect((await request.patch(workspaceURL, { headers, data: { status: "archived" } })).status()).toBe(200);
  await page.reload();
  await expect(page.getByText("Archived Workspace · Read-only", { exact: false })).toBeVisible();
  await expect(action).toBeDisabled();
  expect((await request.post(mutationURL, { headers, data: replace(4, "Denied correction") })).status()).toBe(409);
  expect((await request.patch(workspaceURL, { headers, data: { status: "active" } })).status()).toBe(200);
  expect((await request.post(mutationURL, { headers, data: { operation_id: "fixture-delete", expected_version: 4, action: "delete", actor: "fixture", reason: "Fixture cleanup" } })).status()).toBe(200);
  await page.reload();
  await action.click();
  await expect(dialog).toContainText("This memory has already been deleted.");
  await expect(save).toHaveCount(0);
  expect((await request.post(mutationURL, { headers, data: replace(5, "Do not resurrect") })).status()).toBe(409);
  const tombstone = await (await request.get(memoryURL, { headers })).json();
  expect(tombstone.memory).toMatchObject({ id: memory.id, version: 5, content: "" });
  expect(tombstone.memory.deleted_at).toBeTruthy();

  await info.attach("recalled-memory-evidence.json", { contentType: "application/json", body: JSON.stringify({
    workspace, agent: agent.id, memory: memory.id, prompt, originalRun: before.run.id, event, correctedRun: after.run.id,
    detail, tombstone, conflict: conflict.status(), checks: ["current detail", "draft preserved", "immutable event", "new recall", "foreign, archived, and deleted writes blocked"],
    limitations: ["local deterministic provider", "disposable PostgreSQL", "not a retrieval-quality measurement"]
  }, null, 2) });
});
