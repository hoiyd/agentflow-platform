import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Sidebar, type APIConnectionStatus } from "./ChatChrome";

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
