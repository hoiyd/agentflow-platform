import { expect, type Page } from "@playwright/test";
import type { AgentInfo } from "../../lib/api";

// Chat requires an owned Agent; never depend on profiles left by another test.
export async function createWorkspaceChatAgent(page: Page, workspaceID: string, name: string) {
  const response = await page.request.post("http://127.0.0.1:18080/api/agents", {
    headers: { Origin: "http://127.0.0.1:13000", "X-Workspace-ID": workspaceID },
    data: { name, system_prompt: "Answer concisely.", tools: [], memory_enabled: false, retrieval_enabled: false }
  });
  expect(response.status()).toBe(201);
  const agent: AgentInfo = await response.json();
  expect(agent.workspace_id).toBe(workspaceID);
  expect(agent.is_template).toBe(false);
  await page.reload();
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: /^Agent: / }).click();
  await page.getByRole("option", { name, exact: true }).click();
  await expect(page.getByRole("button", { name: `Agent: ${name}`, exact: true })).toBeVisible();
  return agent;
}
