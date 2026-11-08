SHELL := /bin/sh
.DEFAULT_GOAL := help

-include .env
GO ?= go
DOCKER ?= docker
COMPOSE = $(DOCKER) compose --project-directory "$(CURDIR)" --env-file "$(CURDIR)/.env" -f "$(CURDIR)/config/manifests/compose/compose.yaml"
DOCS ?=
DB_PORT ?= 9494
DB_DATA_DIR ?= $(CURDIR)/data
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
EMBEDDING_WORKERS ?= 1
EMBEDDING_BATCH_SIZE ?= 8
EMBEDDING_BUFFER_BATCHES ?= 16
INSERT_BATCH_SIZE ?= 128
# Hyphenated .env keys are bridged to portable process environment names.
remote-db ?=
remote-db-disable-ssl ?= false
export JAR_REMOTE_DB_DISABLE_SSL = $(remote-db-disable-ssl)
remote-db-token ?=
remote-chat ?=
remote-embedding ?=
export JAR_REMOTE_DB = $(remote-db)
export JAR_REMOTE_DB_TOKEN = $(remote-db-token)
export JAR_REMOTE_CHAT = $(remote-chat)
export JAR_REMOTE_EMBEDDING = $(remote-embedding)
ifneq ($(strip $(remote-db)),)
DB_URL := quack:$(patsubst quack:%,%,$(remote-db))
endif

.PHONY: check-connectivity
check-connectivity: ## Check configured remote Granite, Ministral and DuckDB services
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"

remote-chat-model ?=
remote-embedding-model ?=
export REMOTE_CHAT_MODEL = $(remote-chat-model)
export REMOTE_EMBEDDING_MODEL = $(remote-embedding-model)

.PHONY: remote-config remote-index remote-chat
remote-config:
	@test -n "$$JAR_REMOTE_DB" && test -n "$$JAR_REMOTE_DB_TOKEN" && test -n "$$JAR_REMOTE_CHAT" && test -n "$$JAR_REMOTE_EMBEDDING" || { echo 'ERROR: Set remote-db, remote-db-token, remote-chat and remote-embedding in .env.' >&2; exit 2; }

remote-index: ## Check remote services, then index DOCS with remote embeddings and database
	@/bin/sh "$(CURDIR)/hack/check-docs.sh"
	@$(MAKE) --no-print-directory remote-config
	@$(MAKE) --no-print-directory check-connectivity
	$(GO) run ./cmd/jar-index -docs "$$DOCS" -db-url "$$DB_URL" \
		-remote-embedding "$$JAR_REMOTE_EMBEDDING" -remote-embedding-model "$$REMOTE_EMBEDDING_MODEL" \
		-embedding-model "$$EMBEDDING_MODEL" -dimensions "$$DIMENSIONS" \
		-force="$$FORCE_INDEX" -document-workers "$$DOCUMENT_WORKERS" -embedding-workers "$$EMBEDDING_WORKERS" \
		-embedding-batch-size "$$EMBEDDING_BATCH_SIZE" -embedding-buffer-batches "$$EMBEDDING_BUFFER_BATCHES" -insert-batch-size "$$INSERT_BATCH_SIZE"

remote-chat: ## Check remote services, then open web chat with remote chat, embeddings and database
	@$(MAKE) --no-print-directory remote-config
	@$(MAKE) --no-print-directory check-connectivity
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-model "$$CHAT_MODEL" \
		-remote-chat "$$JAR_REMOTE_CHAT" -remote-chat-model "$$REMOTE_CHAT_MODEL" \
		-remote-embedding "$$JAR_REMOTE_EMBEDDING" -remote-embedding-model "$$REMOTE_EMBEDDING_MODEL" \
		-web -host "$$CHAT_HOST" -allow-any-host="$$CHAT_ALLOW_ANY_HOST" -public-url "$$CHAT_PUBLIC_URL" -port "$$CHAT_PORT" -open-browser="$$OPEN_BROWSER"

