# The whole lab from the repo root. Run `make` for the list.

.DEFAULT_GOAL := help
.PHONY: help up down test e2e e2e-dropped check db-reset

help: ## Show the targets
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  \033[1mmake %-12s\033[0m %s\n", $$1, $$2}'

up: ## Start everything, broker in manual mode (AUTO=1: self-filling; KEEP_DATA=1: keep data). Ctrl-C stops all
	@scripts/up.sh

down: ## Stop services started by `make up` that are still running
	@scripts/down.sh

test: ## All tests: brokersim and tradeapp (race detector), plus the web build
	cd brokersim && go vet ./... && go test -race -count=1 ./...
	cd tradeapp && go vet ./... && go test -race -count=1 ./...
	cd web && ([ -d node_modules ] || npm install --no-audit --no-fund --silent) && npm run build --silent

e2e: ## End to end against the real brokersim (own ports and database)
	$(MAKE) -C tradeapp e2e

e2e-dropped: ## The same, with every webhook lost: the sweeper must recover
	$(MAKE) -C tradeapp e2e-dropped

check: test e2e ## Everything: unit, integration and end-to-end tests

db-reset: ## Recreate the tradeapp database (empty)
	dropdb -h localhost --if-exists tradeapp && createdb -h localhost tradeapp
