import Link from "next/link";
import type { CSSProperties } from "react";
import {
  Activity,
  ArrowRight,
  ArrowUpRight,
  Bot,
  Braces,
  Check,
  Database,
  FileCheck2,
  Gauge,
  GitBranch,
  Layers3,
  Play,
  Repeat2,
  Search,
  ShieldCheck,
  Users,
  Wrench
} from "lucide-react";

const runtimeStages = [
  { name: "Plan", detail: "4 steps", state: "complete" },
  { name: "Retrieve", detail: "6 grounded chunks", state: "complete" },
  { name: "Act", detail: "3 tool calls", state: "complete" },
  { name: "Verify", detail: "5 checks passed", state: "complete" }
];

const executionModes = [
  {
    icon: Bot,
    label: "Direct",
    title: "Single agent",
    body: "One configured agent handles the request with tools, memory, retrieval, and streaming output."
  },
  {
    icon: Users,
    label: "Coordinate",
    title: "Multi-agent",
    body: "Planner, researcher, worker, and reviewer collaborate through persisted stages and bounded child runs."
  },
  {
    icon: Repeat2,
    label: "Autonomous",
    title: "Bounded loop",
    body: "Observe, plan, act, review, and decide within explicit iteration, runtime, output, and tool limits."
  }
];

const platformCapabilities = [
  {
    icon: Layers3,
    phase: "Configure",
    title: "Agents and execution policy",
    body: "Create reusable agents, scope tools, and freeze the effective model, context, budget, and completion policy per run.",
    details: ["Agent profiles", "Tool security scope", "Frozen runtime snapshot"]
  },
  {
    icon: Database,
    phase: "Ground",
    title: "Memory and hybrid RAG",
    body: "Combine curated memory with versioned knowledge indexes, semantic and keyword recall, RRF fusion, reranking, and relevance gating.",
    details: ["Index compatibility", "Prompt-injection guard", "Calibrated retrieval evaluation"]
  },
  {
    icon: Wrench,
    phase: "Execute",
    title: "Tool-aware orchestration",
    body: "Stream every mode through the same Go runtime with guarded tools, durable stage checkpoints, resumable child runs, and compacted context.",
    details: ["Tool artifact spillover", "Checkpoint recovery", "Context manifest"]
  },
  {
    icon: Gauge,
    phase: "Control",
    title: "Boundaries and runtime limits",
    body: "Bound concurrent runs and model traffic, enforce run budgets, and keep credentials out of snapshots, traces, artifacts, and replay.",
    details: ["Usage ledger", "Backpressure", "Credential redaction"]
  },
  {
    icon: FileCheck2,
    phase: "Verify",
    title: "Evidence-gated completion",
    body: "Treat output as a candidate until its frozen completion contract passes configured deterministic or model-backed checks.",
    details: ["Seven verifier types", "Immutable evidence", "Completion gate"]
  },
  {
    icon: Activity,
    phase: "Inspect",
    title: "Trace, usage, and replay",
    body: "Inspect typed events, retrieval decisions, model inputs, tool calls, usage, recovery actions, and verification evidence for every run.",
    details: ["Run replay", "Model request capture", "Controlled comparison"]
  }
];

const architectureRows = [
  { label: "Runtime", value: "Go orchestration", detail: "Native Turn Engine" },
  { label: "Models", value: "OpenAI-compatible", detail: "Chat and embedding providers" },
  { label: "State", value: "PostgreSQL", detail: "pgvector semantic retrieval" },
  { label: "Transport", value: "HTTP + SSE", detail: "Persisted events and replay projections" }
];

