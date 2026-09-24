import { act, renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { useCompletionVerification } from "./useCompletionVerification";

it("keeps the saved policy when a draft fails validation or is canceled", () => {
  const { result } = renderHook(() => useCompletionVerification());
  act(() => result.current.open());
  act(() => result.current.change({ enabled: true, textConstraints: { ...result.current.draft!.textConstraints, maximumCharacters: 1 } }));
  act(() => result.current.save());
  expect(result.current.settings.enabled).toBe(false);
  expect(result.current.error).not.toBe("");
  act(() => result.current.cancel());
  expect(result.current.draft).toBeNull();
  expect(result.current.settings.enabled).toBe(false);
});
