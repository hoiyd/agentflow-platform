.PHONY: help setup quickstart dev test

help:
	@printf '%s\n' \
	  'make setup       Install locked frontend dependencies and download Go modules' \
	  'make quickstart  Run setup, then start API and web workbench' \
	  'make dev         Start API and web workbench without reinstalling dependencies' \
	  'make test        Run backend tests, frontend lint/tests, and production build'

setup:
	@bash scripts/setup.sh

quickstart: setup
	@bash scripts/dev.sh

dev:
	@bash scripts/dev.sh

test:
	@bash scripts/test.sh
