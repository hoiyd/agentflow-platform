# Stylesheet organization

Global styles load in two ordered layers from `app/layout.tsx`:

1. The top-level files (`base.css`, `shell.css`, `chat.css`, and related files) contain the original component and page structure. Collaboration components are grouped under `collaboration/`.
2. `workbench/` contains the Precision Workbench theme and UX refinements. Its files must remain in the import order declared in `layout.tsx` because later modules include responsive overrides.

## Workbench modules

- `foundation.css`: design tokens, typography, focus, and shared primitives.
- `home.css`: product home page.
- `shell.css`: workspace navigation, top bar, and task status.
- `chat.css`: mode chooser, messages, markdown, and empty state.
- `composer.css`: composer, agent selector, and agent dialogs.
- `tools-knowledge.css`: tools and knowledge workspace refinements.
- `collaboration.css`: multi-agent DAG, Loop Trace, and trace controls.
- `replay-overlays.css`: run replay, event details, and shared overlays.
- `responsive.css`: breakpoint-specific overrides; keep this last.

Add new rules to the narrowest matching module. Shared tokens belong in `foundation.css`; avoid creating another cross-feature override file.

## Collaboration modules

- `panel.css`: shared trace panel shell and header controls.
- `autonomous.css`: bounded-loop iterations and human input.
- `dag.css`: multi-agent execution graph.
- `plan-steps.css`: plan approval editor and collaboration step output.
