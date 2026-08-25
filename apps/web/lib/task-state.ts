import type { TaskState, TaskStateRevision } from "./api";

export function hasTaskStateFacts(state: TaskState | null): boolean {
  return Boolean(
    state &&
      (state.goal ||
        state.tasks.length > 0 ||
        state.decisions.length > 0 ||
        state.constraints.length > 0 ||
        state.blockers.length > 0 ||
        state.artifact_refs.length > 0)
  );
}

export function taskStateCounts(state: TaskState) {
  return {
    openTasks: state.tasks.filter((task) => task.status === "pending" || task.status === "in_progress").length,
    completedTasks: state.tasks.filter((task) => task.status === "completed").length,
    openBlockers: state.blockers.filter((blocker) => blocker.status === "open").length
  };
}

export function partitionTaskStateRevisions(revisions: TaskStateRevision[], runId: string) {
  return revisions.reduce(
    (result, revision) => {
      if (revision.source.run_id === runId) {
        result.fromRun.push(revision);
      } else {
        result.conversationHistory.push(revision);
      }
      return result;
    },
    { fromRun: [] as TaskStateRevision[], conversationHistory: [] as TaskStateRevision[] }
  );
}

export function visibleTaskStateRevisions(revisions: TaskStateRevision[], runId: string): TaskStateRevision[] {
  const groups = partitionTaskStateRevisions(revisions, runId);
  const selected = [...groups.fromRun.slice(-20), ...groups.conversationHistory.slice(-10)];
  return [...new Map(selected.map((revision) => [revision.id, revision])).values()].sort(
    (left, right) => left.version - right.version
  );
}

export function taskStateOperationLabel(type: string): string {
  return type.replaceAll("_", " ");
}
