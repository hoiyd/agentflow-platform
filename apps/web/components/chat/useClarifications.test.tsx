import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useClarifications } from "./useClarifications";

const list = vi.hoisted(() => vi.fn());
vi.mock("../../lib/clarification-api", () => ({listClarifications:list}));
afterEach(() => { cleanup(); list.mockReset(); });

it("distinguishes unloaded, failed, loaded-empty and same-status refresh", async () => {
  let finish!: (items: unknown[]) => void;
  list.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const {result} = renderHook(() => useClarifications("run-1", "failed_recoverable"));
  expect(result.current.loading).toBe(true);
  await act(async () => finish([]));
  expect(result.current.loading).toBe(false);
  expect(result.current.error).toBe("");
  expect(result.current.items).toEqual([]);
  act(() => result.current.refresh());
  expect(result.current.loading).toBe(true);
  await act(async () => finish([]));
  expect(result.current.loading).toBe(false);
  list.mockRejectedValue(new Error("read failed"));
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.error).toBe("read failed"));
  expect(result.current.loading).toBe(false);
});

it("ignores late reads for an old status or Run even when they ignore abort", async () => {
  const finish: Array<(items: unknown[]) => void> = [];
  list.mockImplementation(() => new Promise(resolve => { finish.push(resolve); }));
  const {result, rerender} = renderHook(({id,status}) => useClarifications(id,status), {
    initialProps:{id:"run-1",status:"running"}
  });
  rerender({id:"run-1",status:"failed_recoverable"});
  rerender({id:"run-2",status:"failed_recoverable"});
  await act(async () => { finish[0]([]); finish[1]([]); });
  expect(result.current.loading).toBe(true);
  expect(result.current.items).toEqual([]);
  await act(async () => finish[2]([]));
  expect(result.current.loading).toBe(false);
});

it("does not reuse an old successful read when the Run returns to its previous status", async () => {
  list.mockResolvedValue([]);
  const {result,rerender}=renderHook(({status})=>useClarifications("run-1",status),{
    initialProps:{status:"failed_recoverable"}
  });
  await waitFor(()=>expect(result.current.ready).toBe(true));
  list.mockImplementation(()=>new Promise(()=>{}));
  rerender({status:"running"});
  rerender({status:"failed_recoverable"});
  expect(result.current.loading).toBe(true);
  expect(result.current.ready).toBe(false);
});
