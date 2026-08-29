import type { CompletionVerificationSettings, VerifierTypeInput } from "../lib/verification";

type VerifierConfigEditorProps = {
  disabled: boolean;
  draft: CompletionVerificationSettings;
  onChange: (update: Partial<CompletionVerificationSettings>) => void;
  type: VerifierTypeInput;
};

export function VerifierConfigEditor({ disabled, draft, onChange, type }: VerifierConfigEditorProps) {
  if (type === "text_constraints") {
    const settings = draft.textConstraints;
    const update = (next: Partial<typeof settings>) =>
      onChange({ textConstraints: { ...settings, ...next } });
    return (
      <VerifierSection
        checked={settings.enabled}
        disabled={disabled}
        label="Use text constraints"
        onToggle={(enabled) => update({ enabled })}
      >
        <div className="verifier-field-grid four-columns">
          <NumberField disabled={disabled || !settings.enabled} label="Min characters" min={0} value={settings.minimumCharacters} onChange={(minimumCharacters) => update({ minimumCharacters })} />
          <NumberField disabled={disabled || !settings.enabled} label="Max characters" min={0} value={settings.maximumCharacters} onChange={(maximumCharacters) => update({ maximumCharacters })} />
          <NumberField disabled={disabled || !settings.enabled} label="Min words" min={0} value={settings.minimumWords} onChange={(minimumWords) => update({ minimumWords })} />
          <NumberField disabled={disabled || !settings.enabled} label="Max words" min={0} value={settings.maximumWords} onChange={(maximumWords) => update({ maximumWords })} />
        </div>
        <div className="verifier-field-grid three-columns">
          <TextAreaField disabled={disabled || !settings.enabled} label="Required phrases" placeholder="One phrase per line" value={settings.requiredPhrases} onChange={(requiredPhrases) => update({ requiredPhrases })} />
          <TextAreaField disabled={disabled || !settings.enabled} label="Forbidden phrases" placeholder="One phrase per line" value={settings.forbiddenPhrases} onChange={(forbiddenPhrases) => update({ forbiddenPhrases })} />
          <TextAreaField disabled={disabled || !settings.enabled} label="Required headings" placeholder="One heading per line" value={settings.requiredHeadings} onChange={(requiredHeadings) => update({ requiredHeadings })} />
        </div>
        <CheckboxField checked={settings.caseSensitive} disabled={disabled || !settings.enabled} label="Case-sensitive matching" onChange={(caseSensitive) => update({ caseSensitive })} />
      </VerifierSection>
    );
  }

  if (type === "citation") {
    const settings = draft.citation;
    const update = (next: Partial<typeof settings>) => onChange({ citation: { ...settings, ...next } });
    return (
      <VerifierSection checked={settings.enabled} disabled={disabled} label="Use citation policy" onToggle={(enabled) => update({ enabled })}>
        <div className="verifier-field-grid three-columns">
          <NumberField disabled={disabled || !settings.enabled} label="Min citations" min={1} max={100} value={settings.minimumCitations} onChange={(minimumCitations) => update({ minimumCitations })} />
          <NumberField disabled={disabled || !settings.enabled} label="Unique sources" min={0} max={100} value={settings.minimumUniqueSources} onChange={(minimumUniqueSources) => update({ minimumUniqueSources })} />
          <CheckboxField checked={settings.requireHTTPS} disabled={disabled || !settings.enabled} label="HTTPS only" onChange={(requireHTTPS) => update({ requireHTTPS })} framed />
        </div>
        <div className="verifier-field-grid two-columns">
          <TextAreaField disabled={disabled || !settings.enabled} label="Allowed hosts" placeholder="One hostname per line" value={settings.allowedHosts} onChange={(allowedHosts) => update({ allowedHosts })} />
          <TextAreaField disabled={disabled || !settings.enabled} label="Blocked hosts" placeholder="One hostname per line" value={settings.blockedHosts} onChange={(blockedHosts) => update({ blockedHosts })} />
        </div>
      </VerifierSection>
    );
  }

  if (type === "json_schema") {
    const settings = draft.jsonSchema;
    const update = (next: Partial<typeof settings>) => onChange({ jsonSchema: { ...settings, ...next } });
    return (
      <VerifierSection checked={settings.enabled} disabled={disabled} label="Use JSON Schema" onToggle={(enabled) => update({ enabled })}>
        <label className="verifier-field verifier-schema-field">
          <span>JSON Schema 2020-12</span>
          <textarea className="code-input" disabled={disabled || !settings.enabled} onChange={(event) => update({ schema: event.target.value })} spellCheck={false} value={settings.schema} />
        </label>
      </VerifierSection>
    );
  }

  if (type === "http") {
    const settings = draft.http;
    const update = (next: Partial<typeof settings>) => onChange({ http: { ...settings, ...next } });
    return (
      <VerifierSection checked={settings.enabled} disabled={disabled} label="Use HTTP check" onToggle={(enabled) => update({ enabled })}>
        <div className="verifier-field-grid http-fields">
          <label className="verifier-field">
            <span>Method</span>
            <select disabled={disabled || !settings.enabled} onChange={(event) => update({ method: event.target.value as "GET" | "HEAD" })} value={settings.method}>
              <option value="GET">GET</option>
              <option value="HEAD">HEAD</option>
            </select>
          </label>
          <label className="verifier-field">
            <span>URL</span>
            <input disabled={disabled || !settings.enabled} onChange={(event) => update({ url: event.target.value })} type="url" value={settings.url} />
          </label>
          <NumberField disabled={disabled || !settings.enabled} label="Expected status" min={100} max={599} value={settings.expectedStatus} onChange={(expectedStatus) => update({ expectedStatus })} />
        </div>
        <small className="verifier-requirement">Loopback URLs work by default. External hosts must be configured in the backend allowlist.</small>
      </VerifierSection>
    );
  }

  const settings = draft.command;
  const update = (next: Partial<typeof settings>) => onChange({ command: { ...settings, ...next } });
  return (
    <VerifierSection checked={settings.enabled} disabled={disabled} label="Use command check" onToggle={(enabled) => update({ enabled })}>
      <div className="verifier-field-grid command-fields">
        <label className="verifier-field">
          <span>Executable</span>
          <input disabled={disabled || !settings.enabled} onChange={(event) => update({ executable: event.target.value })} value={settings.executable} />
        </label>
        <label className="verifier-field">
          <span>Working directory</span>
          <input disabled={disabled || !settings.enabled} onChange={(event) => update({ workingDirectory: event.target.value })} value={settings.workingDirectory} />
        </label>
      </div>
      <TextAreaField disabled={disabled || !settings.enabled} label="Arguments" placeholder="One argument per line" value={settings.arguments} onChange={(argumentsValue) => update({ arguments: argumentsValue })} />
      <small className="verifier-requirement">The executable must be allowlisted and the working directory is relative to the backend verification workspace.</small>
    </VerifierSection>
  );
}

