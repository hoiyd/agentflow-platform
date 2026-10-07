import type { ChatEvent, ToolProgress } from "./api-types.ts";

type ProgressEvent = Extract<ChatEvent, { type: "tool_progress" }>;
const sameCall = (a: ToolProgress, b: Pick<ToolProgress, "turn_id" | "tool_call_id">) =>
  a.turn_id === b.turn_id && a.tool_call_id === b.tool_call_id;

export function applyToolProgress(items: ToolProgress[], event: ProgressEvent, runId: string): ToolProgress[] {
  if (event.run_id !== runId) return items;
  const previous = items.find(item => sameCall(item, event));
  if (previous && previous.sequence >= event.sequence) return items;
  if (!previous && !event.update) return items;
  const next: ToolProgress = { ...previous, ...event.update, phase: event.update?.phase ?? previous!.phase,
    run_id: event.run_id, turn_id: event.turn_id, stage_id: event.stage_id,
    tool_call_id: event.tool_call_id, tool_name: event.tool_name, status: event.status, sequence: event.sequence };
  return [...items.filter(item => !sameCall(item, next)), next].slice(-32);
}

export function restoreToolProgress(current: ToolProgress[], saved: ToolProgress[], runId: string): ToolProgress[] {
  const result = current.filter(item => item.run_id === runId);
  for (const item of saved) {
    const index = result.findIndex(previous => sameCall(previous, item));
    if (index < 0) result.push(item);
    else if (result[index].sequence <= item.sequence) result[index] = item;
  }
  return result.sort((a, b) => a.sequence - b.sequence).slice(-32);
}
