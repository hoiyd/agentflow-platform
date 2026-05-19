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

## Day 2 Direction

Next step: add Tool Registry and OpenAI tool calling, then expose tool calls in the UI as inspectable execution events.
