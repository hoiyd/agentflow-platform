const delegationStatusLabels: Record<string, string> = {
  created: "Created",
  running: "Running",
  blocked: "Blocked",
  completed: "Completed",
  failed: "Failed",
  canceled: "Canceled"
};

export function delegationStatusLabel(status: string): string {
  return delegationStatusLabels[status] ?? humanize(status);
}

export function delegationBlockReasonLabel(reason: string, perspective: "parent" | "child"): string {
  if (reason === "child_recovery_required") {
    return perspective === "child"
      ? "This run requires recovery before its parent can continue"
      : "Child run requires recovery";
  }
  return humanize(reason);
}

function humanize(value: string): string {
  const normalized = value.trim().replaceAll("_", " ");
  return normalized ? normalized[0].toUpperCase() + normalized.slice(1) : "Unknown";
}
