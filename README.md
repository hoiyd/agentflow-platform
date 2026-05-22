# AgentFlow Platform

Day 1 MVP for a full-stack AI Agent Workflow Platform.

This version includes:

- ChatGPT-like web UI
- Go backend API
- OpenAI Chat Completions streaming
- Server-Sent Events from backend to browser
- Persistent local conversations and messages
- Smoke-test fallback when `OPENAI_API_KEY` is not configured

## Tech Stack

- Frontend: Next.js, React, TypeScript
- Backend: Go standard library
- Storage: local JSON file store for Day 1
- AI: OpenAI API

## Project Structure

```txt
agentflow-platform/
  apps/
    api/      # Go backend
    web/      # Next.js frontend
  packages/
    shared/   # shared contracts placeholder
```

## Getting Started

### 1. Backend

```bash
cd apps/api
cp .env.example .env
gvm use go1.25.5
GOCACHE="$PWD/../../.cache/go-build" go run ./cmd/server
```

The API will run at `http://localhost:8080`.

If `OPENAI_API_KEY` is empty, the backend streams a deterministic local response so the app can still be verified.
Use `OPENAI_REQUEST_TIMEOUT` to tune the OpenAI-compatible request header
timeout, for example `OPENAI_REQUEST_TIMEOUT=5m`. Response bodies are not cut
off by a fixed client-wide timeout, which keeps long completions from failing
mid-read.

### 2. Frontend

```bash
cd apps/web
npm install
npm run dev
```

The web app will run at `http://localhost:3000`.
`npm run dev` runs Next.js in development mode with file watching enabled, so
frontend edits are visible after refreshing the page without restarting the
process. Use `npm run start` only for a production build.

### 3. Verify

Open `http://localhost:3000`, send a message, and confirm:

- the assistant response streams token by token
- a conversation appears in the left sidebar
- refresh keeps the conversation history
- `apps/api/.data/agentflow.json` is created

## API

```txt
GET  /health
GET  /api/conversations
POST /api/conversations
GET  /api/conversations/{id}/messages
GET  /api/agents
POST /api/agents
GET  /api/agents/{id}
GET  /api/runs
GET  /api/runs/{id}
GET  /api/runs/{id}/collaboration_steps
POST /api/runs/{id}/continue
POST /api/chat
```

`POST /api/chat` returns `text/event-stream`. It accepts an optional
`agent_id`; if omitted, the backend uses the default Narrative Strategist. Each chat
request creates a Run and streams run metadata before text deltas.
Set `mode` to `multi_agent` to run the fixed Planner -> Worker -> Reviewer ->
Finalizer orchestrator. In that mode, `agent_id` selects the Worker persona.
The Planner step pauses with `waiting_for_user`; edit the plan in the UI or call
`POST /api/runs/{id}/continue` with the approved plan to continue.

```bash
curl -s http://localhost:8080/api/agents

curl -N http://localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  --data '{"agent_id":"agent_coding","message":"Say hello from the coding agent"}'

curl -N http://localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  --data '{"mode":"multi_agent","agent_id":"agent_coding","message":"Draft a launch checklist"}'

curl -N http://localhost:8080/api/runs/run_123/continue \
  -H 'Content-Type: application/json' \
  --data '{"plan":"1. Confirm scope\\n2. Draft checklist\\n3. Review and finalize"}'

curl -s http://localhost:8080/api/runs
```

## Tool Configuration

The backend loads enabled tools from `TOOL_CONFIG_PATH`, defaulting to `.data/tools.json`.
If the file is missing, all built-in tools are enabled.

```json
{
  "enabled_tools": [
    "calculator",
    "smartapis__smartagent_discovery_capabilities",
    "smartapis__smartagent_catalog_list_plans",
    "smartapis__smartagent_places_search"
  ],
  "mcp_servers": [
    {
      "id": "smartapis",
      "enabled": true,
      "transport": "streamable-http",
      "url": "https://smartapis.net/mcp"
    }
  ]
}
```

MCP tools are registered as `<server_id>__<tool_name>` to avoid collisions with
built-in tools. The example above uses the official
SmartAPIs.net remote MCP endpoint through Streamable HTTP. Stdio MCP servers are
still supported with `transport: "stdio"`, `command`, and `args`.

## Day 2 Direction

Next step: expose tool calls in the UI as inspectable execution events.
