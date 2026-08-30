"use client";

import { Check, Search, Save } from "lucide-react";

import type { RetrievedMemory } from "../../lib/memory-api";
import type { MemoryWorkbenchModel } from "./useMemoryWorkbench";

const MEMORY_KINDS = [
  { value: "fact", label: "Fact" },
  { value: "preference", label: "Preference" },
  { value: "correction", label: "Correction" },
  { value: "project_convention", label: "Project convention" },
  { value: "note", label: "Note" }
];

export function MemoryPanel({ model }: { model: MemoryWorkbenchModel }) {
  return (
    <section className="memory-panel">
      <div className="memory-workbench">
        {model.error ? <div className="error">{model.error}</div> : null}
        <div className="memory-operations">
          <section className="memory-operation memory-write">
            <header className="memory-operation-header">
              <div><span>Write</span><h2>Save memory</h2></div>
            </header>
            <label className="memory-field">
              <span>Kind</span>
              <select disabled={model.isSaving} onChange={(event) => model.setKind(event.target.value)} value={model.kind}>
                {MEMORY_KINDS.map((kind) => <option key={kind.value} value={kind.value}>{kind.label}</option>)}
              </select>
            </label>
            <label className="memory-field memory-content-field">
              <span>Content</span>
              <textarea
                disabled={model.isSaving}
                onChange={(event) => model.setContent(event.target.value)}
                placeholder="A durable fact, preference, correction, or project convention"
                value={model.content}
              />
            </label>
            <div className="memory-action-row">
              <button className="send memory-primary-action" disabled={model.isSaving || !model.content.trim()} onClick={model.saveMemory} type="button">
                <Save size={15} /> {model.isSaving ? "Saving..." : "Save memory"}
              </button>
            </div>
            {model.lastCreated ? (
              <div className="memory-saved" role="status">
                <Check size={15} />
                <div><strong>Saved</strong><span>{model.lastCreated.content}</span></div>
                <code>{model.lastCreated.kind}</code>
              </div>
            ) : null}
          </section>

          <section className="memory-operation memory-recall">
            <header className="memory-operation-header">
              <div><span>Recall</span><h2>Search memory</h2></div>
              <label className="memory-limit">
                <span>Limit</span>
                <select disabled={model.isSearching} onChange={(event) => model.setLimit(Number(event.target.value))} value={model.limit}>
                  <option value={5}>5</option>
                  <option value={10}>10</option>
                  <option value={20}>20</option>
                </select>
              </label>
            </header>
            <form className="memory-search" onSubmit={(event) => { event.preventDefault(); void model.search(); }}>
              <input
                aria-label="Memory search query"
                disabled={model.isSearching}
                onChange={(event) => model.setQuery(event.target.value)}
                placeholder="Search long-term memory"
                value={model.query}
              />
              <button className="send memory-primary-action" disabled={model.isSearching || !model.query.trim()} type="submit">
                <Search size={15} /> {model.isSearching ? "Searching..." : "Search"}
              </button>
            </form>
            <MemoryResults hasSearched={model.hasSearched} items={model.results} />
          </section>
        </div>
      </div>
    </section>
  );
}

function MemoryResults({ hasSearched, items }: { hasSearched: boolean; items: RetrievedMemory[] }) {
  if (!hasSearched) {
    return <div className="memory-empty">No recall query yet.</div>;
  }
  if (items.length === 0) {
    return <div className="memory-empty">No matching memory.</div>;
  }
  return (
    <div className="memory-results">
      {items.map((item, index) => (
        <article className="memory-result" key={item.memory.id}>
          <div className="memory-rank"><span>{String(index + 1).padStart(2, "0")}</span><i style={{ width: `${scoreWidth(item.score)}%` }} /></div>
          <div className="memory-result-main">
            <div className="memory-result-heading">
              <strong>{item.memory.kind.replaceAll("_", " ")}</strong>
              <code>{formatScore(item.score)}</code>
            </div>
            <p>{item.memory.content}</p>
            <div className="memory-result-meta">
              <span>Similarity {formatScore(item.similarity)}</span>
              <span>Recency +{formatScore(item.recency_boost)}</span>
              {item.memory.conversation_id ? <span>Conversation {shortID(item.memory.conversation_id)}</span> : null}
              {item.memory.run_id ? <span>Run {shortID(item.memory.run_id)}</span> : null}
              <time dateTime={item.memory.created_at}>{formatDate(item.memory.created_at)}</time>
            </div>
          </div>
        </article>
      ))}
    </div>
  );
}

function scoreWidth(score: number) {
  return Math.round(Math.max(0, Math.min(1, Number.isFinite(score) ? score : 0)) * 100);
}

function formatScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(3) : "0.000";
}

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Date unavailable" : date.toLocaleString();
}

function shortID(value: string) {
  return value.length > 16 ? `${value.slice(0, 13)}...` : value;
}
