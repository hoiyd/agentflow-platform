import Link from "next/link";

const capabilities = [
  {
    title: "Agent runtime",
    body: "Run single-agent, multi-agent, and autonomous workflows with configurable prompts, tools, retrieval, and executor."
  },
  {
    title: "Memory and RAG",
    body: "Index memories and knowledge documents, search with vector retrieval, rerank results, and inspect retrieved context."
  },
  {
    title: "Replay and evaluation",
    body: "Review run traces, collaboration steps, retrieved memories, retrieved knowledge, and RAG evaluation output."
  }
];

const demoSteps = ["Configure an agent", "Add knowledge", "Run a workflow", "Inspect replay"];
const proofPoints = ["Multi-agent runs", "Semantic memory", "RAG evaluation", "Replay traces"];
const stackItems = ["Native executor", "LangChainGo", "pgvector", "OpenAI-compatible embeddings"];

export default function Page() {
  return (
    <main className="home-page">
      <section className="home-hero">
        <nav className="home-nav" aria-label="Home">
          <Link href="/" className="home-logo">
            AgentFlow
          </Link>
          <div className="home-nav-actions">
            <a href="#demo-flow" className="home-nav-link subtle">
              Demo
            </a>
            <a href="#capabilities" className="home-nav-link subtle">
              Capabilities
            </a>
            <Link href="/workspace" className="home-nav-link">
              Workspace
            </Link>
          </div>
        </nav>

        <div className="home-hero-content">
          <p className="home-kicker">AI agent workflow platform</p>
          <h1>Build, test, and replay agent workflows.</h1>
          <p className="home-subtitle">
            AgentFlow is a focused project workspace for configurable agents, tool use, semantic memory, RAG, evaluation, and replayable execution traces.
          </p>
          <div className="home-actions">
            <Link href="/workspace" className="home-primary-action">
              Open Workspace
            </Link>
            <a href="#demo-flow" className="home-secondary-action">
              Demo Flow
            </a>
          </div>
          <div className="home-proof-row" aria-label="Project highlights">
            {proofPoints.map((point) => (
              <span key={point}>{point}</span>
            ))}
          </div>
        </div>
      </section>

      <section className="home-section home-stack" aria-label="Technical stack">
        <span>What it demonstrates</span>
        <div>
          {stackItems.map((item) => (
            <strong key={item}>{item}</strong>
          ))}
        </div>
      </section>

      <section className="home-section" id="demo-flow">
        <div className="home-section-header">
          <span>Demo flow</span>
          <h2>Show the system end to end</h2>
        </div>
        <div className="home-demo-flow">
          {demoSteps.map((step, index) => (
            <div className="home-demo-step" key={step}>
              <span>{String(index + 1).padStart(2, "0")}</span>
              <strong>{step}</strong>
            </div>
          ))}
        </div>
      </section>

      <section className="home-section home-capabilities" id="capabilities" aria-label="Capabilities">
        {capabilities.map((item) => (
          <article className="home-capability" key={item.title}>
            <h3>{item.title}</h3>
            <p>{item.body}</p>
          </article>
        ))}
      </section>
    </main>
  );
}
