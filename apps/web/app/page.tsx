import Link from "next/link";
import { Activity, ArrowUpRight, Check, Database, GitBranch, Play, Terminal } from "lucide-react";

const workflowSteps = [
  {
    title: "Plan",
    detail: "4 tasks generated",
    duration: "0.8s"
  },
  {
    title: "Research",
    detail: "3 tools · 12 sources",
    duration: "6.4s"
  },
  {
    title: "Synthesize",
    detail: "2,418 tokens",
    duration: "3.1s"
  },
  {
    title: "Review",
    detail: "Quality gate passed",
    duration: "1.2s"
  }
];

const platformLayers = [
  {
    icon: GitBranch,
    title: "Orchestrate",
    body: "Move from a direct agent call to planned collaboration and bounded autonomous loops without changing workspaces."
  },
  {
    icon: Database,
    title: "Ground",
    body: "Attach tools, semantic memory, and indexed knowledge. Inspect exactly what entered the model context."
  },
  {
    icon: Activity,
    title: "Observe",
    body: "Follow execution live, then replay every step, tool call, retrieval, token, latency, and failure."
  }
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
            <a href="#platform" className="home-nav-link subtle">
              Platform
            </a>
            <a href="#architecture" className="home-nav-link subtle">
              Architecture
            </a>
            <Link href="/workspace" className="home-nav-link">
              Open workspace <ArrowUpRight size={15} strokeWidth={1.8} />
            </Link>
          </div>
        </nav>

        <div className="home-hero-content">
          <p className="home-kicker">Agent runtime · OpenAI compatible</p>
          <h1>AgentFlow</h1>
          <p className="home-hero-statement">Build agent systems you can inspect.</p>
          <p className="home-subtitle">
            Configure agents, coordinate work, call tools, and follow every decision from prompt to result in one operational workspace.
          </p>
          <div className="home-actions">
            <Link href="/workspace" className="home-primary-action">
              <Play size={16} fill="currentColor" /> Launch workspace
            </Link>
            <a href="#platform" className="home-secondary-action">
              See how runs work
            </a>
          </div>
        </div>

        <div className="product-frame" aria-label="Agent workflow execution preview">
          <div className="product-frame-bar">
            <div className="product-frame-title">
              <span className="brand-mark small" aria-hidden="true"><span /></span>
              <span>Travel research</span>
              <code>run_01JQ7M8</code>
            </div>
            <div className="run-live"><span /> completed in 11.5s</div>
          </div>
          <div className="product-frame-body">
            <section className="execution-map">
              <div className="preview-label">Execution</div>
              <div className="execution-rail">
                {workflowSteps.map((step) => (
                  <div className="execution-step" key={step.title}>
                    <span className="execution-node"><Check size={11} strokeWidth={2.5} /></span>
                    <div>
                      <strong>{step.title}</strong>
                      <span>{step.detail}</span>
                    </div>
                    <code>{step.duration}</code>
                  </div>
                ))}
              </div>
            </section>
            <section className="execution-output">
              <div className="preview-label">Final output</div>
              <h2>Osaka in late July: high-friction, feasible with constraints.</h2>
              <p>
                The run compared live flight options, hotel availability, heat index, and the Tenjin Matsuri calendar before producing a recommendation.
              </p>
              <div className="output-facts">
                <span><strong>12</strong> sources</span>
                <span><strong>3</strong> tool calls</span>
                <span><strong>4</strong> agents</span>
              </div>
              <div className="output-command"><Terminal size={14} /> Replay run <ArrowUpRight size={14} /></div>
            </section>
          </div>
        </div>
      </section>

      <section className="home-section" id="platform">
        <div className="home-section-header">
          <span>One operational surface</span>
          <h2>From prompt to production evidence.</h2>
          <p>Each layer stays visible and inspectable, so agent behavior can be operated like software.</p>
        </div>
        <div className="platform-layers">
          {platformLayers.map((item) => (
            <article className="platform-layer" key={item.title}>
              <item.icon size={19} strokeWidth={1.7} />
              <h3>{item.title}</h3>
              <p>{item.body}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="home-architecture" id="architecture">
        <div className="home-section architecture-inner">
          <div>
            <span className="section-index">Runtime architecture</span>
            <h2>Built as a platform,<br />not a chat wrapper.</h2>
          </div>
          <div className="architecture-list">
            <div><code>01</code><span>Go orchestration runtime</span><strong>Native + LangChainGo</strong></div>
            <div><code>02</code><span>State and retrieval</span><strong>PostgreSQL + pgvector</strong></div>
            <div><code>03</code><span>Model gateway</span><strong>OpenAI-compatible APIs</strong></div>
            <div><code>04</code><span>Observability</span><strong>SSE events + replay traces</strong></div>
          </div>
        </div>
      </section>
    </main>
  );
}
