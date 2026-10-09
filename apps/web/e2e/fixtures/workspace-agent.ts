import { expect, type Page } from "@playwright/test";
import type { AgentConfigInput, AgentInfo } from "../../lib/api";
import type { components } from "../../lib/api-contract.gen";

// Local mode has no auth Session; resolve the real owner's default Workspace.
export async function defaultWorkspaceID(page: Page) {
  const response = await page.request.get("http://127.0.0.1:18080/api/workspaces");
  expect(response.ok()).toBe(true);
  const workspaces: components["schemas"]["Workspace"][] = await response.json();
  const defaults = workspaces.filter(workspace => workspace.is_default);
  expect(defaults).toHaveLength(1);
  return defaults[0].id;
}

// Chat requires an owned Agent; never depend on profiles left by another test.
export async function createWorkspaceChatAgent(page: Page, workspaceID: string, name: string, config: AgentConfigInput = {}) {
  const response = await page.request.post("http://127.0.0.1:18080/api/agents", {
    headers: { Origin: "http://127.0.0.1:13000", "X-Workspace-ID": workspaceID },
    data: { system_prompt: "Answer concisely.", tools: [], memory_enabled: false, retrieval_enabled: false, ...config, name }
  });
  expect(response.status()).toBe(201);
  const agent: AgentInfo = await response.json();
  expect(agent.workspace_id).toBe(workspaceID);
  expect(agent.is_template).toBe(false);
  await page.reload();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  // An existing staged Run owns its composer; select only in a fresh Chat.
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await expect(page.getByRole("link", { name: "View trace", exact: true })).toHaveCount(0);
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: /^Agent: / }).click();
  await page.getByRole("option", { name, exact: true }).click();
  await expect(page.getByRole("button", { name: `Agent: ${name}`, exact: true })).toBeVisible();
  return agent;
}
