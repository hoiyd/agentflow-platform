import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";

import { MessageCitations } from "./MarkdownContent";

it("separates Knowledge and Web sources and links only safe Web URLs", () => {
  render(<MessageCitations
    citations={[{ source_id: "S1", document_id: "doc-1", document_title: "Runbook", chunk_id: "chunk-1" }]}
    webCitations={[
      { source_id: "W1", title: "Official page", url: "https://example.com/", run_id: "run-1", tool_call_id: "call-1", tool_event_id: "event-1" },
      { source_id: "W2", title: "Unsafe page", url: "javascript:alert(1)", run_id: "run-1", tool_call_id: "call-2", tool_event_id: "event-2" }
    ]}
  />);

  expect(screen.getByText("Knowledge")).toBeTruthy();
  expect(screen.getAllByText("Web")).toHaveLength(2);
  expect(screen.getByRole("link", { name: "Official page" }).getAttribute("href")).toBe("https://example.com/");
  expect(screen.queryByRole("link", { name: "Unsafe page" })).toBeNull();
  expect(screen.getAllByRole("link", { name: "Tool event" })[0].getAttribute("href"))
    .toBe("/runs/run-1?event=event-1#run-event-detail");
});

it("links Knowledge read evidence to its Run Tool event without changing automatic sources", () => {
  const { container } = render(<MessageCitations citations={[
    { source_id: "S1", document_id: "doc-1", chunk_id: "chunk-1", document_title: "Automatic source" },
    { source_id: "S2", document_id: "doc-2", chunk_id: "chunk-2", document_title: "Read source", run_id: "run-2", tool_call_id: "call-2", tool_event_id: "event-2" },
  ]} />);
  expect(within(container).getAllByRole("link", { name: "Tool event" })).toHaveLength(1);
  expect(within(container).getByRole("link", { name: "Tool event" }).getAttribute("href"))
    .toBe("/runs/run-2?event=event-2#run-event-detail");
});
