import { FolderOpen } from "lucide-react";
import type { ProjectGuidanceSnapshot } from "../../lib/project-guidance";

export function FrozenProjectGuidance({ snapshot }: { snapshot?: ProjectGuidanceSnapshot }) {
  if (!snapshot) return null;
  return <details className="frozen-project-guidance">
    <summary><FolderOpen size={15} /><span>Saved project instructions</span><span className="project-guidance-summary-root">{snapshot.selection.root}</span></summary>
    <dl className="project-guidance-identities"><div><dt>Version</dt><dd>{snapshot.version}</dd></div><div><dt>Source</dt><dd>{snapshot.source_type}</dd></div>
      <div><dt>Task files</dt><dd>{snapshot.selection.paths.join(", ")}</dd></div></dl>
    <ul className="project-guidance-evidence">{snapshot.entries.map(entry => <li key={entry.path}>
      <div className="project-guidance-evidence-heading"><strong>{entry.path}</strong><span>{entry.reason.replaceAll("_", " ")}</span></div>
      {entry.matched_paths?.length ? <p>Matched: {entry.matched_paths.join(", ")}</p> : null}
      {entry.hash ? <div className="project-guidance-hash"><span>SHA-256</span><code>{entry.hash}</code></div> : null}
      <p>{entry.original_bytes} bytes · {entry.estimated_tokens} estimated body tokens</p>
      {entry.content ? <details><summary>Instruction text</summary><pre>{entry.content}</pre></details> : null}
    </li>)}</ul>
  </details>;
}
