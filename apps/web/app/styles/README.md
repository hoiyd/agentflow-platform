# Stylesheet Organization

The frontend currently uses a transitional two-layer stylesheet structure. The
first layer preserves original component geometry; the Precision Workbench
layer owns the current visual system and focused UX corrections. This is an
explicit migration state, not a recommendation to keep adding global overrides.

Global styles load in two ordered layers from `app/layout.tsx`:

1. The top-level files (`base.css`, `shell.css`, `chat.css`, and related files) contain the original component and page structure. Collaboration components are grouped under `collaboration/`.
2. `workbench/` contains the Precision Workbench theme and UX refinements. Its files must remain in the import order declared in `layout.tsx` because later modules include responsive overrides.

## Workbench Modules

- `foundation.css`: design tokens, typography, focus, and shared primitives.
- `home.css`: product home page.
- `shell.css`: workspace navigation, top bar, and task status.
- `chat.css`: mode chooser, messages, markdown, and empty state.
- `task-state.css`: Conversation Task State trigger, inspector layout, and fact rows.
- `composer.css`: composer, agent selector, and agent dialogs.
- `tools-knowledge.css`: tools and knowledge workspace refinements.
- `memory.css`: manual semantic-memory write and recall workspace.
- `collaboration.css`: multi-agent DAG, Loop Trace, and trace controls.
- `replay-overlays.css`: run replay, event details, and shared overlays.
- `runtime-diagnostics.css`: event-derived protocol diagnostics shown on Run Replay.
- `task-state-replay.css`: bounded Task State Revision projection on Run Replay.
- `responsive.css`: breakpoint-specific overrides; keep this last.

Add new rules to the narrowest matching module. Shared tokens belong in `foundation.css`; avoid creating another cross-feature override file.

## Collaboration Modules

- `panel.css`: shared trace panel shell and header controls.
- `autonomous.css`: bounded-loop iterations and human input.
- `dag.css`: multi-agent execution graph.
- `plan-steps.css`: plan approval editor and collaboration step output.

## Change Rules

1. Search both layers before adding a selector so an existing rule can be moved
   or narrowed instead of overridden again.
2. Keep feature selectors in their owning module; do not create catch-all fix
   files.
3. Preserve the import order in `app/layout.tsx`.
4. Avoid nested cards and nested vertical scroll regions.
5. Put shared color, spacing, typography, and focus tokens in
   `workbench/foundation.css`.
6. When a legacy rule is fully superseded, remove it and its override together.

The long-term direction is to make the Workbench modules authoritative and
retire duplicated legacy rules incrementally, with layout tests after each
ownership move.
