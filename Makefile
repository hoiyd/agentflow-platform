.PHONY: help setup quickstart dev test golden-eval

help:
	@printf '%s\n' \
	  'make setup       Install locked frontend dependencies and download Go modules' \
	  'make quickstart  Run setup, then start API and web workbench' \
	  'make dev         Start API and web workbench without reinstalling dependencies' \
	  'make golden-eval Run the isolated offline RAG regression gate' \
	  'make test        Run backend tests, frontend lint/tests, and production build'

setup:
	@bash scripts/setup.sh

quickstart: setup
	@bash scripts/dev.sh

dev:
	@bash scripts/dev.sh

golden-eval:
	@bash -c 'source scripts/go-env.sh && activate_agentflow_go && cd apps/api && go run ./cmd/eval rag --enforce'

test:
	@bash scripts/test.sh
