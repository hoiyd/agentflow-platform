import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SelectionMenu } from "./SelectionMenu";

afterEach(cleanup);

// Failure inventory: long/duplicate labels retain their IDs; filtering or an
// empty result must not select; dismissal, disabled state and changed scope
// must not retain a stale popup; keyboard confirmation must restore focus.
const options = [
  { value: "private", label: "Experienced PS5 Game Player with a long and descriptive Agent name", description: "Research games and compare sources." },
  { value: "template", label: "Experienced PS5 Game Player", annotation: "Template" },
  { value: "writer", label: "Article writer", description: "Writes technical articles." },
];

it("filters by name and description, renders full labels and keeps duplicate names distinct", () => {
  const onChange = vi.fn();
  render(<SelectionMenu label="Agent" value="private" options={[...options, {value: "copy", label: options[0].label}]} onChange={onChange} placement="above" />);
  fireEvent.click(screen.getByRole("button", {name: `Agent: ${options[0].label}`}));
  expect(screen.getAllByRole("option")).toHaveLength(4);
  expect(screen.getByText("Template")).toBeTruthy();
  const search = screen.getByRole("combobox", {name: "Search agents"});
  expect(document.activeElement).toBe(search);
  fireEvent.change(search, {target: {value: "  TECHNICAL  "}});
  expect(screen.getAllByRole("option")).toHaveLength(1);
  fireEvent.click(screen.getByRole("option", {name: /Article writer/}));
  expect(onChange).toHaveBeenCalledExactlyOnceWith("writer");
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(screen.getByRole("button"));
  fireEvent.click(screen.getByRole("button"));
  expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("");
  expect(screen.getAllByRole("option")).toHaveLength(4);
});

it("names template search independently of the Copy from field label", () => {
  render(<SelectionMenu label="Copy from" optionsLabel="templates" value="" options={options} onChange={vi.fn()} />);
  fireEvent.click(screen.getByRole("button"));
  const search = screen.getByRole("combobox", {name: "Search templates"});
  fireEvent.change(search, {target: {value: "missing"}});
  expect(screen.getByRole("status").textContent).toBe("No templates found");
});

// Non-search mode must focus its listbox, retain keyboard selection/dismissal,
// and close rather than leave an interactive popup when disabled.
it("supports a non-searchable menu with listbox keyboard focus and disabled reset", () => {
  const onChange = vi.fn();
  const view = render(<SelectionMenu label="Skill" value="private" options={options} onChange={onChange} searchable={false} />);
  const trigger = screen.getByRole("button", {name: /^Skill:/});
  fireEvent.click(trigger);
  expect(screen.queryByRole("combobox")).toBeNull();
  const listbox = screen.getByRole("listbox", {name: "Skill"});
  expect(document.activeElement).toBe(listbox);
  fireEvent.keyDown(listbox, {key: "ArrowDown"});
  expect(document.getElementById(listbox.getAttribute("aria-activedescendant")!)?.textContent).toContain("Experienced PS5 Game Player");
  fireEvent.keyDown(listbox, {key: "Enter", isComposing: true});
  expect(onChange).not.toHaveBeenCalled();
  fireEvent.keyDown(listbox, {key: "Enter"});
  expect(onChange).toHaveBeenCalledExactlyOnceWith("template");
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("listbox"), {key: "Escape"});
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("listbox"), {key: "ArrowUp"});
  fireEvent.keyDown(screen.getByRole("listbox"), {key: " "});
  expect(onChange).toHaveBeenLastCalledWith("writer");
  fireEvent.click(trigger);
  view.rerender(<SelectionMenu label="Skill" value="private" options={options} onChange={onChange} searchable={false} disabled />);
  expect(screen.queryByRole("listbox")).toBeNull();
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
});

it("navigates filtered options without selecting until Enter, and handles empty results", () => {
  const onChange = vi.fn();
  render(<SelectionMenu label="Agent" value="private" options={options} onChange={onChange} />);
  const trigger = screen.getByRole("button", {name: /^Agent:/});
  fireEvent.keyDown(trigger, {key: "ArrowDown"});
  const search = screen.getByRole("combobox");
  fireEvent.keyDown(search, {key: "Enter", isComposing: true});
  expect(onChange).not.toHaveBeenCalled();
  expect(screen.getByRole("listbox")).toBeTruthy();
  fireEvent.keyDown(search, {key: "ArrowUp"});
  expect(document.getElementById(search.getAttribute("aria-activedescendant")!)?.textContent).toContain("Article writer");
  expect(onChange).not.toHaveBeenCalled();
  fireEvent.change(search, {target: {value: "unknown agent"}});
  expect(screen.getByRole("status").textContent).toBe("No agents found");
  expect(search.hasAttribute("aria-activedescendant")).toBe(false);
  fireEvent.keyDown(search, {key: "ArrowDown"});
  fireEvent.keyDown(search, {key: "Enter"});
  expect(onChange).not.toHaveBeenCalled();
  fireEvent.keyDown(search, {key: "Escape"});
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(trigger);
  fireEvent.click(trigger);
  fireEvent.change(screen.getByRole("combobox"), {target: {value: "article"}});
  fireEvent.keyDown(screen.getByRole("combobox"), {key: "Enter"});
  expect(onChange).toHaveBeenCalledExactlyOnceWith("writer");
});

it("dismisses outside or on Tab, ignores the current option, and resets when disabled or scope changes", () => {
  const onChange = vi.fn();
  const view = render(<><SelectionMenu label="Agent" value="private" options={options} onChange={onChange} /><button>Outside</button></>);
  const trigger = screen.getByRole("button", {name: /^Agent:/});
  fireEvent.click(trigger);
  fireEvent.click(screen.getByRole("option", {name: new RegExp(options[0].label)}));
  expect(onChange).not.toHaveBeenCalled();
  fireEvent.click(trigger);
  fireEvent.pointerDown(screen.getByText("Outside"));
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(trigger);
  fireEvent.blur(screen.getByRole("combobox"), {relatedTarget: screen.getByText("Outside")});
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(trigger);
  view.rerender(<SelectionMenu label="Agent" value="private" options={options} onChange={onChange} disabled />);
  expect(screen.queryByRole("listbox")).toBeNull();
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
  view.rerender(<SelectionMenu label="Agent" value="private" options={options} onChange={onChange} />);
  expect(screen.queryByRole("listbox")).toBeNull();
  fireEvent.click(screen.getByRole("button"));
  view.rerender(<SelectionMenu label="Agent" value="writer" options={options} onChange={onChange} />);
  expect(screen.queryByRole("listbox")).toBeNull();
  view.rerender(<SelectionMenu label="Agent" value="" options={[]} onChange={onChange} />);
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
});
