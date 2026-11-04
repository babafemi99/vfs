.PHONY: run test vet check db e2e e2e-dropped sim web

# Local Postgres, no password (see DECISIONS.md).
DATABASE_URL ?= postgres://localhost:5432/tradeapp?sslmode=disable
TEST_DATABASE_URL ?= postgres://localhost:5432/tradeapp_test?sslmode=disable

# Three terminals: make sim, make run, make web → http://localhost:5173
run:
	DATABASE_URL=$(DATABASE_URL) go run ./cmd/api

# brokersim on :8090 (provider console at http://localhost:8090/dev). A fresh
# seed each start, because its ids are seeded and it forgets everything on exit.
sim:
	cd ../brokersim && SIM_SEED=$$RANDOM$$RANDOM go run ./cmd/brokersim

# The Svelte console on :5173 (proxies /api to :8091).
web:
	cd web && npm install --no-audit --no-fund && npm run dev

vet:
	go vet ./...

test:
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -race -count=1 ./...

check: vet test

# Create the two local databases (safe to re-run).
db:
	-createdb -h localhost tradeapp
	-createdb -h localhost tradeapp_test

# End to end against the real brokersim (builds both; own database and ports).
e2e:
	scripts/e2e.sh

# The same, with every webhook lost: the sweeper must recover all of it.
e2e-dropped:
	DROP_WEBHOOKS=1 scripts/e2e.sh
