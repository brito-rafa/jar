SHELL := /bin/sh
.DEFAULT_GOAL := help

-include .env
GO ?= go
DOCS ?=
DB_URL ?=
DB_TOKEN ?=
DB_DISABLE_SSL ?= false
CHAT_URL ?=
EMBEDDING_URL ?=
CHAT_MODEL ?=
EMBEDDING_API_MODEL ?=
# This canonical identity is stored in the index; it is separate from an API alias.
EMBEDDING_MODEL ?= bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0
DIMENSIONS ?= 768
CHAT_PORT ?= 18080
CHAT_HOST ?= 127.0.0.1
CHAT_ALLOW_ANY_HOST ?= false
CHAT_PUBLIC_URL ?=
OPEN_BROWSER ?= true
FORCE_INDEX ?= false
DOCUMENT_WORKERS ?= 20
EMBEDDING_WORKERS ?= 1
EMBEDDING_BATCH_SIZE ?= 8
EMBEDDING_BUFFER_BATCHES ?= 16
INSERT_BATCH_SIZE ?= 128

export DOCS DB_URL DB_TOKEN DB_DISABLE_SSL CHAT_URL EMBEDDING_URL CHAT_MODEL EMBEDDING_API_MODEL
export EMBEDDING_MODEL DIMENSIONS CHAT_PORT CHAT_HOST CHAT_ALLOW_ANY_HOST CHAT_PUBLIC_URL OPEN_BROWSER
export FORCE_INDEX DOCUMENT_WORKERS EMBEDDING_WORKERS EMBEDDING_BATCH_SIZE EMBEDDING_BUFFER_BATCHES INSERT_BATCH_SIZE

##@ Application
.PHONY: index metadata status chat chat-web chat-terminal run
index: ## Import DOCS using the configured embedding API and DuckDB service
	@/bin/sh "$(CURDIR)/hack/check-docs.sh"
	@test -n "$$DB_URL" && test -n "$$DB_TOKEN" && test -n "$$EMBEDDING_URL" || { echo 'ERROR: Set DB_URL, DB_TOKEN and EMBEDDING_URL in .env.' >&2; exit 2; }
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh" embedding db
	$(GO) run ./cmd/jar-index -docs "$$DOCS" -db-url "$$DB_URL" \
		-embedding-url "$$EMBEDDING_URL" -embedding-api-model "$$EMBEDDING_API_MODEL" \
		-embedding-model "$$EMBEDDING_MODEL" -dimensions "$$DIMENSIONS" \
		-force="$$FORCE_INDEX" -document-workers "$$DOCUMENT_WORKERS" -embedding-workers "$$EMBEDDING_WORKERS" \
		-embedding-batch-size "$$EMBEDDING_BATCH_SIZE" -embedding-buffer-batches "$$EMBEDDING_BUFFER_BATCHES" -insert-batch-size "$$INSERT_BATCH_SIZE"

metadata: ## Build filename metadata from an existing index without inference
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -metadata

status: ## Show the connected DuckDB index status without inference
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -status

chat: chat-web ## Alias for browser chat

chat-web: ## Start browser chat (CHAT_PORT=18080)
	@test -n "$$DB_URL" && test -n "$$DB_TOKEN" && test -n "$$CHAT_URL" && test -n "$$EMBEDDING_URL" || { echo 'ERROR: Set DB_URL, DB_TOKEN, CHAT_URL and EMBEDDING_URL in .env.' >&2; exit 2; }
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-url "$$CHAT_URL" -embedding-url "$$EMBEDDING_URL" \
		-chat-model "$$CHAT_MODEL" -embedding-api-model "$$EMBEDDING_API_MODEL" \
		-web -host "$$CHAT_HOST" -allow-any-host="$$CHAT_ALLOW_ANY_HOST" -public-url "$$CHAT_PUBLIC_URL" -port "$$CHAT_PORT" -open-browser="$$OPEN_BROWSER"

chat-terminal: ## Chat in the terminal with slash commands and Tab completion
	@test -n "$$DB_URL" && test -n "$$DB_TOKEN" && test -n "$$CHAT_URL" && test -n "$$EMBEDDING_URL" || { echo 'ERROR: Set DB_URL, DB_TOKEN, CHAT_URL and EMBEDDING_URL in .env.' >&2; exit 2; }
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-url "$$CHAT_URL" -embedding-url "$$EMBEDDING_URL" \
		-chat-model "$$CHAT_MODEL" -embedding-api-model "$$EMBEDDING_API_MODEL"

run: chat ## Alias for chat (run make index first)

##@ Services
.PHONY: check-connectivity db-reset
check-connectivity: ## Check configured model APIs and DuckDB TCP connectivity
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"

db-reset: ## ERASE connected JAR index tables; requires CONFIRM=erase
	@test "$(CONFIRM)" = erase || { echo 'This erases the connected JAR index. Run: make db-reset CONFIRM=erase' >&2; exit 2; }
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -reset -confirm=erase

##@ Development
.PHONY: build cli-help test vet clean
build: ## Build bin/jar-index and bin/jar-chat
	mkdir -p bin
	$(GO) build -o bin/jar-index ./cmd/jar-index
	$(GO) build -o bin/jar-chat ./cmd/jar-chat

cli-help: ## Show index and chat command-line options
	$(GO) run ./cmd/jar-index -help
	$(GO) run ./cmd/jar-chat -help

test: ## Run unit tests; integration tests remain opt-in
	$(GO) test ./...

vet: ## Run Go static analysis
	$(GO) vet ./...

clean: ## Remove compiled commands
	rm -rf bin

##@ Help
.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <target>\n"} /^[a-zA-Z0-9_-]+:.*##/ { printf "  %-20s %s\n", $$1, $$2 } /^##@/ { printf "\n%s\n", substr($$0, 5) }' Makefile
	@printf '%s\n' '' 'Start: copy .env.example to .env and configure the service endpoints and DB_TOKEN.' \
	  '  make index DOCS="/path/one;/path/two"' \
	  '  make chat-web' \
	  '  make chat-terminal' \
	  '  make status' '' \
	  'Services: DB_URL=quack:host:9494, DB_TOKEN, DB_DISABLE_SSL=false,' \
	  '          CHAT_URL, EMBEDDING_URL, CHAT_MODEL, EMBEDDING_API_MODEL.' \
	  'Empty API model IDs discover the sole advertised model.' \
	  'Keep EMBEDDING_MODEL and DIMENSIONS consistent with the existing index.' \
	  'Index tuning: FORCE_INDEX=false, DOCUMENT_WORKERS=20, EMBEDDING_WORKERS=1,' \
	  '              EMBEDDING_BATCH_SIZE=8, EMBEDDING_BUFFER_BATCHES=16, INSERT_BATCH_SIZE=128.' \
	  'Browser: CHAT_HOST=127.0.0.1, CHAT_PORT=18080, OPEN_BROWSER=true,' \
	  '         CHAT_ALLOW_ANY_HOST=false, CHAT_PUBLIC_URL=.' \
	  'Kronk inference and DuckDB Compose: https://github.com/brito-rafa/local-jar' ''
