SQLC_IMAGE := sqlc/sqlc:1.31.1
TEST_DB_URL := postgres://maxos:$${POSTGRES_PASSWORD}@127.0.0.1:55432/maxos_test?sslmode=disable
DEV_DB_URL := postgres://maxos:$${POSTGRES_PASSWORD}@127.0.0.1:55432/maxos?sslmode=disable

# Load .env (if present) for every recipe.
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
LOAD_ENV := set -a; [ -f .env ] && . ./.env; set +a;

.PHONY: up down migrate sqlc test-db check tunnel-up

up:
	docker compose up -d --wait postgres

down:
	docker compose --profile tunnel down

migrate:
	$(LOAD_ENV) MAXOS_DATABASE_URL="$(DEV_DB_URL)" go run ./cmd/maxos migrate

sqlc:
	docker run --rm -v "$(CURDIR):/src" -w /src $(SQLC_IMAGE) generate

test-db: up
	$(LOAD_ENV) docker compose exec -T postgres psql -U maxos -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname = 'maxos_test'" | grep -q 1 \
		|| docker compose exec -T postgres psql -U maxos -d postgres -c "CREATE DATABASE maxos_test"

check: test-db
	go vet ./...
	golangci-lint run
	$(LOAD_ENV) MAXOS_TEST_DATABASE_URL="$(TEST_DB_URL)" go test ./...

tunnel-up:
	docker compose --profile tunnel up -d --build
