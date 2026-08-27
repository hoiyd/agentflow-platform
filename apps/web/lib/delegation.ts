const delegationStatusLabels: Record<string, string> = {
  created: "Created",
  running: "Running",
  blocked: "Blocked",
  completed: "Completed",
  failed: "Failed",
  canceled: "Canceled"
};

const delegationBlockReasonLabels: Record<string, string> = {
  child_recovery_required: "Child recovery required"
};

export function delegationStatusLabel(status: string): string {
  return delegationStatusLabels[status] ?? humanize(status);
}

export function delegationBlockReasonLabel(reason: string): string {
  return delegationBlockReasonLabels[reason] ?? humanize(reason);
}

function humanize(value: string): string {
  const normalized = value.trim().replaceAll("_", " ");
  return normalized ? normalized[0].toUpperCase() + normalized.slice(1) : "Unknown";
}
