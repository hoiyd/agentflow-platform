import { cleanup, render, screen } from "@testing-library/react";
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
