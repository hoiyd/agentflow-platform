import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("Skill invocation intent and dependency readiness survive persistence and staged execution", async ({ page, request }) => {
  test.skip(process.env.AGENTFLOW_SKILL_POLICY_TEST !== "1", "requires Skill policy packages");
  test.setTimeout(120_000);
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const agent = await createWorkspaceChatAgent(page, workspace, "Report reviewer", {
    tools: ["calculator"], skills: ["manual-review", "auto-review"],
    skill_policies: { "auto-review": "explicit_only" },
    routing_hints: { capabilities: ["report-review"], task_examples: ["skill-policy-gate"], exclusions: [] }
  });
  await page.getByRole("button", { name: "Configure", exact: true }).click();
  await expect(page.getByText("web_search: Credential unavailable", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Invocation for manual-review: Explicit only", exact: true })).toBeDisabled();
  await page.getByRole("checkbox", { name: "calculator", exact: true }).click();
  await expect(page.getByText("calculator: Not bound to Agent", { exact: true })).toHaveCount(2);
  await expect(page.getByRole("checkbox", { name: "manual-review", exact: true })).toBeChecked();
  await page.getByRole("checkbox", { name: "calculator", exact: true }).click();
  await expect(page.getByText("calculator: Ready", { exact: true })).toHaveCount(2);
  await page.getByRole("button", { name: "Cancel edit agent config" }).scrollIntoViewIfNeeded();
  await test.info().attach("skill-policy-config-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
  await page.getByRole("button", { name: "Cancel edit agent config" }).click();
  const invalid = await request.patch(`http://127.0.0.1:18080/api/agents/${agent.id}`, { headers, data: { skills: ["missing-search"] } });
  expect(invalid.status()).toBe(400);
  expect(await invalid.text()).toContain("skill_dependency_missing");
  const runs = [];
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await page.getByRole("checkbox", { name: "Allow calculator in this Workspace", exact: true }).click();
  await expect(page.getByRole("checkbox", { name: "Allow calculator in this Workspace", exact: true })).not.toBeChecked();
  await page.getByRole("button", { name: "Chat", exact: true }).click();
  await page.getByRole("button", { name: "Skill: No explicit skill", exact: true }).click();
  await expect(page.getByRole("option", { name: /manual-review/ })).toHaveAttribute("aria-disabled", "true");
  await expect(page.getByText("calculator: Disabled in Workspace", { exact: true })).toHaveCount(2);
  await page.getByRole("listbox", { name: "Skill", exact: true }).press("ArrowDown");
  await page.getByRole("listbox", { name: "Skill", exact: true }).press("Enter");
  await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await page.getByRole("checkbox", { name: "Allow calculator in this Workspace", exact: true }).click();
  await expect(page.getByRole("checkbox", { name: "Allow calculator in this Workspace", exact: true })).toBeChecked();
  await page.getByRole("button", { name: "Chat", exact: true }).click();
  for (const [mode, explicit, marker] of [
    ["Single agent", false, "implicit"], ["Multi-agent", false, "implicit"],
    ["Single agent", true, "explicit"], ["Multi-agent", true, "explicit"], ["Bounded loop", true, "explicit"],
    ["Single agent", false, "binding"]
  ] as const) {
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: new RegExp(mode) }).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(`${explicit ? "/skill:manual-review " : ""}skill-policy-gate-${marker}: review a report`);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("report-review");
      await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
    }
    await expect(page.locator(".message.assistant").last()).toContainText(mode === "Single agent" && !explicit ? "Explicit invocation required; no method loaded." : "Review includes verified facts and open questions.", { timeout: 45000 });
    const id = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
    await expect.poll(async () => {
      const response = await request.get(`http://127.0.0.1:18080/api/runs/${id}`, { headers });
      expect(response.ok()).toBe(true);
      return (await response.json()).status;
    }).toBe("completed");
    const response = await request.get(`http://127.0.0.1:18080/api/runs/${id}/replay`, { headers });
    expect(response.ok()).toBe(true);
    const replay = await response.json();
    expect(replay.run.status).toBe("completed");
    const evidence = JSON.stringify(replay.projection.skill_evidence);
    expect(evidence.includes('"activation":"explicit"')).toBe(explicit);
    const events = replay.run_events as Array<{type: string; payload: {manifest?: {entries: Array<{source: string; selected: boolean; activation?: string}>}; error_code?: string}}>;
    const included = events.flatMap(event => event.payload.manifest?.entries ?? []).filter(entry => entry.source === "skill_instructions" && entry.selected);
    expect(included.length > 0).toBe(explicit);
    if (explicit) expect(included.some(entry => entry.activation === "explicit")).toBe(true);
    else expect(JSON.stringify(events)).toContain("skill_invocation_denied");
    runs.push({ mode, marker, explicit, task: `${explicit ? "/skill:manual-review " : ""}skill-policy-gate-${marker}: review a report`, run_id: id, snapshot: replay.runtime_snapshot, events, evidence: replay.projection.skill_evidence });
    await page.getByRole("link", { name: "View trace", exact: true }).click();
    await page.reload();
    await expect(page.getByRole("heading", { name: "Run replay", exact: true })).toBeVisible();
    await page.getByRole("link", { name: "Back to chat", exact: true }).click();
  }
  // Change the binding through the real dialog; new Runs change behavior while
  // the already recorded Runs retain their original frozen policy.
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: /Single agent/ }).click();
  await page.getByRole("button", { name: "Configure", exact: true }).click();
  await page.getByRole("button", { name: "Invocation for auto-review: Explicit only", exact: true }).click();
  await page.getByRole("option", { name: "Automatic", exact: true }).click();
  await page.getByRole("button", { name: "Save Config", exact: true }).click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: /Single agent/ }).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("skill-policy-gate-auto: review a report");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.locator(".message.assistant").last()).toContainText("Review includes verified facts and open questions.");
  const autoID = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
  await expect.poll(async () => (await (await request.get(`http://127.0.0.1:18080/api/runs/${autoID}`, { headers })).json()).status).toBe("completed");
  const autoResponse = await request.get(`http://127.0.0.1:18080/api/runs/${autoID}/replay`, { headers });
  const autoReplay = await autoResponse.json();
  expect(autoReplay.run.status).toBe("completed");
  expect(JSON.stringify(autoReplay.projection.skill_evidence)).toContain('"activation":"model"');
  const oldResponse = await request.get(`http://127.0.0.1:18080/api/runs/${runs[0].run_id}/replay`, { headers });
  expect((await oldResponse.json()).runtime_snapshot.agent.skill_policies["auto-review"]).toBe("explicit_only");
  runs.push({ mode: "Single agent", marker: "auto", explicit: false, task: "skill-policy-gate-auto: review a report", run_id: autoID, snapshot: autoReplay.runtime_snapshot, events: autoReplay.run_events, evidence: autoReplay.projection.skill_evidence });
  const readiness = await (await request.get("http://127.0.0.1:18080/api/skills", { headers })).json();
  await test.info().attach("skill-policy-evidence.json", { body: JSON.stringify({ schema: "skill-policy-browser-v1", workspace_id: workspace, agent, readiness, runs, checks: ["real UI/API/Postgres", "explicit vs denied automatic", "generated Planner prefix is not user intent", "frozen binding policy", "all modes", "reload"], limitations: ["deterministic provider, not live method quality", "no shell or implicit Tool grants"] }, null, 2), contentType: "application/json" });
});
