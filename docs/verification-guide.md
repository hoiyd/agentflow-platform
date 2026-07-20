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

Completion Gate flow:

1. Send `POST /api/chat` with the JSON Schema contract from [Completion Verification](completion-verification.md).
2. Return output that does not match the schema and confirm the Run does not become `completed`.
3. Inspect `GET /api/runs/{id}/replay` and confirm failed Evidence includes contract/verifier versions, Subject Hash, Snapshot Hash, summary, and an Artifact ID.
4. For an HTTP verifier with remaining attempt budget, restore the target service and call `POST /api/runs/{id}/verify`.
5. Confirm fresh passing Evidence is appended and `run.completed` appears only after `verification.passed`.
6. Change the candidate subject in a resumed Run and confirm a `verification.stale` marker references the superseded Evidence.

Run Budget flow:

1. Set `RUN_MAX_MODEL_CALLS=1`, start the backend, and create a Multi-Agent or Autonomous Run that requires more than one model call.
2. Confirm the Run stops with a typed budget error and Replay contains `budget.exceeded`.
3. Call `GET /api/runs/{id}/usage` and confirm the ledger has one model reservation/settlement operation rather than one entry per provider retry attempt.
4. Pause an Autonomous Run at `waiting_for_user`, wait longer than `RUN_MAX_RUNTIME`, and resume it. Confirm only active execution time is charged.
5. With Postgres, run `TEST_DATABASE_URL=... go test ./internal/store -run 'TestPostgresRunUsage|TestPostgresActiveRuntime'` to verify atomic reservation and active-runtime round trips.
