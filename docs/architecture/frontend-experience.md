# Frontend Experience Principles

AgentFlow uses a desktop-first **Precision Workbench** direction. The interface
should feel like an operational tool for repeated execution and inspection, not
a marketing template or a collection of AI-themed cards.

This document records product-level design constraints. Stylesheet ownership is
documented separately in
[apps/web/app/styles/README.md](../../apps/web/app/styles/README.md).

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

- The landing first viewport establishes the brand, literal product offer,
  concise supporting copy, primary actions, and execution modes. Product recordings
  live in a separate, content-aligned demo section rather than a cropped hero backdrop.
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
- Single mode uses one 40px toolbar row: Agent/Skill selection on the left,
  profile actions and separated Verification on the right. Show the Agent name
  once; expand its description below on request. New agent is the quieter action.
- Agent, Workspace and template pickers share searchable, wrapping options and
  stable IDs. Agent menus open above the composer with one bounded scroll owner;
  closed names use at most two lines with full-name hover. Arrow keys browse,
  Enter selects, Escape dismisses.
- The non-searchable Skill picker keeps a fixed-width slot in Single mode.
  Disabled **No skills** explains missing bindings or no selected Agent through
  a custom hover/focus note, not a native tooltip; Escape dismisses it without
  layout shift. **Automatic** removes only the explicit Skill command.
- Keep template creation separate from runtime selection; see the
  [Agent creation flow](../runtime/agent-profiles.md#creation-and-selection).
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
- Label recorded workbench media as examples, not live telemetry. Link capability
  descriptions to their owning documents and state important opt-in/provider or
  deployment limits instead of publishing invented metrics or fixed feature counts.
- Keep product recordings at their native aspect ratio and no wider than their
  source resolution. The landing walkthroughs use native 2880 x 1800 browser
  captures displayed within a 960 CSS pixel content width. Use GIFs without video-player chrome; provide a
  compact pause control and a static image for reduced motion. Do not re-encode
  from compressed videos or upscale a source to claim higher recording quality.
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

## Run Session Ownership

`components/chat/useRunSession.ts` owns submission, Continue, Resume, Cancel,
optimistic drafts, and durable event observation. It reuses the existing event
projection and request-lease helpers, not a second Run engine. `ChatShell` keeps
page layout; `useConversationHistory` owns Messages/Task State/Run trace recovery
and accepts completed reads only for the current navigation. Task State refresh
has its own lease: it cannot cancel history loading or overwrite a newer refresh.
URL and sidebar navigation share activation cleanup, so pending reads cannot
leave the previous conversation visible. A failed trace refresh keeps the current
conversation's accepted Run status; switching conversations clears it first.
Partial trace failures preserve messages and the known Run status. Agent, Knowledge, Memory, and Verification
settings retain their separate owners.

Three dimensions must remain separate:

- **Run status** is the backend's durable business state, including waiting and
  terminal outcomes. A connection ending is not a completed Run.
- **Command activity** admits one submission, continuation, or resume at a time,
  including repeated callbacks before React renders. Cancellation may overlap
  that command; it is not another mutually exclusive execution state.
- **Connection leases** invalidate obsolete stream, cancellation, and observation
  callbacks. Navigating away aborts browser requests, not the backend Run; only
  an explicit Cancel requests backend cancellation.

Before the first accepted event, a failed submission removes only its optimistic
drafts and restores input, Run/Trace state, and layout. Once events are accepted,
partial output stays visible; a transport failure can reconnect to the durable
Run rather than resubmit the task. Continue/Resume failures remove their own
drafts and retain editable input; Resume restores the previous waiting status.

Historical events rebuild Trace details without replacing the canonical current
status. Observation rejects mismatched Run/conversation snapshots and reloads
persisted messages once when the Run stops or waits for input. Late cancellation
responses cannot overwrite a terminal Run or another conversation. See
[functional regression gates](../operations/functional-regression-testing.md)
for the affected race and browser-to-backend checks.

## Replay Read and Action Errors

`useReplayData` owns Replay/Episode reads; `RunReplay` keeps recovery commands
separate. Only initial Replay failure replaces the page. Refresh failures are
local warnings and command failures retain the last readable evidence and event
selection. Episode report failure remains a secondary warning.

Resume changes status only on accepted server events, not on click. Before
acceptance, rejection leaves the original status intact; after acceptance,
transport failure cannot roll it back. Repeated clicks are guarded, and accepted
status disables stale Resume actions. Read leases reject obsolete results;
changing Run identity aborts browser requests, not server execution. See
[durable recovery](../runtime/durable-recovery.md) for backend recovery rules.