export DB_DATA_DIR CHAT_HOST CHAT_ALLOW_ANY_HOST CHAT_PUBLIC_URL CHAT_PORT OPEN_BROWSER FORCE_INDEX DOCUMENT_WORKERS EMBEDDING_WORKERS EMBEDDING_BATCH_SIZE EMBEDDING_BUFFER_BATCHES INSERT_BATCH_SIZE DOCS DB_URL CHAT_MODEL EMBEDDING_MODEL DIMENSIONS JAR_DB_TOKEN DB_PORT

##@ Database
.PHONY: local-db-check local-db-init local-db-up local-db-start local-db-down local-db-logs local-db-reset
.PHONY: remote-db-reset
remote-db-reset: ## ERASE remote JAR index tables; requires CONFIRM=erase
	@test "$(CONFIRM)" = erase || { echo 'This erases the remote JAR index. Run: make remote-db-reset CONFIRM=erase' >&2; exit 2; }
	@test -n "$$JAR_REMOTE_DB" && test -n "$$JAR_REMOTE_DB_TOKEN" || { echo 'ERROR: Set remote-db and remote-db-token in .env.' >&2; exit 2; }
	$(GO) run ./cmd/jar-index -db-url "quack:$${JAR_REMOTE_DB#quack:}" -reset -confirm=erase

local-db-check: ## Create DB_DATA_DIR if missing; reject empty paths or files
	@test -n "$$(printf '%s' "$$DB_DATA_DIR" | tr -d '[:space:]')" || { echo 'ERROR: DB_DATA_DIR must be set to a directory.' >&2; exit 2; }
	@mkdir -p "$$DB_DATA_DIR" || { printf 'ERROR: Cannot create DB_DATA_DIR: %s\n' "$$DB_DATA_DIR" >&2; exit 2; }

local-db-init: local-db-check ## Create .env token; preserve settings; create DB_DATA_DIR
	@if [ -e .env ]; then echo '.env already exists; keeping it'; else \
		umask 077; set -C; { printf 'JAR_DB_TOKEN='; openssl rand -hex 32; printf 'DB_PORT=9494\n'; } > .env; \
	fi

local-db-up: local-db-init ## Start persistent DuckDB and wait for its TCP endpoint
	@$(MAKE) --no-print-directory local-db-start

local-db-start: local-db-check
	$(COMPOSE) up -d --wait --wait-timeout 90

local-db-down: ## Stop DuckDB; keep the local database file
	$(COMPOSE) down

local-db-logs: ## Follow DuckDB container logs
	$(COMPOSE) logs -f duckdb

local-db-reset: local-db-check ## ERASE local jar.db and its WAL; preserve training/backups; requires CONFIRM=erase
	@test "$(CONFIRM)" = erase || { echo 'This erases jar.db and its WAL in DB_DATA_DIR. Run: make local-db-reset CONFIRM=erase' >&2; exit 2; }
	$(COMPOSE) down
	@rm -f "$${DB_DATA_DIR:?DB_DATA_DIR is required}/jar.db" "$${DB_DATA_DIR:?DB_DATA_DIR is required}/jar.db.wal"
	@$(MAKE) --no-print-directory local-db-up

##@ Application
.PHONY: index metadata status chat chat-web chat-terminal run build cli-help
index: ## Import DOCS recursively (required); load only the embedding model
	@/bin/sh "$(CURDIR)/hack/check-docs.sh"
	$(GO) run ./cmd/jar-index -docs "$$DOCS" -db-url "$$DB_URL" \
		-embedding-model "$$EMBEDDING_MODEL" -dimensions "$$DIMENSIONS" \
		-force="$$FORCE_INDEX" -document-workers "$$DOCUMENT_WORKERS" -embedding-workers "$$EMBEDDING_WORKERS" \
		-embedding-batch-size "$$EMBEDDING_BATCH_SIZE" -embedding-buffer-batches "$$EMBEDDING_BUFFER_BATCHES" -insert-batch-size "$$INSERT_BATCH_SIZE"

metadata: ## Build filename book/date metadata on an existing index; no re-embedding
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -metadata

