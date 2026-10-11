import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import Page from "../../app/page";

afterEach(cleanup);

function imageSource(name: string) {
  return new URL((screen.getByRole("img", { name }) as HTMLImageElement).src).pathname;
}

it("keeps the public product entry actionable with valid section links and recorded media", () => {
  render(<Page />);
  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("AgentFlow");
  for (const link of screen.getAllByRole("link")) {
    const href = link.getAttribute("href")!;
    if (href.startsWith("#")) expect(document.getElementById(href.slice(1))).not.toBeNull();
    else expect(href === "/" || href === "/workspace" || href.startsWith("https://github.com/hoiyd/agentflow-platform")).toBe(true);
  }
  for (const link of screen.getAllByRole("link", { name: "Open workspace" })) {
    expect(link.getAttribute("href")).toBe("/workspace");
  }
  const image = screen.getByRole("img", { name: "Multi-agent execution recording" });
  expect(imageSource("Multi-agent execution recording")).toBe("/demos/multi-agent.gif");
  expect(image.getAttribute("width")).toBe("2880");
  expect(image.getAttribute("height")).toBe("1800");
  expect(image.getAttribute("loading")).toBe("lazy");
  expect(screen.getByText("Recorded product demos")).toBeTruthy();
  expect(document.querySelector("video")).toBeNull();
});

it("switches between original GIF recordings and mounts only the selected image", () => {
  render(<Page />);
  fireEvent.click(screen.getByRole("button", { name: "Knowledge retrieval" }));
  expect(imageSource("Knowledge retrieval recording")).toBe("/demos/knowledge.gif");
  expect(screen.getByRole("button", { name: "Knowledge retrieval" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Completion verification" }));
  expect(imageSource("Completion verification recording")).toBe("/demos/verification.gif");
  expect(document.querySelectorAll(".home-demo-recording img")).toHaveLength(1);
});

it("uses a static source for reduced motion before hydration and after switching recordings", () => {
  render(<Page />);
  const source = () => document.querySelector(".home-demo-recording picture source")!;
  expect(source().getAttribute("media")).toBe("(prefers-reduced-motion: reduce)");
  expect(source().getAttribute("srcset")).toBe("/demos/multi-agent.webp");
  fireEvent.click(screen.getByRole("button", { name: "Knowledge retrieval" }));
  expect(source().getAttribute("srcset")).toBe("/demos/knowledge.webp");
});

it("pauses with a static image, resumes the GIF, and resets playback when switching", () => {
  render(<Page />);
  fireEvent.click(screen.getByRole("button", { name: "Pause animation" }));
  expect(imageSource("Multi-agent execution recording")).toBe("/demos/multi-agent.webp");
  fireEvent.click(screen.getByRole("button", { name: "Play animation" }));
  expect(imageSource("Multi-agent execution recording")).toBe("/demos/multi-agent.gif");
  fireEvent.click(screen.getByRole("button", { name: "Pause animation" }));
  fireEvent.click(screen.getByRole("button", { name: "Knowledge retrieval" }));
  expect(imageSource("Knowledge retrieval recording")).toBe("/demos/knowledge.gif");
  expect(screen.getByRole("button", { name: "Pause animation" })).toBeTruthy();
});

it("exposes the original recording on failure and clears the error only on a different selection", () => {
  render(<Page />);
  fireEvent.error(screen.getByRole("img", { name: "Multi-agent execution recording" }));
  expect(screen.getByRole("alert").textContent).toContain("Recording unavailable");
  expect(screen.getByRole("link", { name: "View original GIF" }).getAttribute("href")).toContain("apps/web/public/demos/multi-agent.gif");
  fireEvent.click(screen.getByRole("button", { name: "Multi-agent execution" }));
  expect(screen.getByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Knowledge retrieval" }));
  expect(screen.queryByRole("alert")).toBeNull();
});

it("separates runtime capabilities from deployment and evidence limitations", () => {
  render(<Page />);
  for (const name of ["Keep work moving", "Tools and trusted Skills", "Model controls and visibility", "Owner-scoped Workspaces"]) {
    expect(screen.getByRole("heading", { name })).toBeTruthy();
  }
  expect(screen.getByText(/Single-instance execution/)).toBeTruthy();
  expect(screen.getByText(/not proof of factual accuracy/)).toBeTruthy();
});
