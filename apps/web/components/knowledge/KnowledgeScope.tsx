import { Filter, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { components } from "../../lib/api-contract.gen";
import { SelectionMenu } from "../ui/SelectionMenu";
import { EMPTY_SCOPE, environments, type ScopeValues } from "./useKnowledgeScope";

export function KnowledgeScopeFields({ values, onChange, disabled = false }: { values: ScopeValues; onChange: (value: ScopeValues) => void; disabled?: boolean }) {
  return <div className="knowledge-scope-fields">
    <label><span>Product</span><input aria-label="Knowledge product" disabled={disabled} maxLength={120} placeholder="All products" value={values.product} onChange={e => onChange({ ...values, product: e.target.value })} /></label>
    <div><span className="knowledge-scope-field-label">Environment</span><SelectionMenu label="Knowledge environment" searchable={false} disabled={disabled} value={values.environment}
      options={[{ value: "", label: "All environments" }, ...environments.map(value => ({ value, label: value }))]} onChange={environment => onChange({ ...values, environment })} /></div>
    <label><span>Language</span><input aria-label="Knowledge language" disabled={disabled} maxLength={35} placeholder="All languages" value={values.language} onChange={e => onChange({ ...values, language: e.target.value })} /></label>
    <button type="button" className="knowledge-scope-clear" disabled={disabled || !Object.values(values).some(Boolean)} onClick={() => onChange({ ...EMPTY_SCOPE })}><X size={14} />Clear</button>
  </div>;
}

export function KnowledgeScopeControl({ values, onChange, disabled }: { values: ScopeValues; onChange: (value: ScopeValues) => void; disabled: boolean }) {
  const [draft, setDraft] = useState<ScopeValues | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLElement>(null);
  const open = draft !== null && !disabled;
  useEffect(() => {
    if (!open) return;
    const opener = trigger.current;
    dialog.current?.querySelector<HTMLInputElement>("input")?.focus();
    return () => opener?.focus();
  }, [open]);
  const count = Object.values(values).filter(value => value.trim()).length;
  return <>
    <button ref={trigger} type="button" title="Knowledge scope for new runs" className={`verification-config-toggle ${count ? "active" : ""}`} disabled={disabled} onClick={() => setDraft({ ...values })} aria-label="Knowledge scope for new runs" aria-haspopup="dialog" aria-expanded={open}><Filter size={15} /><span>Knowledge</span>{count ? <strong>{count}</strong> : null}</button>
    {draft && open ? <div className="modal-backdrop agent-config-modal-backdrop" role="presentation" onKeyDown={e => {
      if (e.key === "Escape") setDraft(null);
      if (e.key === "Tab") {
        const controls = dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled):not([tabindex="-1"]), input:not(:disabled), [tabindex="0"]');
        const first = controls?.[0], last = controls?.[controls.length - 1];
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
      }
    }}>
      <section ref={dialog} role="dialog" aria-modal="true" aria-label="Knowledge scope" className="agent-config-dialog knowledge-scope-dialog">
        <header><div><span>New run settings</span><h2>Knowledge scope</h2></div><button type="button" aria-label="Close Knowledge scope" onClick={() => setDraft(null)}><X size={18} /></button></header>
        <KnowledgeScopeFields values={draft} onChange={setDraft} />
        <p>Existing runs keep their saved scope.</p>
        <footer><button type="button" onClick={() => setDraft(null)}>Cancel</button><button type="button" className="send" onClick={() => { onChange(draft); setDraft(null); }}>Apply scope</button></footer>
      </section>
    </div> : null}
  </>;
}

export function FrozenKnowledgeScope({ scope }: { scope?: components["schemas"]["KnowledgeScope"] }) {
  if (!scope) return null;
  return <details className="frozen-knowledge-scope"><summary><Filter size={14} />Saved Knowledge scope</summary>
    <dl><div><dt>Source</dt><dd>Run request</dd></div><div><dt>Policy</dt><dd>{scope.policy_version}</dd></div><div><dt>Identity</dt><dd>{scope.id}</dd></div></dl>
    <pre>{JSON.stringify(scope.filter, null, 2)}</pre>
  </details>;
}
