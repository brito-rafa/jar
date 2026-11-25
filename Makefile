SHELL := /bin/sh
.DEFAULT_GOAL := help

ENV_FILE ?= .env
# Manifest targets parse the env file as literal data, including quotes/dollars.
ifeq ($(strip $(MAKECMDGOALS)),)
-include $(ENV_FILE)
else ifneq ($(strip $(filter-out generate-manifests deploy,$(MAKECMDGOALS))),)
-include $(ENV_FILE)
endif
GO ?= go
DOCKER ?= docker
INDEX_IMAGE ?= jar-index
CHAT_IMAGE ?= jar-chat
MCP_IMAGE ?= jar-mcp
REGISTRY ?= ghcr.io/brito-rafa
IMAGE_TAG ?= latest
PLATFORM ?= linux/amd64
DOCKER_BUILD_FLAGS ?=
PYTHON ?= python3
KUBECTL ?= kubectl
KUSTOMIZE ?= $(KUBECTL) kustomize
# Private, Git-ignored output; templates with placeholders live in config/manifests/kustomize.
MANIFEST_DIR ?= config/manifests/generated
NAMESPACE ?= jar
K8S_INDEX_IMAGE ?=
K8S_CHAT_IMAGE ?=
K8S_MCP_IMAGE ?=
INDEX_NODE_NAME ?=
IMAGE_PULL_SECRET ?=
DOCS ?=
DB_URL ?=
DB_TOKEN ?=
DB_DISABLE_SSL ?= false
CHAT_URL ?=
EMBEDDING_URL ?=
CHAT_API_MODEL ?=
EMBEDDING_API_MODEL ?=
# This canonical identity is stored in the index; it is separate from an API alias.
EMBEDDING_MODEL ?= bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0
DIMENSIONS ?= 4096
CHAT_PORT ?= 18080
CHAT_HOST ?= 127.0.0.1
CHAT_ALLOW_ANY_HOST ?= false
CHAT_PUBLIC_URL ?=
OPEN_BROWSER ?= true
MCP_HOST ?= 127.0.0.1
MCP_PORT ?= 18090
FORCE_INDEX ?= false
DOCUMENT_WORKERS ?= 20
EMBEDDING_WORKERS ?= 1
EMBEDDING_BATCH_SIZE ?= 8
EMBEDDING_BUFFER_BATCHES ?= 16
INSERT_BATCH_SIZE ?= 128

export REGISTRY IMAGE_TAG PLATFORM NAMESPACE K8S_INDEX_IMAGE K8S_CHAT_IMAGE K8S_MCP_IMAGE INDEX_NODE_NAME IMAGE_PULL_SECRET
export DOCS DB_URL DB_TOKEN DB_DISABLE_SSL CHAT_URL EMBEDDING_URL CHAT_API_MODEL EMBEDDING_API_MODEL
export EMBEDDING_MODEL DIMENSIONS CHAT_PORT CHAT_HOST CHAT_ALLOW_ANY_HOST CHAT_PUBLIC_URL OPEN_BROWSER MCP_HOST MCP_PORT
export FORCE_INDEX DOCUMENT_WORKERS EMBEDDING_WORKERS EMBEDDING_BATCH_SIZE EMBEDDING_BUFFER_BATCHES INSERT_BATCH_SIZE

##@ Application
.PHONY: index metadata status chat chat-web chat-terminal mcp run
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
		-chat-api-model "$$CHAT_API_MODEL" -embedding-api-model "$$EMBEDDING_API_MODEL" \
		-web -host "$$CHAT_HOST" -allow-any-host="$$CHAT_ALLOW_ANY_HOST" -public-url "$$CHAT_PUBLIC_URL" -port "$$CHAT_PORT" -open-browser="$$OPEN_BROWSER"

chat-terminal: ## Chat in the terminal with slash commands and Tab completion
	@test -n "$$DB_URL" && test -n "$$DB_TOKEN" && test -n "$$CHAT_URL" && test -n "$$EMBEDDING_URL" || { echo 'ERROR: Set DB_URL, DB_TOKEN, CHAT_URL and EMBEDDING_URL in .env.' >&2; exit 2; }
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"
	$(GO) run ./cmd/jar-chat -db-url "$$DB_URL" -chat-url "$$CHAT_URL" -embedding-url "$$EMBEDDING_URL" \
		-chat-api-model "$$CHAT_API_MODEL" -embedding-api-model "$$EMBEDDING_API_MODEL"

