import Link from "next/link";
import {
  ArrowRight, ArrowUpRight, AudioLines, Bot, Braces, Database, FileCheck2,
  Layers3, Repeat2, SlidersHorizontal, Users, Wrench
} from "lucide-react";
import { ProductDemo } from "../components/home/ProductDemo";

const repository = "https://github.com/hoiyd/agentflow-platform";

const executionModes = [
  {
    icon: Bot,
    title: "Single agent",
    shape: "Request / Tool rounds / Answer",
    body: "One Agent owns the task, calls tools as needed, and streams its answer.",
    use: "Direct requests and tool-assisted work"
  },
  {
    icon: Users,
    title: "Multi-agent",
    shape: "Plan / Approve / Execute / Review",
    body: "Approve a plan, route work to an isolated Worker stage, then review and finalize within one Run.",
    use: "Planned work with a separate review stage"
  },
  {
    icon: Repeat2,
    title: "Bounded loop",
    shape: "Observe / Act / Review / Decide",
    body: "Iterate on results within explicit limits, or pause when the next action needs your input.",
    use: "Tasks that need feedback between actions"
  }
];

const capabilities = [
  {
    icon: AudioLines,
    title: "Keep work moving",
    body: "Admitted Runs keep executing when you close the browser. Reconnect to persisted progress, steer the current task, or queue a separate follow-up.",
    detail: "Steering adds a correction at the next safe boundary. A follow-up starts a new Run; it does not change the task in progress.",
    doc: "runtime/durable-inputs.md"
  },
  {
    icon: Wrench,
    title: "Tools and trusted Skills",
    body: "Search the web, read scoped Knowledge, and use operator-authorized sandbox commands through a shared, validated Tool contract. Bind reviewed Skills as reusable task methods.",
    detail: "Tool progress, bounded multi-round calls, result Artifacts, and source citations remain inspectable. Skills do not grant execution permissions.",
    doc: "tools/trusted-skills.md"
  },
  {
    icon: Database,
    title: "Grounded, bounded context",
    body: "Combine curated Memory and hybrid retrieval with reranking and relevance gates. Keep source identities, Task State, and context limits visible through compaction.",
    detail: "Context Manifests explain what was assembled. Optional, policy-controlled request capture retains redacted model inputs for deeper debugging.",
    doc: "context/context-management.md"
  },
  {
    icon: SlidersHorizontal,
    title: "Model controls and visibility",
    body: "Freeze routes and sampling per Run. Inspect per-attempt timing, generation outcomes, usage provenance, cache-aware cost estimates, and overload waits.",
    detail: "Stream answers through Tool rounds. Provider-supplied reasoning is separately displayed only for explicitly configured, supported response formats.",
    doc: "runtime/model-routing.md"
  },
  {
    icon: FileCheck2,
    title: "Completion backed by evidence",
    body: "Opt in to a completion contract, inspect its checks, and review Run events, usage, recovery actions, and Needs attention in the workbench.",
    detail: "Passing verification is evidence for configured checks, not proof of factual accuracy. Controlled Run comparison stays in a separate evaluation surface.",
    doc: "runtime/verification.md"
  },
  {
    icon: Layers3,
    title: "Owner-scoped Workspaces",
    body: "Use OIDC sign-in and personal Workspace onboarding. Create, rename, archive, or soft-delete owned Workspaces while keeping business resources scoped to their owner.",
    detail: "Local mode is a trusted operator mode. Agent profiles, model routes, Tool policy, and installed Skills remain service-managed configuration.",
    doc: "operations/workspace-lifecycle.md"
  }
];

const architecture = [
  { label: "Workbench", value: "Next.js + React", detail: "Streaming Chat, Trace, and Replay" },
  { label: "Agent runtime", value: "Go Turn Engine", detail: "Shared Tool and completion boundaries" },
  { label: "Inference", value: "Model routes", detail: "OpenAI-compatible provider adapters" },
  { label: "Persistence", value: "Postgres + pgvector", detail: "Run events, checkpoints, and retrieval" }
];

