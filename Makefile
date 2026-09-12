.PHONY: help setup quickstart dev test golden-eval context-eval routing-eval benchmark-evidence load-evidence contract-generate contract-check

help:
	@printf '%s\n' \
	  'make setup       Install locked frontend dependencies and download Go modules' \
	  'make quickstart  Run setup, then start API and web workbench' \
	  'make dev         Start API and web workbench without reinstalling dependencies' \
	  'make golden-eval Run the isolated offline RAG regression gate' \
	  'make context-eval Run the deterministic Context quality regression gate' \
	  'make routing-eval Run the deterministic Agent routing calibration/holdout gate' \
	  'make benchmark-evidence Build the offline CASE-001 benchmark evidence pack' \
	  'make load-evidence Run bounded load and soak evidence tests' \
	  'make test        Run backend tests, frontend lint/tests, and production build' \
	  'make contract-check Regenerate shared API DTOs and reject drift'

setup:
	@bash scripts/setup.sh

quickstart: setup
	@bash scripts/dev.sh

dev:
	@bash scripts/dev.sh

golden-eval:
	@bash -c 'source scripts/go-env.sh && activate_agentflow_go && cd apps/api && go run ./cmd/eval rag --enforce'

context-eval:
	@bash -c 'source scripts/go-env.sh && activate_agentflow_go && cd apps/api && go run ./cmd/eval context --enforce'

routing-eval:
	@bash -c 'source scripts/go-env.sh && activate_agentflow_go && cd apps/api && go run ./cmd/eval route --enforce'

benchmark-evidence:
	@bash scripts/benchmark-evidence.sh

load-evidence:
	@bash scripts/load-evidence.sh

test:
	@bash scripts/test.sh

contract-generate:
	cd apps/api && go generate ./internal/apicontract
	cd apps/web && npm run contract:generate

contract-check: contract-generate
	git diff --exit-code -- apps/api/internal/apicontract/types.gen.go apps/web/lib/api-contract.gen.ts
