import { expect, test } from "@playwright/test";

const api = "http://127.0.0.1:18080";
test.skip(process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires signed OIDC and disposable Postgres");

test("Agent menu searches a large catalog, wraps long names and executes the selected ID", async ({page}, info) => {
  await page.goto("/workspace");
  await page.getByRole("button", {name: "Sign in", exact: true}).click();
  await expect(page.getByRole("button", {name: /^Workspace: /})).toBeVisible();
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  const headers = {Origin: "http://127.0.0.1:13000", "X-Workspace-ID": session.personal_workspace};
  const longName = "Evidence research and technical writing specialist for extended platform architecture investigations";
  const agents = [];
  for (let index = 0; index < 12; index++) {
    const response = await page.request.post(`${api}/api/agents`, {headers, data: {
      name: index < 2 ? longName : `Catalog agent ${index}`,
      description: index === 1 ? "Distinct source-comparison specialist" : `Catalog entry ${index}`,
      system_prompt: "Answer concisely.", tools: [], skills: index < 2 ? ["fixture-method"] : [], memory_enabled: false, retrieval_enabled: false,
    }});
    expect(response.status()).toBe(201);
    agents.push(await response.json());
  }
  await page.reload();
  await page.getByRole("button", {name: "New conversation", exact: true}).click();
  await page.getByRole("button", {name: "Direct Single agent", exact: true}).click();
  const geometry = [];
  for (const width of [1280, 1920]) {
    await page.setViewportSize({width, height: 900});
    const trigger = page.getByRole("button", {name: /^Agent: /});
    await trigger.click();
    const list = page.getByRole("listbox", {name: "Agent", exact: true});
    expect(await list.getByRole("option").count()).toBeGreaterThanOrEqual(12);
    const popup = page.locator(".selection-popup");
    const bounds = await popup.boundingBox();
    const anchor = await trigger.boundingBox();
    expect(bounds!.y).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(anchor!.y);
    expect(await list.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true);
    const search = page.getByRole("combobox", {name: "Search agents"});
    await search.fill("source-comparison");
    const option = list.getByRole("option");
    await expect(option).toHaveCount(1);
    await expect(option).toContainText(longName);
    expect(await option.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await search.press("Enter");
    await expect(list).toBeHidden();
    await expect(trigger).toHaveAccessibleName(`Agent: ${longName}`);
    await expect(trigger).toBeFocused();
    const controls = [trigger, page.getByRole("button", {name: "Show agent description", exact: true}),
      page.getByRole("button", {name: "Skill: Automatic", exact: true}),
      page.getByRole("button", {name: "New agent", exact: true}),
      page.getByRole("button", {name: "Configure", exact: true}),
      page.getByRole("button", {name: "Verification Off", exact: true})];
    const controlBounds = [];
    for (const control of controls) {
      const rect = await control.boundingBox();
      expect(rect!.height).toBe(40);
      expect(Math.abs(rect!.y - (await trigger.boundingBox())!.y)).toBeLessThanOrEqual(1);
      expect(await control.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
      if (controlBounds.length) expect(rect!.x).toBeGreaterThanOrEqual(controlBounds.at(-1)!.x + controlBounds.at(-1)!.width);
      controlBounds.push(rect!);
    }
    const toolbar = page.locator(".agent-bar.single");
    expect((await toolbar.boundingBox())!.height).toBe(40);
    await controls[2].click();
    const skillList = page.getByRole("listbox", {name: "Skill", exact: true});
    await expect(skillList).toBeFocused();
    await expect(page.getByRole("combobox", {name: "Search skills"})).toHaveCount(0);
    const skillBounds = (await skillList.boundingBox())!;
    expect(skillBounds.y).toBeGreaterThanOrEqual(0);
    expect(skillBounds.x + skillBounds.width).toBeLessThanOrEqual(width);
    await skillList.press("ArrowDown");
    await skillList.press("Enter");
    await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("/skill:fixture-method ");
    await page.getByRole("button", {name: "Skill: fixture-method", exact: true}).click();
    await page.getByRole("option", {name: "Automatic", exact: true}).click();
    await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
    await trigger.click();
    await page.getByRole("combobox", {name: "Search agents"}).fill("Catalog entry 2");
    await page.getByRole("combobox", {name: "Search agents"}).press("Enter");
    await expect(page.getByRole("button", {name: "Skill: No skills", exact: true})).toBeDisabled();
    expect((await trigger.boundingBox())!.width).toBe(controlBounds[0].width);
    expect(await page.getByRole("button", {name: "Skill: No skills", exact: true}).getAttribute("title")).toBeNull();
    const noteTrigger = page.getByRole("group", {name: "Skill: No skills", exact: true});
    await noteTrigger.hover();
    const note = page.getByRole("tooltip");
    await expect(note).toContainText("No skills are assigned to this agent.");
    const noteBounds = (await note.boundingBox())!;
    expect(noteBounds.y).toBeGreaterThanOrEqual(0);
    expect(noteBounds.x).toBeGreaterThanOrEqual(0);
    expect(noteBounds.x + noteBounds.width).toBeLessThanOrEqual(width);
    expect((await trigger.boundingBox())!.width).toBe(controlBounds[0].width);
    await note.hover();
    await expect(note).toBeVisible();
    if (width === 1280) {
      const image = info.outputPath("no-skills-note.png");
      await note.screenshot({path: image});
      await info.attach("no-skills-note.png", {path: image, contentType: "image/png"});
    }
    await page.mouse.move(0, 0);
    await expect(note).toHaveCount(0);
    await noteTrigger.focus();
    await expect(note).toBeVisible();
    await noteTrigger.press("Escape");
    await expect(note).toHaveCount(0);
    await trigger.click();
    await page.getByRole("combobox", {name: "Search agents"}).fill("source-comparison");
    await page.getByRole("combobox", {name: "Search agents"}).press("Enter");
    await expect(page.getByRole("group", {name: "Skill: No skills", exact: true})).toHaveCount(0);
    await expect(page.getByRole("region", {name: "Agent description", exact: true})).toHaveCount(0);
    await controls[1].click();
    const description = page.getByRole("region", {name: "Agent description", exact: true});
    await expect(description).toContainText("Distinct source-comparison specialist");
    const expandedAnchor = (await trigger.boundingBox())!;
    expect((await description.boundingBox())!.y).toBeGreaterThan(expandedAnchor.y + expandedAnchor.height);
    await page.getByRole("button", {name: "Hide agent description", exact: true}).click();
    if (width === 1280) {
      const image = info.outputPath("agent-toolbar-desktop.png");
      await page.locator(".composer").screenshot({path: image});
      await info.attach("agent-toolbar-desktop.png", {path: image, contentType: "image/png"});
    }
    geometry.push({width, bounds, anchor, controlBounds});
  }
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("Agent selection evidence");
  await page.getByRole("button", {name: "Send message", exact: true}).click();
  await expect(page.getByText("Evidence saved.", {exact: true})).toBeVisible();
  const href = await page.getByRole("link", {name: "View trace"}).getAttribute("href");
  const replay = await (await page.request.get(`${api}/api${href}/replay`, {headers})).json();
  expect(replay.run.agent_id).toBe(agents[1].id);
  await info.attach("agent-selection-evidence.json", {body: JSON.stringify({
    store: "disposable-postgres", owner: session.user.id, workspace: session.personal_workspace,
    created_agents: agents.map(item => item.id), selected_agent: replay.run.agent_id,
    run: replay.run.id, geometry, checks: ["long duplicate names", "description search", "keyboard confirmation", "bounded list", "popup above composer", "40px aligned controls including bound Skill", "non-search Skill keyboard invocation and Automatic reset", "disabled Skill retains Agent picker width", "non-overlapping toolbar", "description disclosure below toolbar", "actual selected ID reaches runtime"],
    limitations: ["desktop only", "deterministic provider fixture"],
  }, null, 2), contentType: "application/json"});
});