export default function Page() {
  return (
    <main className="home-page">
      <section className="home-hero" aria-labelledby="home-title">
        <nav className="home-nav" aria-label="Home">
          <Link href="/" className="home-logo">
            <span className="brand-mark" aria-hidden="true"><span /></span> AgentFlow
          </Link>
          <div className="home-nav-actions">
            <a href="#modes" className="home-nav-link subtle">Modes</a>
            <a href="#demo" className="home-nav-link subtle">Demos</a>
            <a href="#platform" className="home-nav-link subtle">Capabilities</a>
            <a href="#architecture" className="home-nav-link subtle">Architecture</a>
            <a href={repository} className="home-nav-link subtle"><Braces size={15} /> Source</a>
            <Link href="/workspace" className="home-nav-link">Open workspace <ArrowUpRight size={15} /></Link>
          </div>
        </nav>

        <div className="home-intro">
          <div>
            <p className="home-kicker">Go-native AI Agent runtime</p>
            <h1 id="home-title">AgentFlow</h1>
            <p className="home-statement">Execute. Inspect. Stay in control.</p>
          </div>
          <div className="home-intro-copy">
            <p>Run a single Agent, coordinate a team, or iterate in a bounded loop. Keep tools, context, model usage, and completion evidence in the same workbench.</p>
            <div className="home-actions">
              <Link href="/workspace" className="home-primary-action">Launch workspace <ArrowRight size={16} /></Link>
              <a href="#demo" className="home-secondary-action">Watch demos <ArrowRight size={15} /></a>
            </div>
          </div>
        </div>

      </section>

      <section className="home-modes home-section" id="modes" aria-labelledby="modes-title">
        <header className="home-section-header">
          <p className="home-kicker">Execution modes</p>
          <h2 id="modes-title">Use the least orchestration the task needs.</h2>
          <p>Three execution shapes. One shared runtime, Tool contract, and event history.</p>
        </header>
        <div className="home-mode-list">
          {executionModes.map(mode => (
            <article className="home-mode" key={mode.title}>
              <h3><mode.icon size={19} strokeWidth={1.7} />{mode.title}</h3>
              <p className="home-mode-shape">{mode.shape}</p>
              <p>{mode.body}</p>
              <span>{mode.use}</span>
            </article>
          ))}
        </div>
      </section>

      <ProductDemo />

      <section className="home-platform" id="platform" aria-labelledby="platform-title">
        <div className="home-section home-platform-inner">
          <header className="home-section-header">
            <p className="home-kicker">Runtime capabilities</p>
            <h2 id="platform-title">The work behind the answer.</h2>
            <p>Follow what the Agent used, what changed, and where your input is needed.</p>
            <a className="home-text-link" href={`${repository}/blob/main/docs/README.md`}>Read the documentation <ArrowUpRight size={15} /></a>
          </header>
          <div className="home-capability-list">
            {capabilities.map(capability => (
              <article className="home-capability" key={capability.title}>
                <capability.icon size={20} strokeWidth={1.7} aria-hidden="true" />
                <div>
                  <h3><a href={`${repository}/blob/main/docs/${capability.doc}`}>{capability.title}<ArrowUpRight size={14} /></a></h3>
                  <p>{capability.body}</p>
                  <p className="home-capability-detail">{capability.detail}</p>
                </div>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className="home-architecture" id="architecture" aria-labelledby="architecture-title">
        <div className="home-section home-architecture-inner">
          <header className="home-section-header">
            <p className="home-kicker">Architecture and boundaries</p>
            <h2 id="architecture-title">Explicit state.<br />Shared execution paths.</h2>
            <p>HTTP and SSE connect the workbench to a Go runtime. Persisted events support observation and Replay; frozen configuration governs execution.</p>
          </header>
          <dl className="home-architecture-list">
            {architecture.map(row => (
              <div key={row.label}>
                <dt>{row.label}</dt>
                <dd><strong>{row.value}</strong><span>{row.detail}</span></dd>
              </div>
            ))}
          </dl>
          <div className="home-boundaries">
            <strong>Deployment boundary</strong>
            <p>Single-instance execution, not distributed scheduling. Browser disconnects do not cancel Runs; process crashes still require checkpoint repair and explicit, mode-supported Resume. Tools and Skills cannot grant themselves permissions.</p>
            <a href={`${repository}/blob/main/docs/operations/production-readiness-roadmap.md`}>Operational limits <ArrowUpRight size={14} /></a>
          </div>
        </div>
      </section>

      <footer className="home-footer home-section">
        <div><span className="home-kicker">AgentFlow</span><h2>Start a task. Keep its history.</h2></div>
        <Link href="/workspace" className="home-primary-action">Open workspace <ArrowUpRight size={16} /></Link>
      </footer>
    </main>
  );
}
