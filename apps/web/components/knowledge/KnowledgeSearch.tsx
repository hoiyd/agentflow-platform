import type { KnowledgeWorkbenchModel } from "./useKnowledgeWorkbench";
import { KnowledgeResultCard, ModelContextPreview, RetrievalDiagnostics } from "./KnowledgeRetrieval";

export function KnowledgeSearch({ model }: { model: KnowledgeWorkbenchModel }) {
  return (
    <section className="knowledge-search">
      <div className="knowledge-search-row">
        <input
          aria-label="Search indexed knowledge"
          value={model.query}
          onChange={(event) => model.setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void model.searchKnowledge();
            }
          }}
          placeholder="Search indexed knowledge"
        />
        <label className="threshold-input">
          <span>Min similarity</span>
          <input
            max="1"
            min="0"
            onChange={(event) => model.setMinSimilarity(event.target.value)}
            step="0.05"
            type="number"
            value={model.minSimilarity}
          />
        </label>
        <label className="threshold-input">
          <span>Context token limit</span>
          <input
            min="1"
            onChange={(event) => model.setKnowledgeContextMaxTokens(event.target.value)}
            step="100"
            type="number"
            value={model.knowledgeContextMaxTokens}
          />
        </label>
        <button
          className="send"
          disabled={model.isSearching || model.query.trim().length === 0}
          onClick={model.searchKnowledge}
          type="button"
        >
          {model.isSearching ? "Searching..." : "Search"}
        </button>
      </div>

      <RetrievalDiagnostics
        contextSelection={model.contextSelection}
        embedding={model.searchEmbedding}
        fusion={model.searchFusion}
        gate={model.searchRelevanceGate}
        hasSearched={model.hasSearched}
        reranker={model.searchReranker}
        security={model.searchSecurity}
      />

      {model.hasSearched && model.noMatchReason ? <div className="rag-no-match">{model.noMatchReason}</div> : null}

      <div className="knowledge-section-heading">
        <h2>Evidence</h2>
        {model.hasSearched ? <span>{model.results.length} matches</span> : null}
      </div>
      <div className="rag-results">
        {model.results.length === 0 ? (
          <div className="knowledge-empty">
            {model.hasSearched && model.noMatchReason
              ? "No results passed the relevance gate."
              : "Search results will appear here."}
          </div>
        ) : (
          model.results.map((result) => <KnowledgeResultCard key={result.chunk.id} result={result} />)
        )}
      </div>

      <ModelContextPreview items={model.contextItems} selection={model.contextSelection} />
    </section>
  );
}
