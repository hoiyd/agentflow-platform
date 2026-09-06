"use client";

import { Check, Search, Save, Pencil, Trash2, History } from "lucide-react";
import { useState } from "react";

import type { MemoryInfo, RetrievedMemory } from "../../lib/memory-api";
import { MemoryMutationDialog, type MemoryDialogMode } from "./MemoryMutationDialog";
import type { MemoryWorkbenchModel } from "./useMemoryWorkbench";

const MEMORY_KINDS = [
  { value: "fact", label: "Fact" },
  { value: "preference", label: "Preference" },
  { value: "correction", label: "Correction" },
  { value: "project_convention", label: "Project convention" },
  { value: "note", label: "Note" }
];

export function MemoryPanel({ model }: { model: MemoryWorkbenchModel }) {
  const [selection, setSelection] = useState<{ memory: Pick<MemoryInfo, "id" | "content">; mode: MemoryDialogMode } | null>(null);
  const [lookupID, setLookupID] = useState("");
  const open = (memory: MemoryInfo, mode: MemoryDialogMode) => setSelection({ memory, mode });
  return (
    <section className="memory-panel">
      <div className="memory-workbench">
        {model.error ? <div className="error">{model.error}</div> : null}
        {model.lastChanged ? (
          <div className="memory-change-notice" role="status">
            <span>Memory {model.lastChanged.deleted_at ? "deleted" : "corrected"} · Version {model.lastChanged.version}</span>
            <button className="secondary-action" onClick={() => open(model.lastChanged!, "history")} type="button"><History size={15} /> History</button>
          </div>
        ) : null}
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
                <MemoryActions memory={model.lastCreated} onOpen={open} />
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
            <form className="memory-search" onSubmit={(event) => { event.preventDefault(); if (lookupID.trim()) setSelection({ memory: { id: lookupID.trim(), content: "" }, mode: "history" }); }}>
              <input aria-label="Memory ID" placeholder="Memory ID" value={lookupID} onChange={(event) => setLookupID(event.target.value)} />
              <button className="secondary-action" type="submit" disabled={!lookupID.trim()}><History size={15} /> History</button>
            </form>
            <MemoryResults hasSearched={model.hasSearched} items={model.results} onOpen={open} />
          </section>
        </div>
      </div>
      {selection ? <MemoryMutationDialog key={`${selection.memory.id}:${selection.mode}`} memory={selection.memory} initialMode={selection.mode} onClose={() => setSelection(null)} onUpdated={model.applyMemoryChange} /> : null}
    </section>
  );
}

function MemoryResults({ hasSearched, items, onOpen }: { hasSearched: boolean; items: RetrievedMemory[]; onOpen: (memory: MemoryInfo, mode: MemoryDialogMode) => void }) {
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
              <MemoryActions memory={item.memory} onOpen={onOpen} />
            </div>
            <p>{item.memory.content}</p>
            <div className="memory-result-meta">
              <span>Version {item.memory.version}</span>
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

function MemoryActions({ memory, onOpen }: { memory: MemoryInfo; onOpen: (memory: MemoryInfo, mode: MemoryDialogMode) => void }) {
  return <div className="memory-item-actions">
    <button type="button" title="Correct memory" aria-label="Correct memory" onClick={() => onOpen(memory, "replace")}><Pencil size={15} /></button>
    <button type="button" title="Delete memory" aria-label="Delete memory" onClick={() => onOpen(memory, "delete")}><Trash2 size={15} /></button>
    <button type="button" title="Memory history" aria-label="Memory history" onClick={() => onOpen(memory, "history")}><History size={15} /></button>
  </div>;
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
