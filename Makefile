.PHONY: help setup quickstart dev test golden-eval

help:
	@printf '%s\n' \
	  'make setup       Install locked frontend dependencies and download Go modules' \
	  'make quickstart  Run setup, then start API and web workbench' \
	  'make dev         Start API and web workbench without reinstalling dependencies' \
	  'make golden-eval Seed and run the canonical RAG Golden Dataset v1' \
	  'make test        Run backend tests, frontend lint/tests, and production build'

setup:
	@bash scripts/setup.sh

quickstart: setup
	@bash scripts/dev.sh

dev:
	@bash scripts/dev.sh

golden-eval:
	@node scripts/run-golden-dataset-v1.mjs

test:
	@bash scripts/test.sh