status: ## Show index status, local database directory and file size
	@printf 'Local database directory: %s\n' "$$DB_DATA_DIR"
	@if [ -f "$$DB_DATA_DIR/jar.db" ]; then \
		bytes=$$(wc -c < "$$DB_DATA_DIR/jar.db" | tr -d '[:space:]'); \
		awk -v bytes="$$bytes" 'BEGIN { printf "Local database file: jar.db (%s bytes; %.2f MiB)\n", bytes, bytes / 1048576 }'; \
	else printf 'Local database file: jar.db (not found)\n'; fi
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -status

chat: chat-web ## Alias for browser chat

chat-web: ## Start web chat and open a browser (CHAT_PORT=8080)
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
	  '  make local-db-up' \
	  '  make index DOCS="/path/to/documents"' \
	  '  make status' \
	  '  make chat-web' \
	  '  make chat-terminal  # optional terminal interface' '' \
	  'Remote setup (put these examples in ignored .env; endpoints have no default):' \
	  '  remote-chat=100.74.54.18:8080' \
	  '  remote-embedding=100.74.54.18:8081' \
	  '  remote-db=100.74.54.18:9494' \
	  '  remote-db-token=<your-remote-server-token>  # required; no default' \
	  '  remote-db-disable-ssl=false  # default; true for plain HTTP over a trusted encrypted network' \
	  '  remote-chat-model=          # default: discover the sole advertised model' \
	  '  remote-embedding-model=     # default: discover the sole advertised model' '' \
	  'Remote examples (requires the four endpoint/token settings above):' \
	  '  make check-connectivity' \
	  '  ./hack/check-connectivity.sh  # reads .env directly' \
	  '  make remote-index DOCS="/path/one;/path/two"' \
	  '  make remote-chat' \
	  '  make remote-chat CHAT_PORT=8082 OPEN_BROWSER=false' \
	  '  make remote-index DOCS="/path/to/documents" remote-embedding-model=granite-embedding' \
	  '  make remote-db-reset CONFIRM=erase  # erases only remote JAR index tables' \
	  '  make status  # uses remote-db when set; local file details still describe DB_DATA_DIR' \
	  '  remote-index and remote-chat run connectivity checks first and stop on failure.' \
	  '  index and chat keep local Kronk inference; remote targets use HTTP model APIs.' \
	  '  EMBEDDING_MODEL and DIMENSIONS must match the actual remote embedding model.' '' \
	  'Variables (examples explicitly set the defaults unless noted):'  \
	  '  GO               Default: go' \
	  '                   make build GO=go' \
	  '  DOCKER           Default: docker' \
	  '                   make local-db-up DOCKER=docker' \
	  '  DOCS             No default; semicolon-separated existing directories for indexing' \
	  '                   make index DOCS="/path/one;/path/two"' \
	  '                   make index DOCS="/path/to/documents"' \
	  '  DB_DATA_DIR      Default: ./data; created by database setup; contains the bind-mounted jar.db' \
	  '                   Set DB_DATA_DIR=/path/to/database in .env or make local-db-up DB_DATA_DIR=/path/to/database' \
	  '  DB_PORT          Default: 9494; publishes the local container on loopback' \
	  '                   make local-db-up DB_PORT=9494' \
	  '                   Set DB_PORT=9494 in .env to keep it across commands.' \
	  '  DB_URL           Default: quack:127.0.0.1:$(DB_PORT), derived from DB_PORT' \
	  '                   make chat DB_URL=quack:127.0.0.1:9494' \
	  '                   Remote example: make chat DB_URL=quack:my-server:9494' \
	  '  JAR_DB_TOKEN     No fixed default; make local-db-init generates it in .env' \
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
	  '  EMBEDDING_WORKERS Default: 1; make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1' \
	  '  EMBEDDING_BATCH_SIZE Default: 8; texts per embedding call' \
	  '  EMBEDDING_BUFFER_BATCHES Default: 16; completed batches queued independently of workers' \
	  '                   make remote-index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BUFFER_BATCHES=16' \
	  '  INSERT_BATCH_SIZE Default: 128; rows per database request' \
	  '  DIMENSIONS       Default: 768; values per embedding vector' \
	  '                   make index DOCS="/path/to/documents" DIMENSIONS=768' \
	  '                   Must match EMBEDDING_MODEL output, not chunk size or chat context.' \
	  '                   Chat reads embedding settings from the database.' ''
