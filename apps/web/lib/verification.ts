import type { components } from "./api/generated";

type ContractSchemas = components["schemas"];

export type VerificationFailureAction = ContractSchemas["VerificationPolicyInput"]["on_exhausted"];
export type VerifierTypeInput = ContractSchemas["VerifierType"];

export type TextConstraintsSettings = {
  enabled: boolean;
  minimumCharacters: number;
  maximumCharacters: number;
  minimumWords: number;
  maximumWords: number;
  requiredPhrases: string;
  forbiddenPhrases: string;
  requiredHeadings: string;
  caseSensitive: boolean;
};

export type CitationSettings = {
  enabled: boolean;
  minimumCitations: number;
  minimumUniqueSources: number;
  requireHTTPS: boolean;
  allowedHosts: string;
  blockedHosts: string;
};

export type JSONSchemaSettings = {
  enabled: boolean;
  schema: string;
};

export type HTTPVerifierSettings = {
  enabled: boolean;
  method: "GET" | "HEAD";
  url: string;
  expectedStatus: number;
};

export type CommandVerifierSettings = {
  enabled: boolean;
  executable: string;
  arguments: string;
  workingDirectory: string;
};

export type CompletionVerificationSettings = {
  enabled: boolean;
  textConstraints: TextConstraintsSettings;
  citation: CitationSettings;
  jsonSchema: JSONSchemaSettings;
  http: HTTPVerifierSettings;
  command: CommandVerifierSettings;
  maxAttempts: number;
  onExhausted: VerificationFailureAction;
};

export type VerifierSpecInput = ContractSchemas["VerifierSpecInput"] & { required: true };
export type CompletionContractInput = ContractSchemas["CompletionContractInput"];

export const DEFAULT_COMPLETION_VERIFICATION: CompletionVerificationSettings = {
  enabled: false,
  textConstraints: {
    enabled: true,
    minimumCharacters: 120,
    maximumCharacters: 0,
    minimumWords: 0,
    maximumWords: 0,
    requiredPhrases: "",
    forbiddenPhrases: "",
    requiredHeadings: "",
    caseSensitive: false
  },
  citation: {
    enabled: false,
    minimumCitations: 2,
    minimumUniqueSources: 1,
    requireHTTPS: true,
    allowedHosts: "",
    blockedHosts: ""
  },
  jsonSchema: {
    enabled: false,
    schema: `{
  "type": "object",
  "additionalProperties": true
}`
  },
  http: {
    enabled: false,
    method: "GET",
    url: "http://localhost:8080/health",
    expectedStatus: 200
  },
  command: {
    enabled: false,
    executable: "go",
    arguments: "test\n./...",
    workingDirectory: "."
  },
  maxAttempts: 2,
  onExhausted: "waiting_for_user"
};

export function buildCompletionContract(
  settings: CompletionVerificationSettings
): CompletionContractInput | undefined {
  if (!settings.enabled) {
    return undefined;
  }

  const normalized = normalizeCompletionVerification(settings);
  const errors = validateCompletionVerification(normalized);
  if (errors.length > 0) {
    throw new Error(errors[0]);
  }

  const verifiers: VerifierSpecInput[] = [];
  if (normalized.textConstraints.enabled) {
    verifiers.push({
      id: "response-text",
      type: "text_constraints",
      required: true,
      config: compactConfig({
        min_characters: normalized.textConstraints.minimumCharacters,
        max_characters: normalized.textConstraints.maximumCharacters,
        min_words: normalized.textConstraints.minimumWords,
        max_words: normalized.textConstraints.maximumWords,
        required_phrases: lines(normalized.textConstraints.requiredPhrases),
        forbidden_phrases: lines(normalized.textConstraints.forbiddenPhrases),
        required_headings: lines(normalized.textConstraints.requiredHeadings),
        case_sensitive: normalized.textConstraints.caseSensitive
      })
    });
  }
  if (normalized.citation.enabled) {
    verifiers.push({
      id: "source-policy",
      type: "citation",
      required: true,
      config: compactConfig({
        min_citations: normalized.citation.minimumCitations,
        min_unique_hosts: normalized.citation.minimumUniqueSources,
        require_https: normalized.citation.requireHTTPS,
        allowed_hosts: lines(normalized.citation.allowedHosts),
        blocked_hosts: lines(normalized.citation.blockedHosts)
      })
    });
  }
  if (normalized.jsonSchema.enabled) {
    verifiers.push({
      id: "response-schema",
      type: "json_schema",
      required: true,
      config: { schema: parseJSONObject(normalized.jsonSchema.schema) }
    });
  }
  if (normalized.http.enabled) {
    verifiers.push({
      id: "http-check",
      type: "http",
      required: true,
      config: {
        method: normalized.http.method,
        url: normalized.http.url.trim(),
        expected_status: normalized.http.expectedStatus
      }
    });
  }
  if (normalized.command.enabled) {
    verifiers.push({
      id: "command-check",
      type: "command",
      required: true,
      config: compactConfig({
        args: [normalized.command.executable.trim(), ...lines(normalized.command.arguments)],
        working_directory: normalized.command.workingDirectory.trim()
      })
    });
  }

  return {
    subject_type: "run_output",
    verifiers,
    policy: {
      mode: "all_must_pass",
      max_attempts: normalized.maxAttempts,
      on_exhausted: normalized.onExhausted
    }
  };
}

