import assert from "node:assert/strict";
import test from "node:test";

import {
  DEFAULT_COMPLETION_VERIFICATION,
  buildCompletionContract,
  normalizeCompletionVerification,
  validateCompletionVerification
} from "./verification.ts";

test("ordinary chat omits the completion contract", () => {
  assert.equal(buildCompletionContract(DEFAULT_COMPLETION_VERIFICATION), undefined);
});

test("all six backend verifiers have a compatible frontend contract", () => {
  const contract = buildCompletionContract({
    ...structuredClone(DEFAULT_COMPLETION_VERIFICATION),
    enabled: true,
    answerRelevance: {
      enabled: true,
      minimumScore: 0.45,
      minimumAnswerCharacters: 40
    },
    textConstraints: {
      ...DEFAULT_COMPLETION_VERIFICATION.textConstraints,
      minimumCharacters: 240,
      requiredHeadings: "Findings\nSources"
    },
    citation: {
      ...DEFAULT_COMPLETION_VERIFICATION.citation,
      enabled: true,
      minimumCitations: 3,
      minimumUniqueSources: 2,
      allowedHosts: "openai.com\nanthropic.com"
    },
    jsonSchema: {
      enabled: true,
      schema: `{"type":"object","required":["status"]}`
    },
    http: {
      enabled: true,
      method: "HEAD",
      url: "http://localhost:8080/health",
      expectedStatus: 204
    },
    command: {
      enabled: true,
      executable: "go",
      arguments: "test\n./...",
      workingDirectory: "apps/api"
    }
  });

  assert.deepEqual(contract, {
    subject_type: "run_output",
    verifiers: [
      {
        id: "answer-relevance",
        type: "answer_relevance",
        required: true,
        config: {
          minimum_score: 0.45,
          minimum_answer_characters: 40
        }
      },
      {
        id: "response-text",
        type: "text_constraints",
        required: true,
        config: {
          min_characters: 240,
          required_headings: ["Findings", "Sources"]
        }
      },
      {
        id: "source-policy",
        type: "citation",
        required: true,
        config: {
          min_citations: 3,
          min_unique_hosts: 2,
          require_https: true,
          allowed_hosts: ["openai.com", "anthropic.com"]
        }
      },
      {
        id: "response-schema",
        type: "json_schema",
        required: true,
        config: { schema: { type: "object", required: ["status"] } }
      },
      {
        id: "http-check",
        type: "http",
        required: true,
        config: {
          method: "HEAD",
          url: "http://localhost:8080/health",
          expected_status: 204
        }
      },
      {
        id: "command-check",
        type: "command",
        required: true,
        config: {
          args: ["go", "test", "./..."],
          working_directory: "apps/api"
        }
      }
    ],
    policy: {
      mode: "all_must_pass",
      max_attempts: 2,
      on_exhausted: "waiting_for_user"
    }
  });
});

test("each verifier can be enabled independently", () => {
  const verifierKeys = ["answerRelevance", "textConstraints", "citation", "jsonSchema", "http", "command"];
  const verifierTypes = ["answer_relevance", "text_constraints", "citation", "json_schema", "http", "command"];

  verifierKeys.forEach((enabledKey, index) => {
    const settings = structuredClone(DEFAULT_COMPLETION_VERIFICATION);
    settings.enabled = true;
    verifierKeys.forEach((key) => {
      settings[key].enabled = key === enabledKey;
    });
    const contract = buildCompletionContract(settings);
    assert.equal(contract.verifiers.length, 1);
    assert.equal(contract.verifiers[0].type, verifierTypes[index]);
  });
});

test("invalid enabled verifier settings are rejected before the request", () => {
  const settings = structuredClone(DEFAULT_COMPLETION_VERIFICATION);
  settings.enabled = true;
  settings.textConstraints.enabled = false;
  assert.deepEqual(validateCompletionVerification(settings), ["Enable at least one verifier."]);

  settings.jsonSchema.enabled = true;
  settings.jsonSchema.schema = "not-json";
  assert.deepEqual(validateCompletionVerification(settings), ["JSON Schema must be valid JSON."]);
  assert.throws(() => buildCompletionContract(settings), /JSON Schema must be valid JSON/);
});

test("unsafe host and command paths are rejected before the request", () => {
  const settings = structuredClone(DEFAULT_COMPLETION_VERIFICATION);
  settings.enabled = true;
  settings.citation.enabled = true;
  settings.citation.allowedHosts = "https://example.com";
  settings.command.enabled = true;
  settings.command.workingDirectory = "../outside";

  assert.deepEqual(validateCompletionVerification(settings), [
    "Citation host must not include a scheme, port, or path: https://example.com.",
    "Command working directory cannot escape the verification workspace."
  ]);
});

test("numeric settings are normalized to server limits", () => {
  const settings = structuredClone(DEFAULT_COMPLETION_VERIFICATION);
  settings.textConstraints.minimumCharacters = -1;
  settings.answerRelevance.minimumScore = 2;
  settings.answerRelevance.minimumAnswerCharacters = 0;
  settings.citation.minimumCitations = 101;
  settings.citation.minimumUniqueSources = -1;
  settings.http.expectedStatus = 999;
  settings.maxAttempts = 9;

  const normalized = normalizeCompletionVerification(settings);
  assert.equal(normalized.answerRelevance.minimumScore, 1);
  assert.equal(normalized.answerRelevance.minimumAnswerCharacters, 1);
  assert.equal(normalized.textConstraints.minimumCharacters, 0);
  assert.equal(normalized.citation.minimumCitations, 100);
  assert.equal(normalized.citation.minimumUniqueSources, 0);
  assert.equal(normalized.http.expectedStatus, 599);
  assert.equal(normalized.maxAttempts, 5);
});
