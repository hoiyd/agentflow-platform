import { ChatShell } from "../../components/ChatShell";

export default async function WorkspacePage({
  searchParams
}: {
  searchParams?: Promise<{ conversation?: string; view?: string }>;
}) {
  const params = await searchParams;
  return <ChatShell initialConversationId={params?.conversation ?? ""} initialView={params?.view === "knowledge" ? "knowledge" : "chat"} />;
}
