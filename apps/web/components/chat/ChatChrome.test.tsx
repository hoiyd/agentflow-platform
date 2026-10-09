import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Sidebar, ToolsPanel, type APIConnectionStatus } from "./ChatChrome";

afterEach(cleanup);

describe("Sidebar API status", () => {
  it.each([
    ["checking", "API checking", "checking"],
    ["connected", "API connected", "online"],
    ["unavailable", "API unavailable", "offline"]
  ] as const)("renders %s state", (status, label, detail) => {
    renderSidebar(status);

    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getByText(detail)).toBeTruthy();
  });
});

describe("ToolsPanel availability", () => {
  it("keeps a persisted Workspace grant distinct from an operator-disabled Tool", () => {
    render(<ToolsPanel error="" onToggle={vi.fn()} updatingTool="" tools={[{
      name: "calculator", description: "Calculate", parameters: {}, enabled: false,
      workspace_enabled: true, service_enabled: false, excluded_reason: "service_disabled", config_revision: 3
    }]} />);
    expect(screen.getByRole("checkbox")).toHaveProperty("checked", true);
    expect(screen.getByRole("checkbox")).toHaveProperty("disabled", true);
    expect(screen.getByText("Disabled by operator")).toBeTruthy();
  });
  it("allows the owner to turn on a Workspace-disabled service Tool", () => {
    const toggle = vi.fn();
    const tool = {name: "calculator", description: "Calculate", parameters: {}, enabled: false, workspace_enabled: false, service_enabled: true, excluded_reason: "workspace_disabled"};
    render(<ToolsPanel error="" onToggle={toggle} updatingTool="" tools={[tool]} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(toggle).toHaveBeenCalledWith(tool);
    expect(screen.getByText("Disabled in this Workspace")).toBeTruthy();
  });
	 it("shows a disabled sandbox boundary without offering a misleading toggle", () => {
	   render(<ToolsPanel error="" onToggle={vi.fn()} updatingTool="" tools={[{
	     name: "sandbox_command", description: "Isolated scratch commands", parameters: {}, enabled: false,
	     unavailable_reason: "sandbox_disabled"
	   }]} />);
	   expect(screen.getByText("Sandbox disabled by operator")).toBeTruthy();
	   expect(screen.getByRole("checkbox")).toHaveProperty("disabled", true);
	 });
  it("distinguishes enabled search from missing credentials", () => {
    render(<ToolsPanel error="" onToggle={vi.fn()} updatingTool="" tools={[{
      name: "web_search", description: "Search the web", parameters: {}, enabled: true,
      unavailable_reason: "credential_unavailable"
    }]} />);

    expect(screen.getByText("Credential unavailable")).toBeTruthy();
    expect(screen.getByRole("checkbox")).toHaveProperty("checked", true);
  });
});

function renderSidebar(apiConnectionStatus: APIConnectionStatus) {
  const noop = vi.fn();
  render(
    <Sidebar
      activeId=""
      apiConnectionStatus={apiConnectionStatus}
      conversations={[]}
      isBusy={false}
      isCollapsed={false}
      isOpen={false}
      onCollapseChange={noop}
      onDeleteConversation={noop}
      onNewConversation={noop}
      onOpenConversation={noop}
      onOpenChange={noop}
      onViewChange={noop}
      onViewRefresh={noop}
      view="chat"
    />
  );
}
