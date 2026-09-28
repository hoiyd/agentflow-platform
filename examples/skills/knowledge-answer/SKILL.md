---
name: knowledge-answer
description: Answer questions from indexed Workspace documents using scoped search, bounded reads, and traceable citations.
metadata:
  agentflow-required-tools: knowledge_search knowledge_read
---
# Knowledge Answer

1. Read [the answer checklist](references/answer-checklist.md).
2. Search Knowledge using the user's actual question. Rewrite a query only to resolve a specific evidence gap.
3. Search previews identify locations, not new citation evidence. Use `knowledge_read` for relevant returned references.
4. Continue from `next_offset` when the necessary evidence exceeds one page. Do not cite text that has not entered model Context.
5. Answer supported claims using returned `[S#]` source identifiers. Explain missing or contradictory evidence rather than filling gaps.

Never switch Workspace, invent a document reference, execute a script, or expand Tool authority.
