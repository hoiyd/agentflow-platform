.PHONY: help setup quickstart dev test golden-eval golden-eval-enforce

help:
	@printf '%s\n' \
	  'make setup       Install locked frontend dependencies and download Go modules' \
	  'make quickstart  Run setup, then start API and web workbench' \
	  'make dev         Start API and web workbench without reinstalling dependencies' \
	  'make golden-eval Run the canonical RAG Golden Dataset v1 offline' \
	  'make golden-eval-enforce Fail when a gating retrieval case misses' \
	  'make test        Run backend tests, frontend lint/tests, and production build'

setup:
	@bash scripts/setup.sh

quickstart: setup
	@bash scripts/dev.sh

dev:
	@bash scripts/dev.sh

golden-eval:
	@cd apps/api && go run ./cmd/eval-rag \
	  --dataset ../../examples/knowledge/golden-dataset.v1.json \
	  --corpus-manifest ../../examples/knowledge/golden-v1/corpus-manifest.v1.json \
	  --corpus-root ../../examples/knowledge/golden-v1

golden-eval-enforce:
	@cd apps/api && go run ./cmd/eval-rag \
	  --dataset ../../examples/knowledge/golden-dataset.v1.json \
	  --corpus-manifest ../../examples/knowledge/golden-v1/corpus-manifest.v1.json \
	  --corpus-root ../../examples/knowledge/golden-v1 \
	  --enforce

test:
	@bash scripts/test.sh
