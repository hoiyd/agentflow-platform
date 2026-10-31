import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";

for (const planning of [true, false]) {
  test(`planning=${planning}: follow-up intent survives reload without changing steering`, async ({ page, request }) => {
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
    const workspace = await defaultWorkspaceID(page);
    const headers = { "X-Workspace-ID": workspace };
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: /Single agent/ }).click();
    const agent = await createWorkspaceChatAgent(page, workspace, `Inbox planning ${planning}`);
    await page.getByRole("checkbox", { name: "Read-only planning", exact: true }).setChecked(planning);
    const input = `stream-gate: inbox planning=${planning}`;
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(input);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.locator(".message.assistant").last()).toContainText("First token");
    const id = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
    const read = async (path: string) => {
      const response = await request.get(`${api}${path}`, { headers });
      expect(response.ok()).toBe(true);
      return response.json();
    };
    const source = await read(`/api/runs/${id}`);
    const path = `/api/conversations/${source.conversation_id}/inputs`;
    const draft = "STEERING_CANARY: keep the current task concise";
    let rejection: unknown;
    let successor: unknown;
    try {
      await page.getByPlaceholder("Ask AgentFlow anything...").fill(draft);
      const queue = page.getByRole("button", { name: "Queue follow-up", exact: true });
      if (planning) await expect(queue).toBeDisabled();
      else await expect(queue).toBeEnabled();
      await expect(page.getByRole("button", { name: "Steer current run", exact: true })).toBeEnabled();
      await page.reload();
      // Local checkbox state resets; the source Run's frozen intent must suffice.
      await expect(page.getByRole("checkbox", { name: "Read-only planning", exact: true })).not.toBeChecked();
      await page.getByPlaceholder("Ask AgentFlow anything...").fill(draft);
      if (planning) {
        await expect(queue).toBeDisabled();
        const response = await request.post(`${api}${path}`, { headers, data: { kind: "follow_up", run_id: id, content: "Execute implicitly", idempotency_key: "planning-rejection", mode: "single", agent_id: agent.id } });
        expect(response.status()).toBe(409);
        rejection = await response.json();
        expect(rejection).toMatchObject({ code: "input_plan_only" });
        expect(await read(path)).toEqual([]);
        await test.info().attach("planning-inbox-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
      } else {
        await expect(queue).toBeEnabled();
        await queue.click();
        await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
        await page.getByPlaceholder("Ask AgentFlow anything...").fill(draft);
      }
      await page.getByRole("button", { name: "Steer current run", exact: true }).click();
      await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
      expect((await request.post(`${api}/__fixture/release`)).status()).toBe(204);
      await expect.poll(async () => (await read(`/api/runs/${id}`)).status).toBe("completed");
      await expect.poll(async () => (await read(path)).find((item: { kind: string }) => item.kind === "steer")?.status).toBe("applied");
      if (!planning) {
        await expect.poll(async () => (await read(path)).find((item: { kind: string }) => item.kind === "follow_up")?.status).toBe("applied");
        const receipt = (await read(path)).find((item: { kind: string }) => item.kind === "follow_up");
        expect(receipt.applied_run_id).not.toBe(id);
        await expect.poll(async () => (await read(`/api/runs/${receipt.applied_run_id}`)).status).toBe("completed");
        successor = await read(`/api/runs/${receipt.applied_run_id}`);
      }
      await page.reload();
      const receipts = await read(path);
      expect(receipts.filter((item: { kind: string }) => item.kind === "follow_up")).toHaveLength(planning ? 0 : 1);
      const messages = await read(`/api/conversations/${source.conversation_id}/messages`);
      for (const receipt of receipts) expect(messages.filter((message: { id: string }) => message.id === receipt.id)).toHaveLength(1);
      const replay = await read(`/api/runs/${id}/replay`);
      expect(JSON.stringify(replay.run_events.filter((event: { type: string }) => event.type === "context.assembled"))).toContain(receipts.find((item: { kind: string }) => item.kind === "steer").id);
      const contracts = await read("/__fixture/contracts");
      expect(contracts.failures ?? []).toEqual([]);
      await test.info().attach("planning-inbox-evidence.json", { body: JSON.stringify({ schema: "planning-inbox-v1", workspace_id: workspace, agent_id: agent.id, input, planning, source: replay.run, snapshot: replay.runtime_snapshot, rejection, successor, receipts, messages, provider_contracts: contracts, checks: ["composer intent and restored source intent", "steering preserved", "planning enqueue rejected without receipt", "ordinary follow-up preserved", "Postgres reload"], limitations: ["deterministic provider, not live-model compliance", "legacy dispatch and atomic consumption covered by focused Go/Postgres tests", "explicit plan execution covered by production planning integration"] }, null, 2), contentType: "application/json" });
    } finally {
      await request.post(`${api}/__fixture/release`);
      await request.post(`${api}/api/runs/${id}/cancel`, { headers });
    }
  });
}