mcp: ## Serve read-only MCP tools for agents (MCP_PORT=18090, path /mcp)
	@test -n "$$DB_URL" && test -n "$$DB_TOKEN" && test -n "$$EMBEDDING_URL" || { echo 'ERROR: Set DB_URL, DB_TOKEN and EMBEDDING_URL in .env.' >&2; exit 2; }
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh" embedding db
	$(GO) run ./cmd/jar-mcp -db-url "$$DB_URL" -embedding-url "$$EMBEDDING_URL" \
		-embedding-api-model "$$EMBEDDING_API_MODEL" -host "$$MCP_HOST" -port "$$MCP_PORT"

run: chat ## Alias for chat (run make index first)

##@ Services
.PHONY: check-connectivity db-reset
check-connectivity: ## Check configured model APIs and DuckDB TCP connectivity
	@/bin/sh "$(CURDIR)/hack/check-connectivity.sh"

db-reset: ## ERASE connected JAR index tables; requires CONFIRM=erase
	@test "$(CONFIRM)" = erase || { echo 'This erases the connected JAR index. Run: make db-reset CONFIRM=erase' >&2; exit 2; }
	$(GO) run ./cmd/jar-index -db-url "$$DB_URL" -reset -confirm=erase

##@ Container images
.PHONY: build build-index build-chat build-mcp
build: build-index build-chat build-mcp ## Build all three container images

build-index: ## Build the jar-index image with document converters
	$(DOCKER) build --platform "$(PLATFORM)" $(DOCKER_BUILD_FLAGS) -f Dockerfile.index -t "$(INDEX_IMAGE)" .

build-chat: ## Build the Distroless jar-chat image (Dockerfile target jar-chat)
	$(DOCKER) build --platform "$(PLATFORM)" $(DOCKER_BUILD_FLAGS) -f Dockerfile --target jar-chat -t "$(CHAT_IMAGE)" .

build-mcp: ## Build the Distroless jar-mcp image (Dockerfile target jar-mcp)
	$(DOCKER) build --platform "$(PLATFORM)" $(DOCKER_BUILD_FLAGS) -f Dockerfile --target jar-mcp -t "$(MCP_IMAGE)" .

.PHONY: push push-index push-chat push-mcp
push: push-index push-chat push-mcp ## Tag and push all three existing images to REGISTRY

push-index: ## Tag and push INDEX_IMAGE as REGISTRY/jar-index:IMAGE_TAG
	$(DOCKER) tag "$(INDEX_IMAGE)" "$(patsubst %/,%,$(REGISTRY))/jar-index:$(IMAGE_TAG)"
	$(DOCKER) push "$(patsubst %/,%,$(REGISTRY))/jar-index:$(IMAGE_TAG)"

push-chat: ## Tag and push CHAT_IMAGE as REGISTRY/jar-chat:IMAGE_TAG
	$(DOCKER) tag "$(CHAT_IMAGE)" "$(patsubst %/,%,$(REGISTRY))/jar-chat:$(IMAGE_TAG)"
	$(DOCKER) push "$(patsubst %/,%,$(REGISTRY))/jar-chat:$(IMAGE_TAG)"

push-mcp: ## Tag and push MCP_IMAGE as REGISTRY/jar-mcp:IMAGE_TAG
	$(DOCKER) tag "$(MCP_IMAGE)" "$(patsubst %/,%,$(REGISTRY))/jar-mcp:$(IMAGE_TAG)"
	$(DOCKER) push "$(patsubst %/,%,$(REGISTRY))/jar-mcp:$(IMAGE_TAG)"

##@ Kubernetes manifests
# Forward explicit overrides only; Make defaults/file exports must not replace
# the generator's correctly parsed .env values.
MANIFEST_SETTINGS = DB_URL DB_TOKEN DB_DISABLE_SSL CHAT_URL EMBEDDING_URL CHAT_API_MODEL EMBEDDING_API_MODEL \
	DOCS EMBEDDING_MODEL DIMENSIONS FORCE_INDEX DOCUMENT_WORKERS EMBEDDING_WORKERS EMBEDDING_BATCH_SIZE \
	EMBEDDING_BUFFER_BATCHES INSERT_BATCH_SIZE CHAT_PORT CHAT_ALLOW_ANY_HOST CHAT_PUBLIC_URL MCP_PORT \
	REGISTRY IMAGE_TAG PLATFORM NAMESPACE K8S_INDEX_IMAGE K8S_CHAT_IMAGE K8S_MCP_IMAGE INDEX_NODE_NAME IMAGE_PULL_SECRET
