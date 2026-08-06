#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const corpusRoot = path.join(root, "examples", "knowledge", "golden-v1");
const manifestPath = path.join(corpusRoot, "corpus-manifest.v1.json");
const datasetPath = path.join(root, "examples", "knowledge", "golden-dataset.v1.json");
const apiBase = (process.env.AGENTFLOW_API_BASE_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const seedOnly = process.argv.includes("--seed-only");
const enforce = process.argv.includes("--enforce");

const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
const dataset = JSON.parse(await readFile(datasetPath, "utf8"));

const existingDocuments = await requestJSON("/api/documents");
const existingSourceURIs = new Set(
  Array.isArray(existingDocuments) ? existingDocuments.map((document) => document.source_uri).filter(Boolean) : []
);

for (const document of manifest.documents) {
  if (existingSourceURIs.has(document.file)) {
    process.stdout.write(`skip   ${document.file} (already indexed)\n`);
    continue;
  }
  const content = await readFile(path.join(corpusRoot, document.file), "utf8");
  await requestJSON("/api/documents", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      title: document.title,
      content,
      source_type: "markdown",
      source_uri: document.file,
      mime_type: "text/markdown",
      metadata: {
        ...document.metadata,
        golden_dataset_id: manifest.dataset_id,
        golden_corpus_version: manifest.version
      }
    })
  });
  process.stdout.write(`seeded ${document.file}\n`);
}

if (seedOnly) {
  process.stdout.write(`Seeded Golden Dataset corpus ${manifest.dataset_id}@${manifest.version}.\n`);
  process.exit(0);
}

const evaluation = await requestJSON("/api/rag/evaluations/run", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ dataset, top_k: 5, min_similarity: 0.15 })
});

process.stdout.write(`\n${evaluation.dataset.id}@${evaluation.dataset.version}\n`);
process.stdout.write(
  `total=${evaluation.summary.total} hit@1=${evaluation.summary.hit_at_1} hit@3=${evaluation.summary.hit_at_3} ` +
    `hit@5=${evaluation.summary.hit_at_5} misses=${evaluation.summary.misses} blocked=${evaluation.summary.blocked_candidates || 0}\n\n`
);

for (const result of evaluation.cases) {
  const classification = result.tags?.includes("non-blocking") ? "diagnostic" : "gating";
  const detail = result.hit ? (result.answerable ? `rank ${result.best_rank}` : "correct no-answer") : result.failure_reason;
  process.stdout.write(`${result.hit ? "PASS" : "MISS"} [${classification}] ${result.id}: ${detail}\n`);
}

const gatingMisses = evaluation.cases.filter((result) => !result.hit && !result.tags?.includes("non-blocking"));
if (gatingMisses.length > 0) {
  process.stdout.write(`\n${gatingMisses.length} gating case(s) missed. Inspect the ranked evidence before changing thresholds.\n`);
  if (enforce) {
    process.exitCode = 1;
  }
}

async function requestJSON(route, options) {
  let response;
  try {
    response = await fetch(`${apiBase}${route}`, options);
  } catch (error) {
    throw new Error(`Cannot reach AgentFlow API at ${apiBase}: ${error.message}`);
  }
  const body = await response.text();
  if (!response.ok) {
    throw new Error(`${options?.method || "GET"} ${route} failed (${response.status}): ${body}`);
  }
  return body ? JSON.parse(body) : null;
}
