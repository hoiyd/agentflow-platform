import { useEffect, useState } from "react";
import { ArrowUp, ChevronRight, FileText, Folder, LoaderCircle } from "lucide-react";
import { listGuidanceFiles, type ProjectFilePage } from "../../lib/project-guidance";

export function ProjectFileBrowser({ root, paths, onChange }: { root: string; paths: string[]; onChange: (paths: string[]) => void }) {
  const [directory, setDirectory] = useState(".");
  const [draft, setDraft] = useState(".");
  function navigate(next: string) { setDirectory(next); setDraft(next); }
  return <section className="project-file-browser" aria-label="Project files">
    <div className="project-directory-bar">
      <button type="button" className="project-guidance-icon" aria-label="Parent directory" disabled={directory === "."}
        onClick={() => navigate(directory.split("/").slice(0, -1).join("/") || ".")}><ArrowUp size={16} /></button>
      <label><input aria-label="Project directory" value={draft} maxLength={1024}
        onChange={event => setDraft(event.target.value)} onKeyDown={event => {
          if (event.key === "Enter") { event.preventDefault(); navigate(draft.trim() || "."); }
        }} /></label>
      <button type="button" className="secondary-action" onClick={() => navigate(draft.trim() || ".")}>Open directory</button>
    </div>
    <DirectoryEntries key={`${root}:${directory}`} root={root} directory={directory} paths={paths} onChange={onChange} navigate={navigate} />
  </section>;
}

function DirectoryEntries({ root, directory, paths, onChange, navigate }: {
  root: string; directory: string; paths: string[]; onChange: (paths: string[]) => void; navigate: (path: string) => void;
}) {
  const [result, setResult] = useState<ProjectFilePage>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [visible, setVisible] = useState(50);
  useEffect(() => {
    const controller = new AbortController();
    void listGuidanceFiles(root, directory, controller.signal).then(page => {
      if (!controller.signal.aborted) setResult(page);
    }).catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Project files unavailable"); });
    return () => controller.abort();
  }, [root, directory, attempt]);
  if (error) return <div className="project-guidance-notice" role="alert"><span>{error}</span><button type="button" className="secondary-action" onClick={() => { setError(""); setAttempt(value => value + 1); }}>Retry files</button></div>;
  if (!result) return <p className="project-guidance-status" role="status"><LoaderCircle className="is-spinning" size={15} />Loading files...</p>;
  return <>
    <ul className="project-file-list">{result.entries.slice(0, visible).map(entry => <li key={entry.path}>
      {entry.kind === "directory" ? <button type="button" aria-label={`Open ${entry.path}`} onClick={() => navigate(entry.path)}><Folder size={16} /><span>{entry.path.split("/").at(-1)}</span><ChevronRight size={15} /></button> :
        <label><input type="checkbox" aria-label={`Select ${entry.path}`} checked={paths.includes(entry.path)} disabled={paths.length >= 16 && !paths.includes(entry.path)}
          onChange={event => onChange(event.target.checked ? [...paths, entry.path] : paths.filter(item => item !== entry.path))} /><FileText size={15} /><span>{entry.path.split("/").at(-1)}</span></label>}
    </li>)}</ul>
    {!result.entries.length ? <p className="project-guidance-status">No selectable files in this directory.</p> : null}
    {visible < result.entries.length ? <button className="secondary-action" type="button" onClick={() => setVisible(value => value + 50)}>Show more files</button> : null}
    {result.truncated ? <p className="project-guidance-status" role="status">Directory listing limited to 500 entries. A narrower directory may contain more files.</p> : null}
  </>;
}
