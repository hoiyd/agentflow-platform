# Stylesheet Organization

Global styles load from `app/layout.tsx`. Each feature has one authoritative
module set: component geometry and Workbench styling live together, without a
legacy stylesheet underneath a second override file.

## Owners

| Module | Responsibility |
| --- | --- |
| `workbench/foundation.css` | Tokens, typography, focus, shared form and dialog primitives |
| `workbench/selection-menu.css`, `disabled-control-note.css` | Shared pickers and disabled-control notes |
| `workbench/home*.css` | Product home, recordings and capability inventory |
| `workbench/navigation.css`, `shell.css` | Sidebar navigation, top bar and task status |
| `workbench/chat.css`, `markdown.css`, `generation-evidence.css` | Chat layout, rendered content, reasoning and partial output |
| `workbench/composer.css`, `agent-toolbar.css`, `agent-dialog.css` | Composer, Agent/Skill selection, Agent creation/configuration |
| `workbench/verification.css` | Completion-policy dialog |
| `workbench/tools.css`, `memory.css` | Tool availability and curated memory workspace |
| `workbench/replay.css`, `run-report.css`, `trace-detail.css` | Replay layout, report, timeline and event detail |
| `workbench/tool-progress.css`, `task-state*.css` | Shared Tool progress and Task State consumers |
| `workbench/evidence-comparison.css`, `runtime-diagnostics.css`, `recovery-summary.css`, `operator-attention.css` | Focused evaluation and diagnostic surfaces |
| `collaboration/panel.css` | Shared trace shell and controls |
| `collaboration/autonomous.css` | Loop metrics, iterations and human input |
| `collaboration/dag.css` | Multi-Agent execution graph |
| `collaboration/plan-steps.css` | Plan approval, step output and attachments |
| `knowledge/layout.css`, `documents.css`, `retrieval.css`, `evaluation.css` | Knowledge shell and task-specific sections |
| `run-usage.css` | Usage ledger and Budget detail |
| `workbench/identity.css` | Authentication and Workspace selection |
| `workbench/responsive.css` | Cross-feature breakpoint overrides and reduced motion |

## Change Rules

- Search the owning module before adding a rule; amend it instead of creating
  another override layer. Shared primitives belong in `foundation.css`.
- Preserve import and conditional-rule order. Some inherited breakpoint rules
  precede their feature's base rules intentionally to preserve the existing
  cascade; do not move them to the end without checking computed styles.
- Keep one vertical scroll owner for each work surface; trace content expands
  inside its shared scroll area.
- Desktop is the supported surface. Preserve existing breakpoint behavior, but
  do not add a mobile redesign unless requested.
