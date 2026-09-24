import { useState } from "react";
import {
  DEFAULT_COMPLETION_VERIFICATION, normalizeCompletionVerification, validateCompletionVerification,
  type CompletionVerificationSettings
} from "../../lib/verification";

export function useCompletionVerification() {
  const [settings, setSettings] = useState<CompletionVerificationSettings>(DEFAULT_COMPLETION_VERIFICATION);
  const [draft, setDraft] = useState<CompletionVerificationSettings | null>(null);
  const [error, setError] = useState("");

  function open() {
    setError("");
    setDraft(structuredClone(settings));
  }

  function cancel() {
    setDraft(null);
    setError("");
  }

  function change(update: Partial<CompletionVerificationSettings>) {
    setError("");
    setDraft((current) => current ? { ...current, ...update } : current);
  }

  function save() {
    if (!draft) return;
    const normalized = normalizeCompletionVerification(draft);
    const errors = validateCompletionVerification(normalized);
    if (errors.length) {
      setError(errors[0]);
      return;
    }
    setSettings(normalized);
    cancel();
  }

  return { settings, draft, error, open, cancel, change, save };
}
