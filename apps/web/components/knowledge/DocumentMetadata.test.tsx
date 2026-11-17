import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { DocumentMetadata } from "./DocumentMetadata";
import { documentFormat, documentFilename } from "./knowledgeFormatters";

afterEach(cleanup);

it("renders serialized null metadata as empty without losing document identity", () => {
  render(<DocumentMetadata label="Document metadata" metadata={null} />);
  expect(screen.getByRole("table", { name: "Document metadata" }).querySelector("tbody")?.children.length).toBe(0);
  const document = { id: "doc", title: "Runbook", metadata: null, source_type: "text", source_uri: "runbook.md", index_identity: { chunker_version: "v1", embedding_provider: "fixture", embedding_model: "fixture", embedding_dimensions: 2 }, created_at: "", updated_at: "" };
  expect(documentFormat(document)).toBe("text");
  expect(documentFilename(document)).toBe("runbook.md");
});