MANIFEST_ENV_OVERRIDES = $(foreach setting,$(MANIFEST_SETTINGS),$(if $(filter environment command line,$(origin $(setting))),$(setting)))
.PHONY: generate-manifests deploy
generate-manifests: ## Fill config/manifests/kustomize templates from ENV_FILE into MANIFEST_DIR; validate offline
	@$(PYTHON) "$(CURDIR)/hack/generate-manifests.py" --env-file "$(ENV_FILE)" --output-dir "$(MANIFEST_DIR)" --kustomize "$(KUSTOMIZE)" --env-overrides "$(strip $(MANIFEST_ENV_OVERRIDES))"

deploy: generate-manifests ## Always regenerate MANIFEST_DIR first, then apply it to the current kubectl context
	$(KUBECTL) apply -k "$(MANIFEST_DIR)"

##@ Development
.PHONY: build-binaries cli-help test test-manifests vet clean
build-binaries: ## Build native bin/jar-index, bin/jar-chat and bin/jar-mcp commands
	mkdir -p bin
	$(GO) build -o bin/jar-index ./cmd/jar-index
	$(GO) build -o bin/jar-chat ./cmd/jar-chat
	$(GO) build -o bin/jar-mcp ./cmd/jar-mcp

cli-help: ## Show index, chat and MCP command-line options
	$(GO) run ./cmd/jar-index -help
	$(GO) run ./cmd/jar-chat -help
	$(GO) run ./cmd/jar-mcp -help

test: test-manifests ## Run Go and manifest-generator tests; integration tests remain opt-in
	$(GO) test ./...

test-manifests: ## Test manifest generation without models, databases, or a cluster
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) "$(CURDIR)/hack/test-generate-manifests.py"

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
	  '  make mcp' \
	  '  make status' '' \
	  'Services: DB_URL=quack:host:9494, DB_TOKEN, DB_DISABLE_SSL=false,' \
	  '          CHAT_URL, EMBEDDING_URL, CHAT_API_MODEL, EMBEDDING_API_MODEL.' \
	  'Empty API model IDs discover the sole advertised model.' \
	  'Keep EMBEDDING_MODEL and DIMENSIONS consistent with the existing index.' \
	  'Index tuning: FORCE_INDEX=false, DOCUMENT_WORKERS=20, EMBEDDING_WORKERS=1,' \
	  '              EMBEDDING_BATCH_SIZE=8, EMBEDDING_BUFFER_BATCHES=16, INSERT_BATCH_SIZE=128.' \
	  'Browser: CHAT_HOST=127.0.0.1, CHAT_PORT=18080, OPEN_BROWSER=true,' \
	  '         CHAT_ALLOW_ANY_HOST=false, CHAT_PUBLIC_URL=.' \
	  'MCP: MCP_HOST=127.0.0.1, MCP_PORT=18090; read-only tools at http://MCP_HOST:MCP_PORT/mcp.' \
	  'Images: make build (all three), make build-index, make build-chat, make build-mcp.' \
	  '        INDEX_IMAGE=jar-index, CHAT_IMAGE=jar-chat, MCP_IMAGE=jar-mcp, DOCKER=docker.' \
	  '        PLATFORM=linux/amd64 (override with linux/arm64), DOCKER_BUILD_FLAGS=.' \
	  'Publish: make push (all three), make push-index, make push-chat, make push-mcp.' \
	  '         REGISTRY=ghcr.io/brito-rafa, IMAGE_TAG=latest; authenticate with docker login first.' \
	  'Manifests: make generate-manifests (reads ENV_FILE=.env; offline).' \
	  '           make deploy regenerates and applies to the current kubectl context.' \
	  '           Templates: config/manifests/kustomize (placeholders, committed).' \
	  '           NAMESPACE=jar, MANIFEST_DIR=config/manifests/generated (private, Git-ignored).' \
	  '           DOCS directories are read-only hostPath mounts on the selected node.' \
	  'Native commands: make build-binaries.' \
	  'Kronk inference and DuckDB Compose: https://github.com/brito-rafa/local-jar' ''
