import type { TaskStateRevision } from "../../lib/api";
import { partitionTaskStateRevisions, taskStateOperationLabel, visibleTaskStateRevisions } from "../../lib/task-state";

export function TaskStateChanges({ revisions, runId }: { revisions: TaskStateRevision[]; runId: string }) {
  if (revisions.length === 0) return null;
  const groups = partitionTaskStateRevisions(revisions, runId);
  const visibleRevisions = visibleTaskStateRevisions(revisions, runId);
  return (
    <section className="task-state-changes">
      <header className="task-state-changes-header">
        <div><div className="panel-title inline">Task state changes</div><p>{groups.fromRun.length} from this run, {groups.conversationHistory.length} elsewhere in the conversation.</p></div>
        <code>{visibleRevisions.length === revisions.length ? `${revisions.length} revisions` : `${visibleRevisions.length} of ${revisions.length}`}</code>
      </header>
      <div className="task-state-change-list">
        {visibleRevisions.map((revision) => {
          const belongsToRun = revision.source.run_id === runId;
          const operations = revision.patch.operations.slice(0, 4);
          return (
            <article className="task-state-change" key={revision.id}>
              <div className="task-state-change-version"><code>v{revision.previous_version}</code><span>→</span><strong>v{revision.version}</strong></div>
              <div className="task-state-change-operations">
                {operations.map((operation, index) => <span key={`${revision.id}-${operation.type}-${index}`}>{taskStateOperationLabel(operation.type)}</span>)}
                {revision.patch.operations.length > operations.length ? <span>+{revision.patch.operations.length - operations.length} more</span> : null}
              </div>
              <div className="task-state-change-source">
                <span className={belongsToRun ? "this-run" : "conversation-history"}>{belongsToRun ? "This run" : "Conversation"}</span>
                <span>{revision.source.actor_type}</span>
                <time dateTime={revision.created_at}>{new Date(revision.created_at).toLocaleString()}</time>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
