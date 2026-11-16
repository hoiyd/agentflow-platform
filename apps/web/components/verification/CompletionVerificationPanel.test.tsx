import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { DEFAULT_COMPLETION_VERIFICATION } from "../../lib/verification";
import { CompletionVerificationPanel } from "./CompletionVerificationPanel";

afterEach(cleanup);

it("offers only the six supported verifiers, without host command configuration", () => {
  render(<CompletionVerificationPanel draft={DEFAULT_COMPLETION_VERIFICATION} error="" onCancel={vi.fn()} onChange={vi.fn()} onSave={vi.fn()} />);
  expect(screen.queryByRole("tab", { name: "Command" })).toBeNull();
  expect(screen.getAllByRole("tab")).toHaveLength(6);
  expect(screen.getByRole("tab", { name: "HTTP" })).toBeTruthy();
});