function VerifierSection({ checked, children, disabled, label, onToggle }: { checked: boolean; children: React.ReactNode; disabled: boolean; label: string; onToggle: (checked: boolean) => void }) {
  return (
    <section className="verifier-editor" role="tabpanel">
      <label className="verifier-enable-toggle">
        <input checked={checked} disabled={disabled} onChange={(event) => onToggle(event.target.checked)} type="checkbox" />
        <span>{label}</span>
      </label>
      <div className="verifier-editor-fields">{children}</div>
    </section>
  );
}

function NumberField({ disabled, label, max, min, onChange, step, value }: { disabled: boolean; label: string; max?: number; min: number; onChange: (value: number) => void; step?: number; value: number }) {
  return (
    <label className="verifier-field">
      <span>{label}</span>
      <input disabled={disabled} max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} step={step} type="number" value={value} />
    </label>
  );
}

function TextAreaField({ disabled, label, onChange, placeholder, value }: { disabled: boolean; label: string; onChange: (value: string) => void; placeholder: string; value: string }) {
  return (
    <label className="verifier-field">
      <span>{label}</span>
      <textarea disabled={disabled} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} value={value} />
    </label>
  );
}

function CheckboxField({ checked, disabled, framed = false, label, onChange }: { checked: boolean; disabled: boolean; framed?: boolean; label: string; onChange: (checked: boolean) => void }) {
  return (
    <label className={`verifier-checkbox ${framed ? "framed" : ""}`}>
      <input checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} type="checkbox" />
      <span>{label}</span>
    </label>
  );
}
