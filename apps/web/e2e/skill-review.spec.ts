import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("reviewed package changes require renewed approval without replacing frozen evidence", async ({ page, request }) => {
  test.skip(process.env.AGENTFLOW_SKILL_POLICY_TEST !== "1", "requires isolated Skill fixtures");
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const agent = await createWorkspaceChatAgent(page, workspace, "Reviewed report method", {
    tools: ["calculator"], skills: ["auto-review"],
    routing_hints: { capabilities: ["report-review"], task_examples: ["skill-policy-gate"], exclusions: [] }
  });
  const api = "http://127.0.0.1:18080";
  const control = async (action: string) => {
    expect((await request.post(`${api}/__fixture/skills/review`, { data: { action } })).status()).toBe(204);
  };
  try {
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("skill-policy-gate-auto: review a report");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.locator(".message.assistant").last()).toContainText("Review includes verified facts and open questions.");
    const run = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
    const before = await (await request.get(`${api}/api/runs/${run}/replay`, { headers })).json();

    await control("change");
    const rejected = await request.patch(`${api}/api/agents/${agent.id}`, { headers, data: { skills: ["auto-review"] } });
    expect(rejected.status()).toBe(400);
    expect(await rejected.text()).toContain("skill_review_changed");
    const deniedRun = await request.post(`${api}/api/chat`, {
      headers, data: { agent_id: agent.id, mode: "single", message: "skill-policy-gate-auto: unreviewed content must not execute" }
    });
    // Chat has opened SSE already: rejection is the typed stream error, not HTTP 400.
    expect(deniedRun.headers()["content-type"]).toContain("text/event-stream");
    const deniedBody = await deniedRun.text();
    expect(deniedBody).toContain("skill_review_changed");
    expect(deniedBody).not.toContain("POLICY_METHOD_BODY");
    const saved = await (await request.get(`${api}/api/agents/${agent.id}`, { headers })).json();
    expect(saved.skills).toContain("auto-review");
    await page.reload();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: /Single agent/ }).click();
    await page.getByRole("button", { name: "Configure", exact: true }).click();
    await expect(page.getByText("Package changed; operator review required", { exact: true })).toBeVisible();
    await test.info().attach("skill-review-blocked.png", { body: await page.screenshot(), contentType: "image/png" });
    await page.getByRole("button", { name: "Cancel edit agent config" }).click();

    await control("approve");
    const accepted = await request.patch(`${api}/api/agents/${agent.id}`, { headers, data: { skills: ["auto-review"] } });
    expect(accepted.ok()).toBe(true);
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: /Multi-agent/ }).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("skill-policy-gate-auto: review a report with the updated method");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.getByRole("button", { name: "Approve & Continue", exact: true })).toBeVisible();
    const paused = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
    const pausedReplay = await (await request.get(`${api}/api/runs/${paused}/replay`, { headers })).json();

    await control("revoke");
    const revoked = await request.patch(`${api}/api/agents/${agent.id}`, { headers, data: { skills: ["auto-review"] } });
    expect(revoked.status()).toBe(400);
    await page.reload();
    await page.getByText("Routing requirements", { exact: true }).click();
    await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("report-review");
    await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
    await expect(page.locator(".message.assistant").last()).toContainText("Review includes verified facts and open questions.");
    const continued = await (await request.get(`${api}/api/runs/${paused}/replay`, { headers })).json();
    expect(continued.runtime_snapshot).toEqual(pausedReplay.runtime_snapshot);
    const after = await (await request.get(`${api}/api/runs/${run}/replay`, { headers })).json();
    expect(after.runtime_snapshot).toEqual(before.runtime_snapshot);
    await test.info().attach("skill-review-evidence.json", {
      body: JSON.stringify({
        schema: "skill-review-gate-v1", workspace_id: workspace, run_id: run, continued_run_id: paused,
        original_snapshot: before.runtime_snapshot, continued_snapshot: continued.runtime_snapshot,
        checks: [
          "real Skill execution", "changed package rejected", "saved binding preserved", "readiness UI",
          "approval reload", "revocation", "frozen Replay unchanged",
          "paused Run reload/Continue after revocation uses frozen package"
        ],
        limitations: [
          "deterministic provider", "canonical runtime-content hash, not whole-tree signature", "operator trust",
          "Continue at plan approval, not arbitrary interrupted Single Resume"
        ]
      }, null, 2),
      contentType: "application/json"
    });
  } finally {
    await control("approve");
    await request.delete(`${api}/api/agents/${agent.id}`, { headers });
  }
});