export function normalizeCompletionVerification(
  settings: CompletionVerificationSettings
): CompletionVerificationSettings {
  return {
    ...settings,
    textConstraints: {
      ...settings.textConstraints,
      minimumCharacters: clampInteger(settings.textConstraints.minimumCharacters, 0, 100_000),
      maximumCharacters: clampInteger(settings.textConstraints.maximumCharacters, 0, 100_000),
      minimumWords: clampInteger(settings.textConstraints.minimumWords, 0, 100_000),
      maximumWords: clampInteger(settings.textConstraints.maximumWords, 0, 100_000)
    },
    citation: {
      ...settings.citation,
      minimumCitations: clampInteger(settings.citation.minimumCitations, 1, 100),
      minimumUniqueSources: clampInteger(settings.citation.minimumUniqueSources, 0, 100)
    },
    jsonSchema: { ...settings.jsonSchema },
    http: {
      ...settings.http,
      expectedStatus: clampInteger(settings.http.expectedStatus, 100, 599)
    },
    command: { ...settings.command },
    maxAttempts: clampInteger(settings.maxAttempts, 1, 5)
  };
}

export function validateCompletionVerification(settings: CompletionVerificationSettings): string[] {
  if (!settings.enabled) {
    return [];
  }

  const errors: string[] = [];
  if (enabledVerifierCount(settings) === 0) {
    errors.push("Enable at least one verifier.");
  }

  const text = settings.textConstraints;
  if (text.enabled) {
    const hasConstraint =
      text.minimumCharacters > 0 ||
      text.maximumCharacters > 0 ||
      text.minimumWords > 0 ||
      text.maximumWords > 0 ||
      lines(text.requiredPhrases).length > 0 ||
      lines(text.forbiddenPhrases).length > 0 ||
      lines(text.requiredHeadings).length > 0;
    if (!hasConstraint) {
      errors.push("Text constraints require at least one limit, phrase, or heading.");
    }
    if (text.maximumCharacters > 0 && text.minimumCharacters > text.maximumCharacters) {
      errors.push("Minimum characters cannot exceed maximum characters.");
    }
    if (text.maximumWords > 0 && text.minimumWords > text.maximumWords) {
      errors.push("Minimum words cannot exceed maximum words.");
    }
  }

  if (settings.jsonSchema.enabled) {
    try {
      parseJSONObject(settings.jsonSchema.schema);
    } catch (error) {
      errors.push(error instanceof Error ? error.message : "JSON Schema must be a valid JSON object.");
    }
  }

  if (settings.citation.enabled) {
    const allowedHosts = normalizedHostInputs(settings.citation.allowedHosts, errors);
    const blockedHosts = normalizedHostInputs(settings.citation.blockedHosts, errors);
    const overlap = allowedHosts.find((host) => blockedHosts.includes(host));
    if (overlap) {
      errors.push(`Citation host cannot be both allowed and blocked: ${overlap}.`);
    }
  }

  if (settings.http.enabled) {
    try {
      const parsed = new URL(settings.http.url.trim());
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        errors.push("HTTP verifier URL must use http or https.");
      }
      if (parsed.username || parsed.password) {
        errors.push("HTTP verifier URL cannot contain credentials.");
      }
    } catch {
      errors.push("HTTP verifier requires an absolute URL.");
    }
  }

  if (settings.command.enabled) {
    if (!settings.command.executable.trim()) {
      errors.push("Command verifier requires an executable.");
    }
    if (isAbsolutePath(settings.command.workingDirectory.trim())) {
      errors.push("Command working directory must be relative.");
    }
    if (settings.command.workingDirectory.split(/[\\/]/).some((segment) => segment === "..")) {
      errors.push("Command working directory cannot escape the verification workspace.");
    }
  }

  return errors;
}

export function enabledVerifierCount(settings: CompletionVerificationSettings): number {
  return [
    settings.textConstraints.enabled,
    settings.citation.enabled,
    settings.jsonSchema.enabled,
    settings.http.enabled,
    settings.command.enabled
  ].filter(Boolean).length;
}

function parseJSONObject(value: string): Record<string, unknown> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error("JSON Schema must be valid JSON.");
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("JSON Schema must be a JSON object.");
  }
  return parsed as Record<string, unknown>;
}

function compactConfig(config: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(config).filter(([, value]) => {
      if (value === 0 || value === false || value === "") return false;
      if (Array.isArray(value) && value.length === 0) return false;
      return true;
    })
  );
}

function lines(value: string): string[] {
  return [...new Set(value.split("\n").map((item) => item.trim()).filter(Boolean))];
}

function normalizedHostInputs(value: string, errors: string[]): string[] {
  return lines(value).map((item) => {
    const host = item.toLowerCase().replace(/\.$/, "");
    if (!host || /[/:@?#]/.test(host)) {
      errors.push(`Citation host must not include a scheme, port, or path: ${item}.`);
    }
    return host;
  });
}

function isAbsolutePath(value: string): boolean {
  return value.startsWith("/") || value.startsWith("\\") || /^[A-Za-z]:[\\/]/.test(value);
}

function clampInteger(value: number, minimum: number, maximum: number): number {
  if (!Number.isFinite(value)) {
    return minimum;
  }
  return Math.min(maximum, Math.max(minimum, Math.round(value)));
}
