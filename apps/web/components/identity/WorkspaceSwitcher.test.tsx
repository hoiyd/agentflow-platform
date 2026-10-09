import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";
import type { Workspace } from "../../lib/workspaces";

afterEach(cleanup);

function setup() {
  const onChange = vi.fn();
  const workspaces = ["Default workspace", "Personal workspace", "A long workspace name that should wrap without clipping"].map((name, index) => ({ id: String(index + 1), name, status: "active" } as Workspace));
  render(<><WorkspaceSwitcher workspaces={workspaces} selected="1" onChange={onChange} /><button>Outside</button></>);
  return { onChange, trigger: screen.getByRole("button", { name: "Workspace: Default workspace" }) };
}

it("shows the current selection and switches only when a different option is confirmed", () => {
  const { onChange, trigger } = setup();
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(trigger);
  expect(screen.getByRole("listbox", { name: "Workspace" })).toBeTruthy();
  expect(screen.getByRole("option", { name: "Default workspace" }).getAttribute("aria-selected")).toBe("true");
  fireEvent.click(screen.getByRole("option", { name: "Default workspace" }));
  expect(onChange).not.toHaveBeenCalled();
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("option", { name: "Personal workspace" }));
  expect(onChange).toHaveBeenCalledExactlyOnceWith("2");
  expect(screen.queryByRole("listbox")).toBeNull();
});

it("supports arrow navigation without changing Workspace and Escape restores focus", () => {
  const { onChange, trigger } = setup();
  fireEvent.keyDown(trigger, { key: "ArrowDown" });
  const current = screen.getByRole("option", { name: "Default workspace" });
  const search = screen.getByRole("combobox", { name: "Search workspaces" });
  expect(document.activeElement).toBe(search);
  expect(search.getAttribute("aria-activedescendant")).toBe(current.id);
  fireEvent.keyDown(search, { key: "ArrowDown" });
  expect(search.getAttribute("aria-activedescendant")).toBe(screen.getByRole("option", { name: "Personal workspace" }).id);
  fireEvent.change(search, {target: {value: "long workspace"}});
  expect(screen.getAllByRole("option")).toHaveLength(1);
  fireEvent.keyDown(search, { key: "Escape" });
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
  fireEvent.blur(screen.getByRole("option", { name: "Default workspace" }), { relatedTarget: screen.getByRole("button", { name: "Outside" }) });
  expect(screen.queryByRole("listbox")).toBeNull();
});
