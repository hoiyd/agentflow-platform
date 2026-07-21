.PHONY: contract-generate contract-check

GO ?= go

contract-generate:
	cd apps/api && $(GO) generate ./internal/apicontract
	cd apps/web && npm run contract:generate

contract-check: contract-generate
	git diff --exit-code -- apps/api/internal/apicontract/types.gen.go apps/web/lib/api/generated.ts
	cd apps/api/tools && $(GO) test ./...
