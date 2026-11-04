# The whole lab from the repo root. Run `make` for the list.

.DEFAULT_GOAL := help
.PHONY: help up down test e2e e2e-dropped check db-reset docker docker-obs docker-down docker-clean

help: ## Show the targets
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  \033[1mmake %-13s\033[0m %s\n", $$1, $$2}'

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

# ---------------------------------------------------------------- Docker

docker: ## Run the app in containers (Postgres, brokersim, API, web on :8088). SIM_AUTO=true: self-filling
	docker compose up --build -d --wait
	@echo ""
	@echo "  Trade console     http://localhost:$${WEB_PORT:-8088}"
	@echo "  Provider console  http://localhost:$${WEB_PORT:-8088}/#/provider"
	@echo "  Trade API         http://localhost:$${API_PORT:-8091}   (metrics: /metrics)"
	@echo "  Logs              docker compose logs -f api"

docker-obs: ## The same plus observability: Vector, VictoriaLogs, VictoriaMetrics, vmalert
	docker compose --profile observability up --build -d --wait
	@echo ""
	@echo "  Trade console     http://localhost:$${WEB_PORT:-8088}"
	@echo "  Provider console  http://localhost:$${WEB_PORT:-8088}/#/provider"
	@echo "  Logs (VictoriaLogs UI)  http://localhost:$${VLOGS_PORT:-9428}/select/vmui"
	@echo "  Alerts (vmalert)  http://localhost:$${VMALERT_PORT:-8880}"
	@echo "  Metrics (VictoriaMetrics UI)  http://localhost:$${VM_PORT:-8428}/vmui"

docker-down: ## Stop the containers (data is kept)
	docker compose --profile observability down

docker-clean: ## Stop the containers and delete their data
	docker compose --profile observability down -v
