import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { EpisodeReport } from "../../lib/api";
import { EpisodeReportPanel } from "./EpisodeReportPanel";

const getEpisodeReport = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", () => ({ getEpisodeReport }));
afterEach(() => { cleanup(); getEpisodeReport.mockReset(); });

const report = {
  run: { id: "one" }, agent: { name: "Frozen Agent" }, task: "Bound task", final_output: "Bound answer",
  steps: [], llm_calls: [], tool_calls: [], errors: [], retrievals: { memories: [], chunks: [] },
  verification: { status: "not_required", evidence: [], warnings: [] }
} as unknown as EpisodeReport;

function expand(container: HTMLElement) {
  const details = container.querySelector("details")!;
  act(() => { details.open = true; fireEvent(details, new Event("toggle")); });
}

// Failure inventory: closed panels do no I/O; errors stay local, retry works,
// and aborted/foreign responses cannot display evidence for another Run.
it("loads only on expansion and refreshes only on explicit request", async () => {
  getEpisodeReport.mockResolvedValue(report);
  const { container } = render(<EpisodeReportPanel runId="one" />);
  expect(getEpisodeReport).not.toHaveBeenCalled();
  expand(container);
  await screen.findByText("Bound task");
  expect(getEpisodeReport).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Export JSON" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Refresh report" }));
  await waitFor(() => expect(getEpisodeReport).toHaveBeenCalledTimes(2));
});

it("keeps an unavailable report local and permits retry", async () => {
  getEpisodeReport.mockRejectedValueOnce(new Error("Report unavailable")).mockResolvedValue(report);
  const { container } = render(<EpisodeReportPanel runId="one" />);
  expand(container);
  expect((await screen.findByRole("alert")).textContent).toContain("Report unavailable");
  fireEvent.click(screen.getByRole("button", { name: "Retry report" }));
  await screen.findByText("Bound task");
  expect(screen.queryByRole("alert")).toBeNull();
});

it("aborts on unmount and ignores a late response", async () => {
  let finish!: (value: EpisodeReport) => void;
  getEpisodeReport.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const old = render(<EpisodeReportPanel runId="one" />);
  expand(old.container);
  await waitFor(() => expect(getEpisodeReport).toHaveBeenCalledOnce());
  const signal = getEpisodeReport.mock.calls[0][1] as AbortSignal;
  old.unmount();
  render(<EpisodeReportPanel runId="two" />);
  await act(async () => finish(report));
  expect(signal.aborted).toBe(true);
  expect(screen.queryByText("Bound task")).toBeNull();
});

it("rejects report identity mismatch", async () => {
  getEpisodeReport.mockResolvedValue(report);
  const { container } = render(<EpisodeReportPanel runId="two" />);
  expand(container);
  expect((await screen.findByRole("alert")).textContent).toContain("another Run");
  expect(screen.queryByText("Bound task")).toBeNull();
});
