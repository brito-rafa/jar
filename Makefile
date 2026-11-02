SHELL := /bin/sh
.DEFAULT_GOAL := help

-include .env
GO ?= go
DOCKER ?= docker
COMPOSE = $(DOCKER) compose --project-directory "$(CURDIR)" --env-file "$(CURDIR)/.env" -f "$(CURDIR)/config/manifests/compose/compose.yaml"
DOCS ?=
DB_PORT ?= 9494
CHAT_PORT ?= 8080
CHAT_HOST ?= 127.0.0.1
CHAT_ALLOW_ANY_HOST ?= false
CHAT_PUBLIC_URL ?=
OPEN_BROWSER ?= true
DB_URL ?= quack:127.0.0.1:$(DB_PORT)
CHAT_MODEL ?= mistralai/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M
EMBEDDING_MODEL ?= bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0
DIMENSIONS ?= 768
FORCE_INDEX ?= false
DOCUMENT_WORKERS ?= 20
EMBEDDING_WORKERS ?= 4
EMBEDDING_BATCH_SIZE ?= 8
INSERT_BATCH_SIZE ?= 128
export CHAT_HOST CHAT_ALLOW_ANY_HOST CHAT_PUBLIC_URL CHAT_PORT OPEN_BROWSER FORCE_INDEX DOCUMENT_WORKERS EMBEDDING_WORKERS EMBEDDING_BATCH_SIZE INSERT_BATCH_SIZE DOCS DB_URL CHAT_MODEL EMBEDDING_MODEL DIMENSIONS JAR_DB_TOKEN DB_PORT

##@ Database
.PHONY: db-init db-up db-start db-down db-logs db-reset
db-init: ## Create data directory and .env token; preserve existing settings
	@mkdir -p "$(CURDIR)/data"
	@if [ -e .env ]; then echo '.env already exists; keeping it'; else \
		umask 077; set -C; { printf 'JAR_DB_TOKEN='; openssl rand -hex 32; printf 'DB_PORT=9494\n'; } > .env; \
	fi

db-up: db-init ## Start persistent DuckDB and wait for its TCP endpoint
	@$(MAKE) --no-print-directory db-start

db-start:
	$(COMPOSE) up -d --wait --wait-timeout 90

db-down: ## Stop DuckDB; keep the database volume
	$(COMPOSE) down

db-logs: ## Follow DuckDB container logs
	$(COMPOSE) logs -f duckdb

db-reset: ## ERASE the container database volume and restart; requires CONFIRM=erase
	@test "$(CONFIRM)" = erase || { echo 'This erases the container database. Run: make db-reset CONFIRM=erase' >&2; exit 2; }
	$(COMPOSE) down --volumes
	@$(MAKE) --no-print-directory db-up

##@ Application
.PHONY: index metadata status chat chat-terminal run build cli-help
index: ## Import DOCS recursively (required); load only the embedding model
	@/bin/sh "$(CURDIR)/hack/check-docs.sh"
	$(GO) run ./cmd/jar-index -docs "$$DOCS" -db-url "$$DB_URL" \
		-embedding-model "$$EMBEDDING_MODEL" -dimensions "$$DIMENSIONS" \
		-force="$$FORCE_INDEX" -document-workers "$$DOCUMENT_WORKERS" -embedding-workers "$$EMBEDDING_WORKERS" \
		-embedding-batch-size "$$EMBEDDING_BATCH_SIZE" -insert-batch-size "$$INSERT_BATCH_SIZE"

metadata: ## Build filename book/date metadata on an existing index; no re-embedding
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -metadata

status: ## Show remote index status without reading documents or loading models
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -status

chat: ## Start local browser chat and open a browser (CHAT_PORT=8080)
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-model "$$CHAT_MODEL" -web -host "$$CHAT_HOST" -allow-any-host="$$CHAT_ALLOW_ANY_HOST" -public-url "$$CHAT_PUBLIC_URL" -port "$$CHAT_PORT" -open-browser="$$OPEN_BROWSER"

