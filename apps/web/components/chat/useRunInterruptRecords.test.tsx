import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useRunInterruptRecords } from "./useRunInterruptRecords";

afterEach(cleanup);

// Failure inventory: unloaded != empty; rejected refresh cannot authorize a
// command; run/status changes, stale reads, same-render refresh and unmount
// must invalidate readiness even if the transport ignores AbortSignal.
it("retains same-Run records on failure but requires a successful current read", async () => {
  const load = vi.fn().mockResolvedValue([{ id: "record-1" }]);
  const { result } = renderHook(() => useRunInterruptRecords("run-1", "waiting_for_user", load, "Unavailable"));
  expect(result.current.ready).toBe(false);
  await waitFor(() => expect(result.current.ready).toBe(true));
  load.mockRejectedValue(new Error("read failed"));
  act(() => {
    result.current.refresh();
    expect(result.current.isReady()).toBe(false);
  });
  await waitFor(() => expect(result.current.error).toBe("read failed"));
  expect(result.current.items).toEqual([{ id: "record-1" }]);
  expect(result.current.ready).toBe(false);
  load.mockResolvedValue([]);
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.ready).toBe(true));
  expect(result.current.items).toEqual([]);
  expect(result.current.isReady("another-run", "waiting_for_user")).toBe(false);
});

it("rejects late results and failure across Run, status and unmount boundaries", async () => {
  const pending: Array<{ resolve: (items: unknown[]) => void; reject: (error: Error) => void; signal: AbortSignal }> = [];
  const load = vi.fn((_id: string, signal: AbortSignal) => new Promise<unknown[]>((resolve, reject) => pending.push({ resolve, reject, signal })));
  const { result, rerender, unmount } = renderHook(({ id, status }) => useRunInterruptRecords(id, status, load, "Unavailable"), {
    initialProps: { id: "run-1", status: "running" }
  });
  rerender({ id: "run-1", status: "waiting_for_user" });
  rerender({ id: "run-2", status: "waiting_for_user" });
  await act(async () => { pending[0].resolve([{ id: "old" }]); pending[1].reject(new Error("old failure")); });
  expect(result.current.items).toEqual([]);
  expect(result.current.error).toBe("");
  expect(result.current.ready).toBe(false);
  await act(async () => pending[2].resolve([]));
  expect(result.current.ready).toBe(true);
  act(() => result.current.refresh());
  unmount();
  expect(pending[3].signal.aborted).toBe(true);
  await act(async () => pending[3].resolve([{ id: "unmounted" }]));
});

it("does not mistake a previous status read or empty Run ID for current readiness", async () => {
  const load = vi.fn().mockResolvedValue([]);
  const { result, rerender } = renderHook(({ id, status }) => useRunInterruptRecords(id, status, load, "Unavailable"), {
    initialProps: { id: "run-1", status: "waiting_for_user" }
  });
  await waitFor(() => expect(result.current.ready).toBe(true));
  load.mockImplementation(() => new Promise(() => {}));
  rerender({ id: "run-1", status: "running" });
  rerender({ id: "run-1", status: "waiting_for_user" });
  expect(result.current.loading).toBe(true);
  expect(result.current.isReady()).toBe(false);
  rerender({ id: "", status: "" });
  expect(result.current.items).toEqual([]);
  expect(result.current.ready).toBe(false);
  expect(result.current.loading).toBe(false);
});
