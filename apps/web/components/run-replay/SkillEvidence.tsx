import { ChevronRight, LocateFixed } from "lucide-react";
import type { SkillEvidence } from "../../lib/api";

type Props = {
  items: SkillEvidence[];
  onInspectEvent: (eventId: string) => void;
};

export function SkillEvidencePanel({ items, onInspectEvent }: Props) {
  if (items.length === 0) return null;
  const included = items.filter(item => item.instructions === "included").length;
  return (
    <details className="skill-evidence">
      <summary>
        <ChevronRight size={16} aria-hidden="true" />
        Skill evidence <span>{included} included · {items.length} entries</span>
      </summary>
      <div className="skill-evidence-list">
        {items.map(item => (
          <article className="skill-evidence-row" key={`${item.agent_id}-${item.stage_id ?? ""}-${item.name}`}>
            <div className="skill-evidence-identity">
              <strong>{item.name}</strong>
              <span>{item.agent_id} · {item.stage_id ?? "Run scope"}</span>
              <code title={item.hash}>{item.hash}</code>
            </div>
            <div className="skill-evidence-observations">
              <div className="skill-evidence-status">
                <span>{item.bound ? "Bound" : "Not bound"}</span>
                <strong>{item.instructions === "included" ? "Instructions included" : "Instructions not observed"}</strong>
                <span>{item.activation === "explicit" ? "Explicit invocation" : item.activation === "model" ? "Model activation" : "Origin not observed"}</span>
              </div>
              {item.event_id ? (
                <div className="skill-evidence-status">
                  <button type="button" className="runtime-diagnostic-action" onClick={() => onInspectEvent(item.event_id!)}>
                    <LocateFixed aria-hidden="true" size={14} /> Inspect input · event {item.first_sequence}
                  </button>
                  <span>First input: {item.estimated_tokens ?? 0} estimated instruction tokens</span>
                  {item.first_request_sequence ? <span>First request · event {item.first_request_sequence}</span> : null}
                  <code>{item.manifest_id}</code>
                  <code>{item.request_id}</code>
                </div>
              ) : null}
              {(item.resources ?? []).map(resource => (
                <div className="skill-evidence-status" key={resource.event_id}>
                  <span>Resource read</span><code>{resource.path}</code>
                  <span>{resource.offset}–{resource.next_offset} / {resource.total_bytes} bytes</span>
                  <code title={resource.hash}>{resource.hash}</code>
                  <button type="button" className="runtime-diagnostic-action" onClick={() => onInspectEvent(resource.event_id)}>
                    <LocateFixed aria-hidden="true" size={14} /> Inspect read · event {resource.sequence}
                  </button>
                </div>
              ))}
              {(item.failures ?? []).map(failure => (
                <div className="skill-evidence-status" key={failure.event_id}>
                  <span>{failure.tool} {failure.code === "result_not_observed" ? "result not observed" : "failed"}</span><code>{failure.code}</code>
                  {failure.path ? <code>{failure.path}</code> : null}
                  <button type="button" className="runtime-diagnostic-action" onClick={() => onInspectEvent(failure.event_id)}>
                    <LocateFixed aria-hidden="true" size={14} /> Inspect failure · event {failure.sequence}
                  </button>
                </div>
              ))}
            </div>
          </article>
        ))}
      </div>
    </details>
  );
}
