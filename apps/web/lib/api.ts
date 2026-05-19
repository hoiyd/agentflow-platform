export type Conversation = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
};

export type Message = {
  id: string;
  conversation_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at: string;
};

export type ChatEvent =
  | { type: "conversation"; conversation_id: string }
  | { type: "delta"; delta: string }
  | { type: "done"; conversation_id: string; message_id: string }
  | { type: "error"; error: string };

export type ToolInfo = {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  source: "builtin" | "mcp" | string;
  source_id?: string;
  enabled: boolean;
};

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export async function listConversations(): Promise<Conversation[]> {
  const response = await fetch(`${API_BASE}/api/conversations`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load conversations: ${response.status}`);
  }
  return response.json();
}

export async function createConversation(title: string): Promise<Conversation> {
  const response = await fetch(`${API_BASE}/api/conversations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title })
  });
  if (!response.ok) {
    throw new Error(`Failed to create conversation: ${response.status}`);
  }
  return response.json();
}

export async function listMessages(conversationId: string): Promise<Message[]> {
  const response = await fetch(`${API_BASE}/api/conversations/${conversationId}/messages`, {
    cache: "no-store"
  });
  if (!response.ok) {
    throw new Error(`Failed to load messages: ${response.status}`);
  }
  return response.json();
}

export async function streamChat(
  input: { conversation_id?: string; message: string },
  onEvent: (event: ChatEvent) => void
) {
  const response = await fetch(`${API_BASE}/api/chat`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });

  if (!response.ok || !response.body) {
    throw new Error(`Chat request failed: ${response.status}`);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";

    for (const rawEvent of events) {
      const dataLine = rawEvent
        .split("\n")
        .find((line) => line.startsWith("data: "));
      if (!dataLine) {
        continue;
      }
      onEvent(JSON.parse(dataLine.slice(6)) as ChatEvent);
    }
  }
}

export async function listTools(): Promise<ToolInfo[]> {
  const response = await fetch(`${API_BASE}/api/tools`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load tools: ${response.status}`);
  }
  return response.json();
}

export async function setToolEnabled(name: string, enabled: boolean): Promise<ToolInfo[]> {
  const action = enabled ? "enable" : "disable";
  const response = await fetch(`${API_BASE}/api/tools/${encodeURIComponent(name)}/${action}`, {
    method: "POST"
  });
  if (!response.ok) {
    throw new Error(`Failed to update tool: ${response.status}`);
  }
  return response.json();
}
