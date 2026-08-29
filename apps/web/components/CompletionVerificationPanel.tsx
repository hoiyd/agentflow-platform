import { Braces, Globe2, Quote, Terminal, TextCursorInput, X } from "lucide-react";
import { useState } from "react";

import type { CompletionVerificationSettings, VerifierTypeInput } from "../lib/verification";
import { enabledVerifierCount } from "../lib/verification";
import { VerifierConfigEditor } from "./VerifierConfigEditor";

type CompletionVerificationPanelProps = {
  draft: CompletionVerificationSettings;
  error: string;
  onCancel: () => void;
  onChange: (update: Partial<CompletionVerificationSettings>) => void;
  onSave: () => void;
};

const VERIFIER_TABS: Array<{ type: VerifierTypeInput; label: string }> = [
  { type: "text_constraints", label: "Text" },
  { type: "citation", label: "Citations" },
  { type: "json_schema", label: "JSON Schema" },
  { type: "http", label: "HTTP" },
  { type: "command", label: "Command" }
];

export function CompletionVerificationPanel({
  draft,
  error,
  onCancel,
  onChange,
  onSave
}: CompletionVerificationPanelProps) {
  const [selectedVerifier, setSelectedVerifier] = useState<VerifierTypeInput>("text_constraints");
  const enabledCount = enabledVerifierCount(draft);

  return (
    <section className="verification-config-panel">
      <div className="verification-config-header">
        <div>
          <span>Run policy</span>
          <strong>Verification</strong>
        </div>
        <label className="verification-enabled-toggle">
          <input
            checked={draft.enabled}
            onChange={(event) => onChange({ enabled: event.target.checked })}
            type="checkbox"
          />
          <span>{draft.enabled ? "Enabled" : "Disabled"}</span>
        </label>
      </div>

      <div className="verification-policy-grid">
        <label>
          <span>Maximum attempts</span>
          <select
            disabled={!draft.enabled}
            onChange={(event) => onChange({ maxAttempts: Number(event.target.value) })}
            value={draft.maxAttempts}
          >
            {[1, 2, 3, 4, 5].map((attempts) => (
              <option key={attempts} value={attempts}>{attempts}</option>
            ))}
          </select>
        </label>
        <label>
          <span>When attempts are exhausted</span>
          <select
            disabled={!draft.enabled}
            onChange={(event) =>
              onChange({ onExhausted: event.target.value as CompletionVerificationSettings["onExhausted"] })
            }
            value={draft.onExhausted}
          >
            <option value="waiting_for_user">Wait for user</option>
            <option value="fail">Fail run</option>
          </select>
        </label>
        <div className="verification-enabled-count">
          <span>Required verifiers</span>
          <strong>{enabledCount}</strong>
        </div>
      </div>

      <div className="verifier-tabs" role="tablist" aria-label="Verifier type">
        {VERIFIER_TABS.map((tab) => (
          <button
            aria-selected={selectedVerifier === tab.type}
            className={selectedVerifier === tab.type ? "active" : ""}
            key={tab.type}
            onClick={() => setSelectedVerifier(tab.type)}
            role="tab"
            type="button"
          >
            {verifierIcon(tab.type)}
            <span>{tab.label}</span>
            <i className={isVerifierEnabled(draft, tab.type) ? "enabled" : ""} />
          </button>
        ))}
      </div>

      <VerifierConfigEditor
        disabled={!draft.enabled}
        draft={draft}
        onChange={onChange}
        type={selectedVerifier}
      />

      {error ? <div className="verification-config-error" role="alert">{error}</div> : null}

      <div className="agent-config-actions verification-config-actions">
        <button className="secondary-action agent-config-cancel" onClick={onCancel} type="button">
          <X size={15} /> Cancel
        </button>
        <button className="send compact-send" onClick={onSave} type="button">
          Save policy
        </button>
      </div>
    </section>
  );
}

function isVerifierEnabled(settings: CompletionVerificationSettings, type: VerifierTypeInput): boolean {
  if (type === "text_constraints") return settings.textConstraints.enabled;
  if (type === "citation") return settings.citation.enabled;
  if (type === "json_schema") return settings.jsonSchema.enabled;
  if (type === "http") return settings.http.enabled;
  return settings.command.enabled;
}

function verifierIcon(type: VerifierTypeInput) {
  if (type === "text_constraints") return <TextCursorInput size={14} />;
  if (type === "citation") return <Quote size={14} />;
  if (type === "json_schema") return <Braces size={14} />;
  if (type === "http") return <Globe2 size={14} />;
  return <Terminal size={14} />;
}
