"use client";

import { useState } from "react";

import type { KnowledgeWorkbenchModel } from "./useKnowledgeWorkbench";
import { KnowledgeDocuments } from "./KnowledgeDocuments";
import { KnowledgeEvaluation } from "./KnowledgeEvaluation";
import { KnowledgeSearch } from "./KnowledgeSearch";

type KnowledgeView = "search" | "documents" | "evaluation";

const views: Array<{ id: KnowledgeView; label: string }> = [
  { id: "search", label: "Search" },
  { id: "documents", label: "Documents" },
  { id: "evaluation", label: "Evaluation" }
];

export function KnowledgePanel({ model }: { model: KnowledgeWorkbenchModel }) {
  const [view, setView] = useState<KnowledgeView>("search");

  return (
    <section className="knowledge-panel">
      <header className="knowledge-toolbar">
        <h1>Knowledge</h1>
        <nav aria-label="Knowledge sections" className="knowledge-tabs" role="tablist">
          {views.map((item) => (
            <button
              aria-controls="knowledge-workspace"
              aria-selected={view === item.id}
              className={view === item.id ? "active" : ""}
              id={`knowledge-tab-${item.id}`}
              key={item.id}
              onClick={() => setView(item.id)}
              role="tab"
              type="button"
            >
              {item.label}
              {item.id === "documents" ? <span>{model.documents.documents.length}</span> : null}
            </button>
          ))}
        </nav>
      </header>

      {model[view].error ? <div className="knowledge-error" role="alert">{model[view].error}</div> : null}

      <div aria-labelledby={`knowledge-tab-${view}`} className="knowledge-workspace" id="knowledge-workspace" role="tabpanel">
        {view === "search" ? <KnowledgeSearch model={model.search} /> : null}
        {view === "documents" ? <KnowledgeDocuments model={model.documents} /> : null}
        {view === "evaluation" ? <KnowledgeEvaluation model={model.evaluation} /> : null}
      </div>
    </section>
  );
}
