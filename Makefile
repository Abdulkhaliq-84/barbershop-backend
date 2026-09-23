# Everyday commands. Run `make` to list them.
# `make check` runs the same checks as CI — run it before you push.

.DEFAULT_GOAL := help
SHELL := /bin/bash

GOLANGCI_LINT_VERSION := v2.13.2
AIR_VERSION           := v1.67.4
OAPI_CODEGEN_VERSION  := v2.8.0
SQLC_VERSION          := v1.31.1

# Local database from compose.yaml. Override on the command line if yours differs.
DATABASE_URL      ?= postgres://barbershop:barbershop@localhost:5432/barbershop?sslmode=disable
TEST_DATABASE_URL ?= postgres://barbershop:barbershop@localhost:5432/barbershop_test?sslmode=disable
export DATABASE_URL
export LOG_FORMAT ?= text
# Login codes are printed to the log (SMS_PROVIDER=console). The secret below is
# for local development only; every real environment sets its own.
export OTP_SECRET   ?= local-development-otp-secret-not-for-real-use
export SMS_PROVIDER ?= console

GOBIN := $(shell go env GOPATH)/bin

# Version stamped into the binary: the nearest tag plus commits since, e.g. v0.1.0-3-gabc1234.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= barbershop-backend:local

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

## ---- code generation ---------------------------------------------------

.PHONY: generate
generate: ## Regenerate code from api/openapi.yaml (oapi-codegen) and SQL (sqlc)
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config api/oapi-codegen.yaml api/openapi.yaml
	CGO_ENABLED=1 go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

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
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/server ./cmd/server

## ---- containers ----------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build the production image locally (IMAGE=barbershop-backend:local)
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: app-up
app-up: ## Run a built or released image with Compose: make app-up BARBERSHOP_IMAGE=ghcr.io/abdulkhaliq-84/barbershop-backend:v0.1.0
	docker compose --profile app up -d

.PHONY: app-down
app-down: ## Stop the Compose app and database
	docker compose --profile app down

.PHONY: check
check: ## Everything CI checks: generated code, tidy, lint, all tests, build
	$(MAKE) generate
	go mod tidy -diff
	$(MAKE) lint test-all build

.PHONY: clean
clean: ## Remove build and test output
	rm -rf bin tmp coverage.out