export default function Page() {
  return (
    <main className="home-page">
      <section className="home-hero">
        <nav className="home-nav" aria-label="Home">
          <Link href="/" className="home-logo">
            <span className="brand-mark" aria-hidden="true"><span /></span>
            AgentFlow
          </Link>
          <div className="home-nav-actions">
            <a href="#modes" className="home-nav-link subtle">Modes</a>
            <a href="#platform" className="home-nav-link subtle">Platform</a>
            <a href="#architecture" className="home-nav-link subtle">Architecture</a>
            <Link href="/workspace" className="home-nav-link">
              Open workspace <ArrowUpRight size={15} strokeWidth={1.8} />
            </Link>
          </div>
        </nav>

        <div className="home-runtime-scene" aria-label="Completed AgentFlow run preview">
          <div className="runtime-scene-header">
            <div>
              <span className="runtime-scene-label">RUN</span>
              <code>run_01JQ7M8</code>
            </div>
            <strong><Check size={13} strokeWidth={2.4} /> Completed</strong>
          </div>

          <div className="runtime-scene-main">
            <div className="runtime-mode-line">
              <span>MODE</span>
              <strong>Multi-agent</strong>
              <span className="runtime-line" aria-hidden="true" />
            </div>
            <div className="runtime-stage-list">
              {runtimeStages.map((stage, index) => (
                <div className="runtime-stage" key={stage.name} style={{ "--stage-index": index } as CSSProperties}>
                  <span className="runtime-stage-index">0{index + 1}</span>
                  <span className="runtime-stage-node"><Check size={12} strokeWidth={2.5} /></span>
                  <div>
                    <strong>{stage.name}</strong>
                    <span>{stage.detail}</span>
                  </div>
                  <code>{stage.state}</code>
                </div>
              ))}
            </div>

            <div className="runtime-evidence">
              <div className="runtime-evidence-title">
                <span>EVIDENCE</span>
                <strong>Replay snapshot</strong>
              </div>
              <dl>
                <div><dt>Retrieval</dt><dd>RRF · 6 chunks</dd></div>
                <div><dt>Context</dt><dd>72.4k / 115.7k</dd></div>
                <div><dt>Usage</dt><dd>12,904 tokens</dd></div>
                <div><dt>Verification</dt><dd className="runtime-pass">passed</dd></div>
              </dl>
            </div>
          </div>
        </div>

        <div className="home-hero-inner">
          <div className="home-hero-content">
            <p className="home-kicker">Go-native agent workflow platform</p>
            <h1>AgentFlow</h1>
            <p className="home-hero-statement">Run agents like production systems.</p>
            <p className="home-subtitle">
              Build direct agents, coordinated teams, and bounded autonomous loops with grounded retrieval, tools, budgets, verification, and replay in one workspace.
            </p>
            <div className="home-actions">
              <Link href="/workspace" className="home-primary-action">
                <Play size={15} fill="currentColor" /> Launch workspace
              </Link>
              <a href="#platform" className="home-secondary-action">
                Explore the platform <ArrowRight size={15} />
              </a>
            </div>
          </div>
        </div>
      </section>

      <section className="home-modes" id="modes">
        <div className="home-section">
          <div className="home-section-header">
            <span>Execution modes</span>
            <h2>Choose the runtime shape that fits the task.</h2>
            <p>All three modes share the same tools, retrieval pipeline, run controls, status model, and trace contract.</p>
          </div>
          <div className="mode-list">
            {executionModes.map((mode) => (
              <article className="mode-row" key={mode.title}>
                <mode.icon size={20} strokeWidth={1.7} />
                <span>{mode.label}</span>
                <h3>{mode.title}</h3>
                <p>{mode.body}</p>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className="home-platform" id="platform">
        <div className="home-section">
          <div className="home-section-header">
            <span>Operational lifecycle</span>
            <h2>Control the run from configuration to evidence.</h2>
            <p>AgentFlow keeps the parts that usually disappear inside a framework visible, persisted, and testable.</p>
          </div>
          <div className="capability-list">
            {platformCapabilities.map((capability) => (
              <article className="capability-row" key={capability.phase}>
                <div className="capability-phase">
                  <capability.icon size={19} strokeWidth={1.7} />
                  <span>{capability.phase}</span>
                </div>
                <div className="capability-copy">
                  <h3>{capability.title}</h3>
                  <p>{capability.body}</p>
                </div>
                <ul>
                  {capability.details.map((detail) => <li key={detail}>{detail}</li>)}
                </ul>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className="home-architecture" id="architecture">
        <div className="home-section architecture-inner">
          <div className="architecture-heading">
            <span className="section-index">Platform architecture</span>
            <h2>Native orchestration.<br />Explicit boundaries.</h2>
            <p>A compact stack built to expose execution state instead of hiding it behind a chat abstraction.</p>
            <div className="architecture-signals" aria-label="Architecture capabilities">
              <span><GitBranch size={14} /> Durable run state</span>
              <span><Search size={14} /> Hybrid retrieval</span>
              <span><ShieldCheck size={14} /> Policy checks</span>
              <span><Braces size={14} /> Typed events</span>
            </div>
          </div>
          <div className="architecture-list">
            {architectureRows.map((row) => (
              <div key={row.label}>
                <span>{row.label}</span>
                <strong>{row.value}</strong>
                <code>{row.detail}</code>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="home-final-cta">
        <div className="home-section final-cta-inner">
          <div>
            <span>AgentFlow Platform</span>
            <h2>Start with a prompt. Keep the evidence.</h2>
          </div>
          <Link href="/workspace" className="home-primary-action">
            Open workspace <ArrowUpRight size={15} />
          </Link>
        </div>
      </section>
    </main>
  );
}
