import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { Check, LoaderCircle, X } from "lucide-react";
import { listGuidanceProjects, type ProjectCatalog, type ProjectContext } from "../../lib/project-guidance";
import { SelectionMenu } from "../ui/SelectionMenu";
import { ProjectFileBrowser } from "./ProjectFileBrowser";

export function ProjectGuidanceDialog({ value, onClose, onApply }: {
  value?: ProjectContext; onClose: () => void; onApply: (next?: ProjectContext) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const [root, setRoot] = useState(value?.root ?? "");
  const [paths, setPaths] = useState(value?.paths ?? []);
  const [catalog, setCatalog] = useState<ProjectCatalog>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useLayoutEffect(() => {
    const element = dialog.current!;
    const opener = document.activeElement as HTMLElement | null;
    element.showModal();
    return () => { element.close(); opener?.focus(); };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void listGuidanceProjects(controller.signal).then(result => {
      if (!controller.signal.aborted) setCatalog(result);
    }).catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Project instructions unavailable"); });
    return () => controller.abort();
  }, [attempt]);
  const project = catalog?.projects.find(item => item.root === root);
  const unavailable = root && catalog && !project;
  const options = [
    { value: "", label: "Select a source directory" },
    ...(catalog?.projects.map(item => ({ value: item.root, label: item.root, description: `${item.entry} · ${item.rule_count} path rules` })) ?? []),
    ...(unavailable ? [{ value: root, label: root, disabled: true, description: "Unavailable in this workspace" }] : [])
  ];
  return <dialog ref={dialog} className="project-guidance-dialog" aria-labelledby={title}
    onCancel={event => { event.preventDefault(); onClose(); }}>
    <header><div><span className="project-guidance-eyebrow">New run context</span><h2 id={title}>Project instructions</h2></div>
      <button type="button" className="project-guidance-icon" aria-label="Close project instructions" onClick={onClose}><X size={18} /></button>
    </header>
    <div className="project-guidance-body">
      {error ? <div className="project-guidance-notice" role="alert"><span>{error}</span><button type="button" className="secondary-action" onClick={() => { setError(""); setCatalog(undefined); setAttempt(current => current + 1); }}>Retry</button></div> : null}
      {!catalog && !error ? <p className="project-guidance-status" role="status"><LoaderCircle size={15} className="is-spinning" />Loading instruction sources...</p> : null}
      {catalog && !catalog.configured ? <p className="project-guidance-status" role="status">Project instructions are not configured for this server.</p> : null}
      {catalog?.configured && !catalog.projects.length ? <p className="project-guidance-status" role="status">No trusted instruction sources are available in this workspace.</p> : null}
      <SelectionMenu label="Source directory" optionsLabel="source directories" value={root} options={options} disabled={!catalog?.projects.length || Boolean(error)}
        onChange={next => { setRoot(next); setPaths([]); }} />
      {unavailable ? <p className="project-guidance-notice" role="alert">The saved source directory is no longer available. Choose another directory or clear the selection.</p> : null}
      {project ? <>
        <p className="project-guidance-status"><span>Entry: <code>{project.entry}</code> · {project.rule_count} path rules</span></p>
        <div className="project-guidance-selection"><span>Task files <strong>{paths.length} / 16</strong></span>
          {paths.length ? <ul aria-label="Selected task files">{paths.map(name => <li key={name}><span>{name}</span>
            <button type="button" aria-label={`Remove ${name}`} onClick={() => setPaths(current => current.filter(item => item !== name))}><X size={13} /></button></li>)}</ul> : null}
        </div>
        <ProjectFileBrowser key={root} root={root} paths={paths} onChange={setPaths} />
      </> : null}
    </div>
    <footer><button type="button" className="secondary-action project-guidance-clear" disabled={!value && !root} onClick={() => onApply(undefined)}><X size={14} />Clear selection</button>
      <button type="button" className="secondary-action" onClick={onClose}>Cancel</button>
      <button type="button" className="send" disabled={!project || !paths.length || Boolean(error)} onClick={() => onApply({ root, paths: [...paths].sort() })}><Check size={15} />Apply instructions</button>
    </footer>
  </dialog>;
}
