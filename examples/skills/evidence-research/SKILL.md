---
name: evidence-research
description: Research a factual question using source-backed evidence; use when findings must distinguish supported claims from uncertainty.
metadata:
  agentflow-required-tools: web_search
---
# Evidence Research

1. Identify the factual question and the expected time horizon from the user request.
2. Read [the evidence checklist](references/evidence-checklist.md).
3. Use only the frozen available Tools. Search for primary sources; a search snippet is not proof that the full page was read.
4. Compare evidence, identify gaps and disagreements, and never invent a citation.
5. Answer the question directly, using available `[W#]` or `[S#]` identifiers for supported claims. State uncertainty where evidence is insufficient.

This method never authorizes another Tool, credential, script, or Workspace.
