import { APIError, apiRequest, stringValue, numberValue } from "./api-client.ts";
import type { ChatEvent, ContractSchemas, RunEvent, RunProjectionSnapshot } from "./api-types.ts";

type RunObservationOptions = {
  afterSequence?: number;
  signal?: AbortSignal;
  onEvent: (event: ChatEvent, sequence: number, replayed: boolean) => void;
  onSnapshot: (snapshot: RunProjectionSnapshot) => void;
};

export async function readChatEventStream(response: Response, onEvent: (event: ChatEvent) => void) {
  await readSSE(response, ({ data }) => {
    const decoded = JSON.parse(data) as ChatEvent | RunEvent;
    onEvent(projectRunEvent(decoded));
  });
}

export async function observeRunEvents(runId: string, options: RunObservationOptions): Promise<void> {
  let cursor = Math.max(0, options.afterSequence ?? 0);
  for (let attempt = 0; attempt < 4 && !options.signal?.aborted; attempt += 1) {
    let snapshotReceived = false;
    let stopped = false;
    let streamError = "";
    try {
      const response = await apiRequest(
        `/api/runs/${runId}/events?after=${cursor}`,
        { cache: "no-store", signal: options.signal },
        { errorMessage: "Failed to observe run", includeErrorBody: true, requireBody: true }
      );
      await readSSE(response, ({ event, id, data }) => {
        if (event === "run.snapshot") {
          if (id > cursor) cursor = id;
          const snapshot = JSON.parse(data) as RunProjectionSnapshot;
          options.onSnapshot(snapshot);
          snapshotReceived = true;
          stopped = runObservationStopped(snapshot.run.status);
          return;
        }
        if (id > 0 && id <= cursor) return;
        if (id > cursor) cursor = id;
        const decoded = JSON.parse(data) as ChatEvent | RunEvent;
        const projected = projectRunEvent(decoded);
        if (projected.type === "error") {
          streamError = projected.error;
          return;
        }
        options.onEvent(projected, id, !snapshotReceived);
        if (projected.type === "run_state") stopped = runObservationStopped(projected.status);
      });
      if (stopped) return;
      throw new Error(streamError || "Run event stream closed before the run stopped");
    } catch (error) {
      if (options.signal?.aborted) return;
      if (error instanceof APIError && !error.retryable) throw error;
      if (attempt === 3) throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 250 * 2 ** attempt));
  }
}

type SSEFrame = { event: string; id: number; data: string };

async function readSSE(response: Response, onFrame: (frame: SSEFrame) => void) {
  if (!response.body) {
    return;
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer = (buffer + decoder.decode(value, { stream: true })).replaceAll("\r\n", "\n");
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";

    for (const rawEvent of events) {
      const lines = rawEvent.split("\n");
      const dataLine = lines.find((line) => line.startsWith("data: "));
      if (!dataLine) {
        continue;
      }
	  const event = lines.find((line) => line.startsWith("event: "))?.slice(7) ?? "message";
	  const rawID = lines.find((line) => line.startsWith("id: "))?.slice(4) ?? "";
	  const id = /^\d+$/.test(rawID) ? Number(rawID) : 0;
	  onFrame({ event, id, data: dataLine.slice(6) });
    }
  }
}

function runObservationStopped(status: string) {
  return status === "waiting_for_user" || status === "completed" || status === "failed" ||
    status === "failed_recoverable" || status === "canceled";
}

function projectRunEvent(event: ChatEvent | RunEvent): ChatEvent {
  if (!("schema_version" in event)) return event;
  const payload = event.payload;
  if (event.type === "model.delta") return { type: "model_delta", delta: String(payload.delta ?? "") };
  if (event.type === "run.progress") return {
    type: "run_progress", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id), iteration: numberValue(payload.iteration), max_iterations: numberValue(payload.max_iterations),
    elapsed_seconds: numberValue(payload.elapsed_seconds), max_runtime_seconds: numberValue(payload.max_runtime_seconds),
    output_chars: numberValue(payload.output_chars), max_output_chars: numberValue(payload.max_output_chars),
    tool_calls: numberValue(payload.tool_calls), max_tool_calls: numberValue(payload.max_tool_calls), stop_reason: stringValue(payload.stop_reason)
  };
  if (event.type.startsWith("stage.")) return {
    type: "stage_state", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id), role: stringValue(payload.name) ?? "stage",
    status: stringValue(payload.status) ?? event.type.slice("stage.".length), iteration: numberValue(payload.iteration),
    input: stringValue(payload.input), output: stringValue(payload.output), error: stringValue(payload.error)
  };
  if (event.type.startsWith("run.")) return {
    type: "run_state", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id) ?? "", status: runStatusValue(payload.status) ?? fallbackRunStatus(event.type)
  };
  return { type: "model_delta", delta: "" };
}

export function runStatusValue(value: unknown): ContractSchemas["RunStatus"] | undefined {
  if (typeof value !== "string") return undefined;
  const statuses: ContractSchemas["RunStatus"][] = [
    "queued", "running", "waiting_for_user", "completed", "failed", "failed_recoverable", "canceling", "canceled"
  ];
  return statuses.find((status) => status === value);
}
function fallbackRunStatus(eventType: string): ContractSchemas["RunStatus"] {
  if (eventType === "run.created") return "queued";
  if (eventType === "run.waiting_for_user") return "waiting_for_user";
  if (eventType === "run.completed") return "completed";
  if (eventType === "run.failed") return "failed";
  if (eventType === "run.cancel_requested") return "canceling";
  if (eventType === "run.canceled") return "canceled";
  return "running";
}

