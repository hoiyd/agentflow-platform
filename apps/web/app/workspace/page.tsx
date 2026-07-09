import { ChatShell } from "../../components/ChatShell";

export default async function WorkspacePage({
  searchParams
}: {
  searchParams?: Promise<{ conversation?: string }>;
}) {
  const params = await searchParams;
  return <ChatShell initialConversationId={params?.conversation ?? ""} />;
}
