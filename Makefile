# Everyday commands. Run `make` to list them.
# `make check` runs the same checks as CI — run it before you push.

.DEFAULT_GOAL := help
SHELL := /bin/bash

GOLANGCI_LINT_VERSION := v2.13.2
AIR_VERSION           := v1.67.4

# Local database from compose.yaml. Override on the command line if yours differs.
DATABASE_URL      ?= postgres://barbershop:barbershop@localhost:5432/barbershop?sslmode=disable
TEST_DATABASE_URL ?= postgres://barbershop:barbershop@localhost:5432/barbershop_test?sslmode=disable
export DATABASE_URL
export LOG_FORMAT ?= text

GOBIN := $(shell go env GOPATH)/bin

.PHONY: help
help: ## List the commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "} {printf "  \033[32m%-14s\033[0m %s\n", $$1, $$2}'

## ---- setup ---------------------------------------------------------------

.PHONY: tools
tools: ## Install pinned dev tools (golangci-lint, air)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install github.com/air-verse/air@$(AIR_VERSION)

## ---- database ------------------------------------------------------------

.PHONY: db-up
db-up: ## Start PostgreSQL + PostGIS in Docker and wait until it is healthy
	docker compose up -d --wait db

.PHONY: db-down
db-down: ## Stop the database (data is kept)
	docker compose down

.PHONY: db-reset
db-reset: ## Delete the database volume and start fresh
	docker compose down -v
	$(MAKE) db-up

.PHONY: migrate
migrate: ## Apply pending migrations
	go run ./cmd/server migrate

.PHONY: migration
migration: ## Create the next migration file: make migration name=iam_users
	@test -n "$(name)" || { echo "usage: make migration name=<module>_<what>"; exit 1; }
	@last=$$(ls migrations/*.sql | sed -E 's#migrations/0*([0-9]+)_.*#\1#' | sort -n | tail -1); \
	file=migrations/$$(printf '%05d' $$((last + 1)))_$(name).sql; \
	printf -- '-- +goose Up\n\n-- +goose Down\n' > $$file; \
	echo "created $$file"

## ---- run -----------------------------------------------------------------

.PHONY: run
run: ## Run the API once
	go run ./cmd/server api

.PHONY: dev
dev: ## Run the API with live reload (air)
	$(GOBIN)/air

## ---- quality -------------------------------------------------------------

.PHONY: fmt
fmt: ## Format the code (gofumpt + goimports)
	$(GOBIN)/golangci-lint fmt ./...

.PHONY: lint
lint: ## Lint, including architecture boundary rules
	$(GOBIN)/golangci-lint run ./...

.PHONY: test
test: ## Unit tests with the race detector (database tests are skipped)
	go test -race -count=1 ./...

.PHONY: test-all
test-all: ## All tests, including database tests (needs `make db-up`)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: build
build: ## Build a static binary into bin/server
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

.PHONY: check
check: ## Everything CI checks: tidy, lint, all tests, build
	go mod tidy -diff
	$(MAKE) lint test-all build

.PHONY: clean
clean: ## Remove build and test output
	rm -rf bin tmp coverage.out
