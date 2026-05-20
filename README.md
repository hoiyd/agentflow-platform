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

### 2. Frontend

```bash
cd apps/web
npm install
npm run dev
```

The web app will run at `http://localhost:3000`.

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
POST /api/chat
```

`POST /api/chat` returns `text/event-stream`.

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
