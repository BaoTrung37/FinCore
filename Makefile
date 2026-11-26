.PHONY: help setup env deps tools db-up db-down db-reset db-logs dev run build sqlc test test-integration vet clean

.DEFAULT_GOAL := help

-include .env
export

TEST_DATABASE_URL ?= $(or $(DATABASE_URL),postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable)
BINARY_PATH := bin/api

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

setup: env deps tools db-up ## First-time setup on a new machine (env, deps, sqlc, postgres)
	@echo "Setup complete. Run 'make run' to start the API server."

env: ## Create .env from .env.example if missing
	@if [ ! -f .env ]; then cp .env.example .env && echo "Created .env from .env.example"; else echo ".env already exists"; fi

deps: ## Download Go module dependencies
	go mod download

tools: ## Install required dev tools (sqlc)
	@command -v sqlc >/dev/null 2>&1 || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

db-up: env ## Start PostgreSQL via Docker Compose and wait until healthy
	docker compose up -d --wait

db-down: ## Stop PostgreSQL containers
	docker compose down

db-reset: ## Reset PostgreSQL database (removes volume) and restart
	docker compose down -v
	docker compose up -d --wait

db-logs: ## Tail PostgreSQL container logs
	docker compose logs -f postgres

dev: db-up run ## Start DB (if needed) and run API server

run: ## Run the API server
	go run ./cmd/api

build: ## Build the API binary to bin/api
	go build -o $(BINARY_PATH) ./cmd/api

sqlc: ## Generate type-safe SQL code with sqlc
	sqlc generate

test: ## Run unit tests with race detector
	go test -race ./...

test-integration: db-up ## Run integration tests against PostgreSQL
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -v ./tests/integration/...

vet: ## Run go vet
	go vet ./...

clean: ## Remove build artifacts
	rm -rf bin/
