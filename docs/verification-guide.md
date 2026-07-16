# Manual Verification

Backend tests:

```bash
cd apps/api
source ~/.gvm/scripts/gvm
gvm use go1.25.5 >/dev/null
mkdir -p /private/tmp/agentflow-go-build-cache
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./...
```

Frontend build:

```bash
cd apps/web
export PATH="$HOME/.nvm/versions/node/v22.6.0/bin:$PATH"
npm run build
```

RAG manual flow:

1. Start backend and frontend.
2. Open the Knowledge page.
3. Upload a `.txt` or `.md` file with a unique phrase.
4. Confirm the document list shows chunk and embedding counts.
5. Click Details and inspect chunks/metadata.
6. Search for the unique phrase.
7. Confirm the search panel shows embedding provider/model/dimensions.
8. Confirm relevant chunks rank above unrelated content.
9. Delete the document and confirm it disappears from list/search.

Demo replay flow:

1. Add or upload a knowledge document with a unique phrase.
2. Ask a chat question that should use that phrase. In Single Agent mode, optionally switch Executor from Native to LangChainGo.
3. Open the run replay page from the active run link.
4. Confirm the Retrieved context panel shows retrieval event count, memory count, chunk count, embedding provider/model/dimensions, and executor/framework.
5. Select a `retrieval` or `llm_start` event.
6. Confirm retrieved memories and knowledge chunks are visible above the raw JSON payload.
7. For LangChainGo runs, confirm event detail or raw payload includes `executor: "langchaingo"`, `framework: "langchaingo"`, and `framework_path: "chains.LLMChain"`.
