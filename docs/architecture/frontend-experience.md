# Frontend Experience Principles

AgentFlow uses a desktop-first **Precision Workbench** direction. The interface
should feel like an operational tool for repeated execution and inspection, not
a marketing template or a collection of AI-themed cards.

This document records product-level design constraints. Stylesheet ownership is
documented separately in
[apps/web/app/styles/README.md](apps/web/app/styles/README.md).

## Product Priorities

1. Keep the active task, Run status, mode controls, and primary action visible.
2. Make Single-Agent, Multi-Agent, and Autonomous behavior distinct without
   changing the basic interaction model.
3. Treat Trace, Replay, Knowledge, Tools, and Verification as inspectable
   operational surfaces.
4. Prefer information density that supports scanning over decorative empty
   space.
5. Preserve one clear scroll owner for each page region; avoid nested vertical
   scrolling.

Mobile optimization is outside the current project scope. Desktop layouts must
remain stable across normal laptop and wide-monitor viewports.

## Visual Direction

- Use a restrained neutral surface, dark ink, one blue interaction accent, and
  semantic status colors only where state requires them.
- Build depth with hairlines, tonal surface changes, and a subtle technical grid
  where appropriate. Avoid atmospheric gradients, glowing decoration, and
  abstract AI imagery.
- Keep corners small and consistent. A border, shadow, or background must clarify
  interaction or hierarchy.
- Use Manrope for interface and display text, IBM Plex Sans for dense workbench
  content, and IBM Plex Mono only for IDs, ranks, tokens, and machine values.
- Letter spacing remains neutral. Typography should derive hierarchy from size,
  weight, line height, and spacing.

## Layout Rules

- The landing first viewport is one composition: brand, literal product offer,
  concise supporting copy, primary actions, and a representative runtime view.
- Operational pages use full-height workbench geometry with predictable
  navigation, conversation, trace, and composer regions.
- The sidebar may collapse to release horizontal space.
- Single-Agent content expands when no trace panel is present. Multi-Agent and
  Autonomous content use the same width when their trace panel is hidden.
- Opening a trace creates one intentional split; hiding it restores the shared
  content width.
- Do not place cards inside cards or style ordinary page sections as floating
  cards.

## Interaction Rules

- Use icons for familiar actions and add tooltips when meaning is not obvious.
- Use segmented controls for mode selection, tabs for views, toggles or
  checkboxes for binary policy, and inputs or steppers for numeric limits.
- Primary and secondary actions must remain visually distinguishable. Paired
  actions such as Save and Cancel use consistent dimensions.
- Agent creation and configuration belong in dialogs rather than consuming the
  conversation's vertical workspace.
- Dialogs are horizontally centered and positioned above the visual midpoint so
  their primary fields remain easy to scan.
- Trace show/hide controls use the same style, side, and placement in Multi-Agent
  and Autonomous modes.
- Status must remain visible outside the composer footer and must not disappear
  when mode tabs or trace panels change.

## Scrolling and Responsive Constraints

- The page owns vertical scrolling unless a bounded data tool has a clear reason
  to own it.
- Trace panels grow with content or share the page scroll. Do not create a
  scrollable panel containing another vertically scrollable list.
- Dense trace metadata may scroll horizontally only when its content exceeds the
  available width; do not reserve an empty scrollbar.
- Headers, mode tabs, status controls, and the composer use stable tracks and
  minimum sizes so dynamic content cannot push them off screen.
- Long Agent names and option labels should wrap or resize within their control
  before truncation is considered.
- Text must not overlap controls or adjacent content at supported desktop widths.

## Landing Page Rules

- The product name is the largest first-viewport signal.
- The headline describes the literal product category or operating benefit; it
  does not use generic AI transformation language.
- The runtime visual shows real platform concepts such as stages, retrieval,
  tools, usage, and Verification.
- No badges, metric strips, floating callouts, testimonial blocks, or feature
  card grids belong in the hero.
- A hint of the next section remains visible to establish page continuation.

## Implementation Discipline

- Follow existing React patterns and the repository's component boundaries.
- Use Lucide icons rather than handwritten SVG controls when an icon exists.
- Add rules to the narrowest stylesheet module; shared tokens belong in the
  foundation layer.
- Prefer stable CSS grid tracks, `minmax`, explicit aspect ratios, and bounded
  dimensions over viewport-font scaling.
- Validate behavior through lint, tests, and production build. Use browser-level
  visual tests when a task changes layout or interaction geometry.
