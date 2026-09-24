import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { Conversation } from "../../lib/api";
import { useConversationWorkspace } from "./useConversationWorkspace";

const updateConversationTitle = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", () => ({ updateConversationTitle }));

it("keeps a newer title draft when an older save completes", async () => {
  let resolve!: (value: Conversation) => void;
  updateConversationTitle.mockReturnValue(new Promise<Conversation>((done) => { resolve = done; }));
  const { result } = renderHook(() => useConversationWorkspace());
  const first = conversation("first");
  const second = conversation("second");

  act(() => result.current.startEditingTitle(first));
  act(() => result.current.setConversationTitleDraft("updated first"));
  let save!: Promise<void>;
  act(() => { save = result.current.saveTitle(); });
  act(() => result.current.startEditingTitle(second));
  await act(async () => { resolve({ ...first, title: "updated first" }); await save; });

  expect(result.current.editingConversationId).toBe("second");
  expect(result.current.conversationTitleDraft).toBe("second");
});

it("does not show an old title save error in a newly selected editor", async () => {
  let reject!: (reason: Error) => void;
  updateConversationTitle.mockReturnValue(new Promise<Conversation>((_, fail) => { reject = fail; }));
  const { result } = renderHook(() => useConversationWorkspace());
  act(() => result.current.startEditingTitle(conversation("first")));
  let save!: Promise<void>;
  act(() => { save = result.current.saveTitle(); });
  act(() => result.current.startEditingTitle(conversation("second")));
  await act(async () => { reject(new Error("old failure")); await save; });

  expect(result.current.editingConversationId).toBe("second");
  expect(result.current.error).toBe("");
});

function conversation(id: string): Conversation {
  return { id, workspace_id: "default_workspace", title: id, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" };
}
