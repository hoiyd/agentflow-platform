import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_INBOX_EDGE_TEST !== "1", "requires bounded-budget and compaction fixtures");
const api = "http://127.0.0.1:18080";

for (const boundary of ["compaction", "budget"]) {
  test(`durable input at ${boundary} boundary`, async ({ page, request }) => {
    const read = async (path: string) => {
      const response = await request.get(`${api}${path}`);
      expect(response.ok()).toBe(true);
      return response.json();
    };
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
    // Create a real Conversation through ordinary Chat before adding old history.
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("Initialize boundary test");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const firstID = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!.split("/").at(-1)!;
    const first = await read(`/api/runs/${firstID}`);
    if (boundary === "compaction") {
      expect((await request.post(`${api}/__fixture/inbox/history/${first.conversation_id}`)).status()).toBe(204);
    }
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(boundary === "budget" ? "stream-gate inbox-budget" : "Answer the current question");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect.poll(async () => page.getByRole("link", { name: "View trace" }).getAttribute("href")).not.toContain(firstID);
    const id = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!.split("/").at(-1)!;
    try {
      if (boundary === "compaction") {
        await expect.poll(async () => (await read(`/api/runs/${id}/replay`)).run_events.some((event: { type: string }) => event.type === "context.compaction_started")).toBe(true);
      } else {
        await expect(page.locator(".message.assistant").last()).toContainText("First token");
      }
      await page.getByPlaceholder("Ask AgentFlow anything...").fill(boundary === "budget" ? "inbox-budget: continue calculating" : "STEERING_CANARY: preserve this original constraint");
      await page.getByRole("button", { name: "Steer current run", exact: true }).click();
      await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
      await page.getByPlaceholder("Ask AgentFlow anything...").fill("A fresh independent task");
      await page.getByRole("button", { name: "Queue follow-up", exact: true }).click();
      await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
      expect((await request.post(`${api}/__fixture/release`)).status()).toBe(204);
      await expect.poll(async () => (await read(`/api/runs/${id}`)).status).toBe(boundary === "budget" ? "failed" : "completed");
      const path = `/api/conversations/${first.conversation_id}/inputs`;
      if (boundary === "compaction") {
        await expect.poll(async () => (await read(path)).filter((item: { status: string }) => item.status === "applied").length).toBe(2);
      }
      const receipts = await read(path);
      const steer = receipts.find((item: { kind: string }) => item.kind === "steer");
      const followup = receipts.find((item: { kind: string }) => item.kind === "follow_up");
      expect(steer.status).toBe("applied");
      const replay = await read(`/api/runs/${id}/replay`);
      expect(JSON.stringify(replay.run_events.filter((event: { type: string }) => event.type === "context.assembled"))).toContain(steer.id);
      if (boundary === "budget") {
        expect(replay.run.error).toContain("budget");
        expect(followup.status).toBe("queued");
        expect(followup.applied_run_id).toBe("");
      } else {
        expect(replay.run_events.some((event: { type: string }) => event.type === "context.compaction_completed")).toBe(true);
      }
      await test.info().attach("inbox-boundary-evidence.json", {
        body: JSON.stringify({ schema: "durable-input-boundary-v1", boundary, run: replay.run, receipts, events: replay.run_events, limitations: ["controlled provider, real production runtime and disposable Postgres", "does not measure live-model instruction compliance"] }, null, 2),
        contentType: "application/json"
      });
    } finally {
      await request.post(`${api}/__fixture/release`);
      await request.post(`${api}/api/runs/${id}/cancel`);
    }
  });
}
