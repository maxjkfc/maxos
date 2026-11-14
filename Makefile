SQLC_IMAGE := sqlc/sqlc:1.31.1
# sqlc writes each context's generated code into a directory named dbgen.
SQLC_GENERATED := ':(glob)app/**/dbgen/**'
# The URLs carry no password: a password containing "/", "@" or "%" would break
# URL parsing. pgx reads it from PGPASSWORD, which LOAD_ENV sets.
TEST_DB_URL := postgres://maxos@127.0.0.1:55432/maxos_test?sslmode=disable
DEV_DB_URL := postgres://maxos@127.0.0.1:55432/maxos?sslmode=disable

# Load .env (if present) for every recipe and hand its POSTGRES_PASSWORD to
# libpq-style clients (pgx, psql) as PGPASSWORD.
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
LOAD_ENV := set -a; [ -f .env ] && . ./.env; set +a; export PGPASSWORD="$$POSTGRES_PASSWORD";

.PHONY: up down migrate sqlc sqlc-check test-db check tunnel-up

up:
	docker compose up -d --wait postgres

down:
	docker compose --profile tunnel down

migrate:
	$(LOAD_ENV) MAXOS_DATABASE_URL="$(DEV_DB_URL)" go run . migrate

sqlc:
	docker run --rm -v "$(CURDIR):/src" -w /src $(SQLC_IMAGE) generate

# Regenerates, then fails if the generated code differs from what git holds
# (stage or commit regenerated files first) or if any of it is untracked.
sqlc-check: sqlc
	git diff --exit-code -- $(SQLC_GENERATED)
	@untracked="$$(git ls-files --others --exclude-standard -- $(SQLC_GENERATED))"; \
	if [ -n "$$untracked" ]; then \
		echo "untracked sqlc output (git add it):" >&2; echo "$$untracked" >&2; exit 1; \
	fi

test-db: up
	$(LOAD_ENV) docker compose exec -T postgres psql -U maxos -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname = 'maxos_test'" | grep -q 1 \
		|| docker compose exec -T postgres psql -U maxos -d postgres -c "CREATE DATABASE maxos_test"

check: test-db
	go vet ./...
	golangci-lint run
	$(LOAD_ENV) MAXOS_TEST_DATABASE_URL="$(TEST_DB_URL)" go test ./...
	$(MAKE) sqlc-check

tunnel-up:
	docker compose --profile tunnel up -d --build
