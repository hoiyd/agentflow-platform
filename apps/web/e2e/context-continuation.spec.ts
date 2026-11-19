import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";
import type { Conversation, Message, RunInfo, TaskState } from "../lib/api";

const api = "http://127.0.0.1:18080";

test("Review and send a handoff without changing the source conversation", async ({ page, request }, info) => {
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const agent = await createWorkspaceChatAgent(page, workspace, "Handoff fixture agent");
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const created = await request.post(`${api}/api/conversations`, { headers, data: { title: "Release readiness" } });
  expect(created.status()).toBe(201);
  const source: Conversation = await created.json();
  const read = async <T>(path: string) => {
    const response = await request.get(api + path, { headers });
    expect(response.ok()).toBe(true);
    return await response.json() as T;
  };
  const originalMessage = "Previous discussion only. Do not copy this transcript.";
  const original = await request.post(`${api}/api/chat`, { headers, data: {
    conversation_id: source.id, agent_id: agent.id, mode: "single", message: originalMessage,
  } });
  expect(original.ok()).toBe(true);
  expect(await original.text()).not.toContain("event: error");
  const patch = await request.patch(`${api}/api/conversations/${source.id}/task-state`, { headers, data: {
    expected_version: 0, operations: [
      { type: "set_goal", goal: "Prepare the release readiness report." },
      { type: "upsert_constraint", constraint: { id: "scope", statement: "Use the staging environment only." } },
      { type: "upsert_task", task: { id: "check", title: "Verify rollback instructions", status: "pending" } },
      { type: "upsert_task", task: { id: "done", title: "Internal completed item", status: "completed" } }
    ]
  } });
  expect(patch.ok()).toBe(true);
  const sourcePath = `/api/conversations/${source.id}`;
  const before = await read<Message[]>(sourcePath + "/messages");
  const stateBefore = await read<TaskState>(sourcePath + "/task-state");
  const known = (await read<Conversation[]>("/api/conversations")).map(item => item.id);

  await page.goto(`/workspace?conversation=${source.id}`);
  await expect(page.getByText(originalMessage, { exact: true })).toBeVisible();
  const open = page.getByRole("button", { name: "Start with handoff", exact: true });
  const dialog = page.getByRole("dialog", { name: "Start with handoff" });
  const draft = dialog.getByRole("textbox", { name: "First message" });
  await open.click();
  await expect(draft).toHaveValue(/Use the staging environment only/);
  await expect(draft).toHaveValue(/Verify rollback instructions/);
  await expect(draft).not.toHaveValue(/Internal completed item|Previous discussion only/);
  await info.attach("context-continuation-dialog.png", { body: await page.screenshot(), contentType: "image/png" });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect((await read<Conversation[]>("/api/conversations")).map(item => item.id)).toEqual(known);

  await open.click();
  await expect(draft).toHaveValue(/Use the staging environment only/);
  const start = dialog.getByRole("button", { name: "Start new conversation" });
  await draft.fill("x".repeat(4001));
  await expect(start).toBeDisabled();
  const revised = "Verify only the staging rollback instructions and report gaps.";
  await draft.fill(revised);
  await page.route("**/api/chat", route => route.fulfill({ status: 503, json: { error: "Fixture admission failure" } }), { times: 1 });
  await start.click();
  await expect(dialog.getByRole("alert")).toContainText("Fixture admission failure");
  await expect(draft).toHaveValue(revised);
  expect((await read<Conversation[]>("/api/conversations")).map(item => item.id)).toEqual(known);

  await start.click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  await expect(page.getByText(revised, { exact: true })).toBeVisible();
  await expect(page.getByText(originalMessage, { exact: true })).toHaveCount(0);
  const added = (await read<Conversation[]>("/api/conversations")).filter(item => !known.includes(item.id));
  expect(added).toHaveLength(1);
  const target = added[0];
  const messages = await read<Message[]>(`/api/conversations/${target.id}/messages`);
  expect(messages.filter(item => item.role === "user").map(item => item.content)).toEqual([revised]);
  const run = (await read<RunInfo[]>("/api/runs")).find(item => item.conversation_id === target.id);
  expect(run).toMatchObject({ status: "completed", mode: "single", agent_id: agent.id });
  expect(await read<Message[]>(sourcePath + "/messages")).toEqual(before);
  expect(await read<TaskState>(sourcePath + "/task-state")).toEqual(stateBefore);
  expect(await read<TaskState>(`/api/conversations/${target.id}/task-state`)).toMatchObject({ version: 0 });
  await page.goto(`/workspace?conversation=${target.id}`);
  await expect(page.getByText(revised, { exact: true })).toBeVisible();
  await info.attach("context-continuation.json", { contentType: "application/json", body: JSON.stringify({
    workspace, source_id: source.id, target_id: target.id, run_id: run?.id, agent_id: agent.id,
    mode: run?.mode, revised,
    checks: ["cancel without write", "rejected submission preserves draft", "isolated new conversation", "source history and state unchanged", "target reload"],
    limitations: ["production Go and Postgres, scripted model", "503 injected before admission"]
  }, null, 2) });
});
