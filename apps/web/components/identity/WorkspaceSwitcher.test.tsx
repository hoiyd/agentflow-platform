import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";

afterEach(cleanup);

function setup() {
  const onChange = vi.fn();
  render(<><WorkspaceSwitcher workspaces={["default_workspace", "personal-id", "A long workspace name that should wrap without clipping"]} selected="default_workspace" personalWorkspace="personal-id" onChange={onChange} /><button>Outside</button></>);
  return { onChange, trigger: screen.getByRole("button", { name: "Workspace: default_workspace" }) };
}

it("shows the current selection and switches only when a different option is confirmed", () => {
  const { onChange, trigger } = setup();
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger);
  expect(screen.getByRole("listbox", { name: "Workspace" })).toBeTruthy();
  expect(screen.getByRole("option", { name: "default_workspace" }).getAttribute("aria-selected")).toBe("true");
  fireEvent.click(screen.getByRole("option", { name: "default_workspace" }));
  expect(onChange).not.toHaveBeenCalled();
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("option", { name: "Personal workspace" }));
  expect(onChange).toHaveBeenCalledExactlyOnceWith("personal-id");
  expect(screen.queryByRole("listbox")).toBeNull();
});

it("supports arrow navigation without changing Workspace and Escape restores focus", () => {
  const { onChange, trigger } = setup();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  const current = screen.getByRole("option", { name: "default_workspace" });
  expect(document.activeElement).toBe(current);
  fireEvent.keyDown(current, { key: "ArrowDown" });
  expect(document.activeElement).toBe(screen.getByRole("option", { name: "Personal workspace" }));
  fireEvent.keyDown(document.activeElement!, { key: "End" });
  expect(document.activeElement?.textContent).toContain("A long workspace name");
  fireEvent.keyDown(document.activeElement!, { key: "Home" });
  expect(document.activeElement).toBe(current);
  fireEvent.keyDown(current, { key: "Escape" });
  expect(document.activeElement).toBe(trigger);
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(onChange).not.toHaveBeenCalled();
});

it("dismisses on outside click or focus leaving the selector", () => {
  const { trigger } = setup();
  fireEvent.click(trigger);
  fireEvent.pointerDown(screen.getByRole("button", { name: "Outside" }));
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(trigger);
  fireEvent.blur(screen.getByRole("option", { name: "default_workspace" }), { relatedTarget: screen.getByRole("button", { name: "Outside" }) });
  expect(screen.queryByRole("listbox")).toBeNull();
});
