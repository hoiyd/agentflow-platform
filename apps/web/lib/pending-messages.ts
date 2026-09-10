export function removePendingMessages<T extends { id: string }>(messages: T[], pendingIds: string[]) {
  const pending = new Set(pendingIds);
  return messages.filter((message) => !pending.has(message.id));
}
