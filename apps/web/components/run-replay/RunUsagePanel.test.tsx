import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { RunUsageLedger } from "../../lib/api";
import { RunUsagePanel } from "./RunUsagePanel";

afterEach(cleanup);

// Failure inventory: missing is not zero, cached/reasoning are subsets,
// unknown prices must not render as a free call, details survive serialized reload.
it("shows reported subsets and unknown usage separately after reload", () => {
  const ledger = JSON.parse(JSON.stringify({
    run_id: "run-1", budget: {},
    totals: { model_calls: 2, tool_calls: 0, prompt_tokens: 20, completion_tokens: 8, total_tokens: 28, estimated_cost_micros: 15, open_reservations: 0, cost_unknown_entries: 1 },
    entries: [
      { id: "a", run_id: "run-1", operation_id: "op-1", kind: "model.settlement", purpose: "primary", timestamp: "2026-10-02T00:00:00Z", model: "model", prompt_tokens: 10, completion_tokens: 4, breakdown: { source: "openai_details", cached_input_tokens: 0, reasoning_tokens: 2 }, cost_details: { status: "estimated", reason: "provider_usage", pricing: { source: "frozen-fixture", input_per_million_tokens_micros: 2000000, output_per_million_tokens_micros: 1000000, cached_input_per_million_tokens_micros: 500000 } }, estimated_cost_micros: 15 },
      { id: "b", run_id: "run-1", operation_id: "op-2", kind: "model.settlement", purpose: "primary", timestamp: "2026-10-02T00:00:01Z", model: "model", cost_details: { status: "unknown", reason: "pricing_unknown", pricing: { source: "run_budget", input_per_million_tokens_micros: 0, output_per_million_tokens_micros: 0 } } }
    ]
  })) as RunUsageLedger;
  render(<RunUsagePanel ledger={ledger} activeRuntimeMS={0} events={[]} />);
  fireEvent.click(screen.getByText("Model usage details"));
  expect(screen.getByText("0 / 10")).toBeTruthy();
  expect(screen.getByText("2 / 4")).toBeTruthy();
  expect(screen.getAllByText("Unknown").length).toBeGreaterThan(0);
  expect(screen.getByText(/1 call.*unpriced/)).toBeTruthy();
  expect(screen.getByText("frozen-fixture")).toBeTruthy();
});