chat-terminal: ## Chat in the terminal with slash commands and Tab completion
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-model "$$CHAT_MODEL"

run: chat ## Alias for chat (run make index first)

build: ## Build bin/jar-index and bin/jar-chat
	mkdir -p bin
	$(GO) build -o bin/jar-index ./cmd/jar-index
	$(GO) build -o bin/jar-chat ./cmd/jar-chat

cli-help: ## Show index and chat command-line options
	$(GO) run ./cmd/jar-index -help
	$(GO) run ./cmd/jar-chat -help

##@ Development
.PHONY: test vet
test: ## Run tests; converter tests require their local tools
	$(GO) test ./...

vet: ## Run Go static analysis
	$(GO) vet ./...

##@ Help
.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
	@printf '%s\n' '' 'Start:' \
	  '  make db-up' \
	  '  make index DOCS="/path/to/documents"' \
	  '  make status' \
	  '  make chat' '' \
	  'Variables (examples explicitly set the defaults unless noted):' \
	  '  GO               Default: go' \
	  '                   make build GO=go' \
	  '  DOCKER           Default: docker' \
	  '                   make db-up DOCKER=docker' \
	  '  DOCS             No default; semicolon-separated existing directories for indexing' \
	  '                   make index DOCS="/path/one;/path/two"' \
	  '                   make index DOCS="/path/to/documents"' \
	  '  DB_PORT          Default: 9494; publishes the local container on loopback' \
	  '                   make db-up DB_PORT=9494' \
	  '                   Set DB_PORT=9494 in .env to keep it across commands.' \
	  '  DB_URL           Default: quack:127.0.0.1:$(DB_PORT), derived from DB_PORT' \
	  '                   make chat DB_URL=quack:127.0.0.1:9494' \
	  '                   Remote example: make chat DB_URL=quack:my-server:9494' \
	  '  JAR_DB_TOKEN     No fixed default; make db-init generates it in .env' \
	  '                   Normally loaded automatically; do not paste tokens into commands.' \
	  '                   To set manually, put JAR_DB_TOKEN=<your-token> in .env.' \
	  '                   The container and clients must use the same token.' \
	  '  CHAT_HOST        Default: 127.0.0.1; use 0.0.0.0 for all IPv4 interfaces' \
	  '  CHAT_ALLOW_ANY_HOST Default: false; true accepts any hostname without app authentication' \
	  '  CHAT_PUBLIC_URL  Default: empty; exact external origin for Tailscale/reverse proxy' \
	  '  CHAT_PORT        Default: 8080; make chat CHAT_PORT=8081' \
	  '  OPEN_BROWSER     Default: true; make chat OPEN_BROWSER=false' \
	  '  CHAT_MODEL       Default: mistralai/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M' \
	  '                   make chat CHAT_MODEL="mistralai/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M"' \
	  '  EMBEDDING_MODEL  Default: bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0' \
	  '                   make index DOCS="/path/to/documents" EMBEDDING_MODEL="bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0"' \
	  '  FORCE_INDEX      Default: false; FORCE_INDEX=true rebuilds unchanged data' \
	  '  DOCUMENT_WORKERS Default: 20 converters; make index DOCS="/path/to/documents" DOCUMENT_WORKERS=20' \
	  '  EMBEDDING_WORKERS Default: 4; make index DOCS="/path/to/documents" EMBEDDING_WORKERS=4' \
	  '  EMBEDDING_BATCH_SIZE Default: 8; texts per embedding call' \
	  '  INSERT_BATCH_SIZE Default: 128; rows per database request' \
	  '  DIMENSIONS       Default: 768; values per embedding vector' \
	  '                   make index DOCS="/path/to/documents" DIMENSIONS=768' \
	  '                   Must match EMBEDDING_MODEL output, not chunk size or chat context.' \
	  '                   Chat reads embedding settings from the database.' ''
