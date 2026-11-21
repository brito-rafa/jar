# JAR setup

JAR is a Go client for **OpenAI-compatible chat and embedding services** and a separate DuckDB service. Document extraction, retrieval orchestration, and the browser/terminal interface run on the client. This repository never loads inference models locally.

For Kronk/llama.cpp inference on your own machine and DuckDB Docker Compose setup, use [local-jar](https://github.com/brito-rafa/local-jar). The two repositories are independent and retain matching Go command/package layouts and compatible database indexes. Kubernetes examples remain in this repository.

Run repository commands from the repository root unless noted otherwise. Keep credentials in the ignored `.env`; do not source it as a shell script. Existing installations should read [Parameter migration](#parameter-migration).

## Requirements

- Go 1.27.3 or newer and a C compiler for the DuckDB Go client. On macOS, use native ARM64 Go and install Apple command-line tools with `xcode-select --install` if needed.
- Python 3 for the connectivity-check script. Docker, kubectl, and Helm are needed only when deploying the supplied Kubernetes examples.
- Disk space and an initial internet connection for Go dependencies. The database server needs access to DuckDB extensions; model servers need their own model files and runtime resources. The JAR client needs no GPU, Kronk, or llama.cpp installation.

### Document converters on Linux

On Debian/Ubuntu, install the local converters:

```sh
sudo apt-get install poppler-utils libreoffice-writer wv
```

PDF extraction uses `pdftotext`. Word extraction uses `wvWare` for DOC when available, then headless LibreOffice for DOC/DOCX. LibreOffice alone handles both Word formats. Scanned PDFs without a text layer still need OCR before indexing; empty documents are skipped with a warning.

### Document converters on macOS

LibreOffice is not required for JAR itself. Some very old Word files, including Word 2.0 documents, need it because other converters can return binary data or incorrect text.

| Input | Converter | Installation |
| --- | --- | --- |
| TXT | Go file reading | None |
| PDF | Poppler `pdftotext` | `brew install poppler` |
| DOCX and most DOC | macOS `textutil` | Included with macOS |
| Legacy DOC | `wvWare` fallback | `brew install wv` |
| DOC still unreadable after those converters | LibreOffice fallback | `brew install --cask libreoffice` |

JAR runs converters locally. LibreOffice is detected through `soffice` on PATH or its standard macOS application path, and runs headlessly with temporary output and a separate temporary profile. Source documents are unchanged. Extracted text is sent to your configured DuckDB server for storage. Passages, questions, and reference-translation text are also sent to your configured model endpoints.

## Endpoint configuration

### Quick start

For a new checkout, create `.env` from `.env.example` and replace the example endpoint/token settings. Preserve any existing `.env` and migrate its keys instead of overwriting it.

```dotenv
CHAT_URL=https://chat.example.com
EMBEDDING_URL=https://embedding.example.com
DB_URL=quack:duckdb.example.com:9494
DB_TOKEN=<your-server-token>
```

Both model URLs are explicit; leaving an endpoint empty does not select local inference. URLs can include `/v1`, and a bare model host/port is treated as HTTP. The client uses `/v1/chat/completions`, `/v1/embeddings`, and `/v1/models`; it does not send API-key authentication headers. Use endpoints configured for that access model.

Run these commands after the services are available:

```bash
make check-connectivity
make index DOCS="/absolute/path/to/your/documents"
# Wait for indexing to finish before starting chat.
make chat-web
```

Indexing checks the embedding API and database TCP connection before starting. Both chat targets check the chat API, embedding API, and database TCP connection; a failed check stops startup. Use `make chat-terminal` for terminal chat. Later starts only need the chat command when the saved index is already ready. `make index` fails immediately if `DOCS` is missing, blank, contains an empty list entry, or any entry is nonexistent or a file rather than a directory. It has no default. Chat, help, status, and metadata commands do not require `DOCS`.

### Database transport

`DB_URL` selects the DuckDB Quack endpoint for index, chat, metadata, status, and reset. Set `DB_TOKEN` to the token configured on that service. For a database intentionally serving plain HTTP, set `DB_DISABLE_SSL=true` (default `false`), or pass `-db-disable-ssl` directly. This sends the database token without application TLS; use it only over a trusted encrypted network. A Tailscale connection encrypts traffic between its peers.

Keep using Quack while the database server runs; do not open its database file from another DuckDB process. Back up files only after stopping the owning database process cleanly. This repository does not start, stop, relocate, or reset database files through Docker Compose; that setup lives in [local-jar](https://github.com/brito-rafa/local-jar).

### Model selection and index compatibility

`CHAT_MODEL` and `EMBEDDING_API_MODEL` select the API model IDs sent to their respective endpoints. If omitted, each endpoint must advertise exactly one model through `/v1/models`.

`EMBEDDING_MODEL` is a separate **canonical embedding identity** stored with the index and included in the corpus fingerprint. It retains the previous default, `bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0`, with `DIMENSIONS=768`. The default describes the existing Granite index configuration; it does not download or choose a remote server's weights. Set `EMBEDDING_API_MODEL` when the server uses an alias such as `granite-embedding`.

The embedding service must implement the indexed model with matching preprocessing and vector dimensions. JAR checks vector sizes but cannot prove that an API alias serves identical weights. Keep `EMBEDDING_MODEL`, dimensions, source directory labels, and embedding behavior unchanged to reuse an existing index across this split. A repository change alone requires no schema migration or reindex. A different embedding model or preprocessing requires rebuilding the index; matching vector sizes alone is insufficient. Chat reads the canonical embedding identity from the existing index.

### Check connectivity and authentication

```bash
make check-connectivity
# Or run the script directly:
./hack/check-connectivity.sh
```

The script reads the repository's `.env` regardless of the current working directory. Explicit process environment values override file settings. It checks configured embedding/chat endpoints with small synthetic requests, and database TCP reachability. A chat check generates up to 32 tokens and reports request time and throughput when token usage is available. These timings include network and prompt-processing time. Connectivity checks do not validate the database token; run `make status` to check authentication without changing data.

### Direct CLI options

Make reads `.env` for its commands. For direct Go/binary calls, export the relevant settings in the process environment or pass endpoint/model flags explicitly; the Go commands do not parse `.env` themselves.

```bash
export DB_TOKEN='<your-server-token>'

go run ./cmd/jar-index -docs "/path/to/documents" \
  -db-url quack:duckdb.example.com:9494 \
  -embedding-url https://embedding.example.com \
  -embedding-api-model granite-embedding

go run ./cmd/jar-index -status -db-url quack:duckdb.example.com:9494

go run ./cmd/jar-chat -db-url quack:duckdb.example.com:9494 \
  -chat-url https://chat.example.com -chat-model ministral-3-8b \
  -embedding-url https://embedding.example.com \
  -embedding-api-model granite-embedding
```

Use `make build` for `bin/jar-index` and `bin/jar-chat`. Run `make cli-help`, `go run ./cmd/jar-index -help`, or `go run ./cmd/jar-chat -help` for all flags. The indexer accepts only embedding model settings; status, metadata, and reset do not call model endpoints.

### Embedding input limits

For llama.cpp servers reporting a structured context-limit error, JAR retries embedding requests using the server tokenizer and token arrays truncated to the reported limit, retaining boundary special tokens. Original indexed text is unchanged. Other HTTP errors fail with their status and structured error type; server response text and document content are not logged. Servers without a compatible tokenizer must accept the input length or require smaller document passages.

Indexing installs and loads the VSS extension on the connected DuckDB server before creating the index. The server needs extension-download access on first use; installing VSS on the client alone does not provision the server.

### Parameter migration

Remote/local provider selection has been removed. Update an existing ignored `.env` to the new names; old `REMOTE_*`, `JAR_REMOTE_*`, and `remote-*` parameters are not aliases.

| Previous remote setting | Remote JAR setting |
| --- | --- |
| `REMOTE_CHAT` / `-remote-chat` | `CHAT_URL` / `-chat-url` |
| `REMOTE_EMBEDDING` / `-remote-embedding` | `EMBEDDING_URL` / `-embedding-url` |
| `REMOTE_CHAT_MODEL` / `-remote-chat-model` | `CHAT_MODEL` / `-chat-model` |
| `REMOTE_EMBEDDING_MODEL` / `-remote-embedding-model` | `EMBEDDING_API_MODEL` / `-embedding-api-model` |
| `REMOTE_DB=host:port` | `DB_URL=quack:host:port` |
| `REMOTE_DB_TOKEN` | `DB_TOKEN` |
| `REMOTE_DB_DISABLE_SSL` | `DB_DISABLE_SSL` |
| `make remote-index` | `make index` |
| `make remote-chat` | `make chat-web` |
| `make remote-db-reset` | `make db-reset` |

Keep the existing canonical `EMBEDDING_MODEL` and `DIMENSIONS` for the saved index. The previous local `CHAT_MODEL` GGUF download reference is not automatically an API model ID: use the service's actual ID or leave `CHAT_MODEL` empty for discovery. Compose settings such as `DB_PORT` and `DB_DATA_DIR` belong in `local-jar`.

This split does not move credentials, database files, document collections, training data, or cached models. Both applications can connect to an existing compatible database, but finish indexing before starting chat and run only one indexer at a time. Preserve existing database files, persistent volumes, and backups.

## Kubernetes setup

The examples in `hack/k8s-setup/` describe a single-node Linux Minikube deployment with an AMD GPU. JAR's Go commands still run on the client machine; Kubernetes hosts the model APIs and database. In addition to the client requirements above, the cluster host needs Docker, Minikube with AMD GPU support (the script specifies v1.35+), kubectl, Helm, OpenSSL, an AMD driver exposing `/dev/kfd` and `/dev/dri`, and sufficient GPU memory. The default profile requests 10 CPUs and 32 GiB RAM.

### Start Minikube and install KServe

```bash
bash hack/k8s-setup/01-minikube-start.sh
kubectl config use-context kserve
bash hack/k8s-setup/02-kserve.sh
```

`PROFILE`, `CPUS`, and `MEMORY` override the first script's defaults. If you choose another profile, select that context before later commands: the remaining scripts use the current kubectl context. The KServe script installs cert-manager and KServe in Standard mode without Istio; its initial cert-manager install assumes a fresh installation.

### Deploy the models

Choose one model manifest; both define the same model services:

```bash
kubectl apply -f config/manifests/k8s/kserve/example-kserve-models.yaml
kubectl get inferenceservices
kubectl get pods
```

This manifest contains a namespaced `ServingRuntime` and two `InferenceService` objects, using HTTPS GGUF downloads. Alternatively, `config/manifests/k8s/kserve/example-kserve-cluster-runtime.yaml` defines cluster-scoped runtime/storage resources and the same services using `hf-gguf://` downloads. Do not apply both alternatives together.

The runtime mounts host GPU devices and uses privileged containers. Its ROCm override targets an RX 6600; adapt GPU settings and resource limits for your hardware. The diagnostic pod is in `config/manifests/k8s/rocm/example-pod-to-check-rocm.yaml`. First startup downloads the model files; wait for both predictors to become ready before connecting.

### Deploy DuckDB

`hack/k8s-setup/03-duckdb.sh` illustrates token substitution but still refers to `duckdb.yaml` in the working directory and prints the token. Instead, create a private copy using the current manifest path:

```bash
umask 077
mkdir -p data/k8s
python3 - <<'PYTHON'
from pathlib import Path
import secrets
source = Path('config/manifests/k8s/duckdb/example-duckdb.yaml').read_text()
Path('data/k8s/duckdb.yaml').write_text(
    source.replace('CHANGE-ME-to-a-long-random-token', secrets.token_hex(24)))
PYTHON
kubectl apply -f data/k8s/duckdb.yaml
kubectl rollout status deployment/duckdb
```

The generated file is ignored local content containing a credential. Copy its token privately into `DB_TOKEN` in `.env`. Preserve the generated file and token on subsequent runs rather than generating a replacement for an existing deployment.

The manifest stores `/data/lab.duckdb` on the `duckdb-data` 20 GiB PVC. Quack listens inside the pod on loopback; an nginx sidecar exposes it through `svc/duckdb:9494`. The Compose service in [local-jar](https://github.com/brito-rafa/local-jar) owns its own database path and is separate from this PVC. Keep the PVC to preserve the index, and stop the owning database process cleanly before file-level backups. The cluster needs a working storage provisioner and extension-download access.

### Expose the service URLs

For a client running on the cluster host, use separate terminals and keep these forwards running:

```bash
kubectl port-forward svc/ministral-3-8b-predictor 8080:80
kubectl port-forward svc/granite-embedding-predictor 8081:80
kubectl port-forward svc/duckdb 9494:9494
```

These commands bind to loopback. `hack/k8s-setup/04-portforward.sh` instead binds model/database forwards to both loopback and the host's Tailscale IPv4 address, requires Tailscale, and also forwards the DuckDB UI on 4213. Use that script only when that network exposure is intended. The model examples do not configure API authentication.

Configure the client `.env` for the loopback forwards:

```dotenv
EMBEDDING_URL=http://127.0.0.1:8081
CHAT_URL=http://127.0.0.1:8080
DB_URL=quack:127.0.0.1:9494
DB_TOKEN=<token-from-your-private-duckdb-manifest>
DB_DISABLE_SSL=true
EMBEDDING_API_MODEL=granite-embedding
CHAT_MODEL=ministral-3-8b
```

For a client on another machine, replace loopback with the deliberately exposed server address. See [Endpoint configuration](#endpoint-configuration) for TLS and endpoint settings.

```bash
make check-connectivity
make index DOCS="/absolute/path/to/documents"
make status
make chat-web
```

The browser chat uses port 18080 by default, separate from the forwarded model port 8080. Document extraction happens on the client; embeddings and generation run remotely.

### Optional shared AI Gateway URL

`hack/k8s-setup/05-aigateway.sh` installs Envoy Gateway and AI Gateway, applies a gateway manifest, and forwards the resulting service on port 8080. It still refers to `ai-gateway.yaml` in the working directory. Use the installation commands in that script, then apply `config/manifests/k8s/ai-gateway/example-ai-gateway.yaml` in place of the old path and use its service-discovery/port-forward commands. Stop the direct chat forward first, or choose a different local gateway port.

The gateway routes requests by model ID to the predictors in the `default` namespace. Set both `CHAT_URL` and `EMBEDDING_URL` to the gateway URL, retaining the explicit model IDs above. The database keeps its own endpoint. This gateway is optional; direct predictor URLs are sufficient.

## Indexing performance and batching

### Batch size and queue capacity

The completed-embedding queue defaults to 16 batches, independently of worker concurrency. With batches of 8, it buffers up to 128 completed chunks while inserts run. Additional chunks may be in flight or awaiting an insert. This bounds memory without forcing a single embedding worker to pause after one queued batch. A larger buffer can reduce temporary backpressure; it cannot overcome a sustained database bottleneck.

```bash
# One embedding worker, 8 chunks per request, 128-chunk queue.
make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=8 EMBEDDING_BUFFER_BATCHES=16

# Compare larger requests while retaining a 128-chunk queue.
make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=16 EMBEDDING_BUFFER_BATCHES=8
```

The direct indexer flag is `--embedding-buffer-batches=16`. Set `EMBEDDING_BUFFER_BATCHES=16` in `.env` to persist it. An already unchanged index is reused; add `FORCE_INDEX=true` only when intentionally rebuilding it for a comparison.

### Indexing metrics

Loading reports embedding batch-request latency (average/maximum and failures), insert-call latency, elapsed time, written chunks per second, and result-queue wait summed across workers. Embedding latency includes network, server queueing and any token-limit recovery; insert latency includes serialization and source-metadata writes. These are client timings, not GPU kernel timings. Progress is provisional until the transaction commits; a failed load rolls back its writes. Metrics are also printed on failure.

## Chat timing and model metrics

### Client timings

Chat answers include client timing for query embedding, database vector search, total retrieval (including embedding and result preparation), generation across all attempts, citation/grounding validation, and total handling time. The terminal and web interface also report timing for failed queries. Values include network and server waits; total handling excludes HTTP session-lock wait, browser rendering, and response delivery. Retrieval includes its embedding/search substeps and should not be added to them. Remote answers are buffered, so time to first token is not measured.

### Remote server metrics

Each remote chat completion logs a JSON `model_response_metrics` event with `usage.prompt_tokens`, `usage.completion_tokens`, `timings.prompt_ms`, and `timings.predicted_ms`, as supplied by llama.cpp. This includes each answer retry and reference-translation part. Missing values are `null`; actual zeroes are retained. Timings are server-reported milliseconds, distinct from JAR's client request durations. Logs contain response ID, model ID and numeric metrics, without prompts or generated text.

## Connection and model settings

| Make variable | Default / purpose |
| --- | --- |
| `GO` | Go executable override; defaults to `go` |
| `CHAT_URL` | Required chat API URL for chat |
| `EMBEDDING_URL` | Required embedding API URL for indexing/chat |
| `DB_URL` | Required DuckDB Quack endpoint, such as `quack:db.example.com:9494` |
| `DB_TOKEN` | Authentication token configured on the database server |
| `DB_DISABLE_SSL` | `false`; use `true` only for intentional plain HTTP database transport |
| `CHAT_MODEL` | Empty; discover the sole model advertised by the chat endpoint |
| `EMBEDDING_API_MODEL` | Empty; discover the sole model advertised by the embedding endpoint |
| `EMBEDDING_MODEL` | Canonical index identity: `bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0` |
| `DIMENSIONS` | `768`; must match the indexed embedding model output |
| `DOCS` | Semicolon-separated directories to scan recursively; indexing only |
| `CHAT_PORT` | Local browser chat TCP port; default `18080` |
| `CHAT_HOST` | `127.0.0.1`; browser listener address |
| `CHAT_ALLOW_ANY_HOST` | `false`; whether to accept arbitrary request hostnames |
| `CHAT_PUBLIC_URL` | Empty; optional exact public origin for a reverse proxy |
| `OPEN_BROWSER` | `true`; open the default browser on `make chat-web` |

The included KServe examples serve IBM Granite Embedding 278M Multilingual and Mistral Ministral 3 8B Instruct GGUF models. Model/runtime downloads happen on the servers, not in this Go client. Review model licenses and conversion provenance separately.

## Browser chat

```bash
make chat-web                         # Open the browser at localhost:18080
make chat-web CHAT_PORT=8081           # Choose another port
make chat-web OPEN_BROWSER=false       # Start without opening a browser
make chat-terminal                    # Use the terminal instead
```

`make chat` and `make run` are aliases for web chat. The existing index must be ready. The interface has a sticky prompt, Tab completion, a compact live vector-search panel, progress spinner, and cited answers. Enter sends a message; Shift+Enter adds a line. Press Ctrl+C in the server terminal to stop it.

Browser tabs share one conversation, search filter, language setting, and latest references. Reloading the page does not clear server history, but does not restore the visible transcript. `/quit` ends the conversation without stopping the web server.

### Network access

The default listener is `127.0.0.1`. For a reverse proxy such as Tailscale Serve, allow its exact public origin:

```bash
make chat-web CHAT_PUBLIC_URL=https://your-machine.your-tailnet.ts.net
```

To accept any hostname on all IPv4 interfaces:

```bash
make chat-web CHAT_HOST=0.0.0.0 CHAT_ALLOW_ANY_HOST=true
```

These settings can also be saved in `.env`. JAR has **no application login**: anyone who can reach an exposed port can use the shared chat and indexed content. Browser same-origin checks remain enabled. These options do not change firewall, router, or Tailscale permissions.

## Multiple document directories

```bash
make index DOCS="/path/to/books;/path/to/conferences;/path/with spaces"
# Or set this in .env (no shell quotes needed in that file):
# DOCS=/path/to/books;/path/to/conferences;/path/with spaces
```

The same list works with `jar-index -docs "/path/one;/path/two"`. Quote the list on the command line so the shell does not interpret semicolons as commands. Whitespace around entries is trimmed; empty entries (including a trailing `;`) are rejected. A literal semicolon in a directory name is not supported. `.env` is read as Make settings; do not source a file containing an unquoted semicolon list as a shell script. For shell exports, use `export DOCS="/path/one;/path/two"`.

Every directory is validated before indexing starts. Roots are scanned recursively in deterministic order using one shared document-worker pool. Duplicate roots and overlapping file paths are read once. In multi-root imports, source labels include each root's directory name to distinguish identical relative filenames; roots with the same basename get a stable hash suffix. Single-root source labels remain unchanged. All roots feed the same deduplicated database index; include the full desired set of roots on each indexing run.

## Deduplication and concurrent indexing

JAR embeds identical passages once while keeping all their source locations. Translations and passages with different wording remain separate. Index updates are transactional: a failed import preserves the previous index.

Indexing processes documents and embedding requests concurrently, then writes results in batches. These are the defaults:

| Setting | Default | Purpose |
| --- | --- | --- |
| `DOCUMENT_WORKERS` | 20 | Documents processed at once |
| `EMBEDDING_WORKERS` | 1 | Concurrent embedding requests |
| `EMBEDDING_BATCH_SIZE` | 8 | Passages per embedding request |
| `EMBEDDING_BUFFER_BATCHES` | 16 | Completed embedding batches buffered independently of worker count |
| `INSERT_BATCH_SIZE` | 128 | Unique passages per database insert |

Override settings when running indexing:

```bash
make index DOCS="/path/to/documents" DOCUMENT_WORKERS=4 EMBEDDING_WORKERS=2
```

Reduce workers if memory usage is high, especially with LibreOffice. More workers do not always mean faster indexing; compare the reported stage timings on your documents. Use `FORCE_INDEX=true` to benchmark an unchanged corpus, while avoiding competing requests to the same model service.

## Reset the connected index

To erase only JAR's index tables on the configured database server:

```bash
make db-reset CONFIRM=erase
```

Without `CONFIRM=erase`, the command refuses to proceed. Stop indexing and chat first. This connects to `DB_URL` using `DB_TOKEN` and transactionally drops JAR's `items`, `item_sources`, and `jar_index_metadata` tables. Unrelated tables, the server process, and local files are preserved. It does not require model endpoints or load models. Run `make index DOCS="/path/to/documents"` afterwards to rebuild the index.

## Index status

```bash
make status
```

Status reports the connected endpoint, readiness, chunk count, embedding identity, vector dimensions, and completion timestamp in UTC. It does not load models, count source documents, maintain a per-file skip history, or inspect a local database file. Runtime indexing warnings identify skipped files.

## Metadata maintenance

New indexes include this metadata. To add it to an existing index without regenerating embeddings:

```bash
make metadata
```

## Development and validation

```bash
make test
make vet
make build
```

Use native ARM64 Go on Apple Silicon, for example `make test GO=/opt/homebrew/bin/go`. Run `gofmt` on changed Go files, and `staticcheck ./...` when available. Relevant concurrency changes also require `go test -race ./pkg/chat ./pkg/duck`.

Ordinary tests use synthetic documents and fake model callbacks; they do not download inference models or require running services. Converter tests skip when their tools are unavailable. Remote database integration tests remain opt-in. Inspect a test before enabling it: some create/drop corpus tables and must use a dedicated disposable server, token, and nondefault port. Do not point destructive fixtures at a real index, and clean up only resources created for the test. The local-model live test belongs only to [local-jar](https://github.com/brito-rafa/local-jar).

The destructive database fixtures select isolated endpoints with `JAR_INDEX_TEST_URL`, `JAR_METADATA_TEST_URL`, or `JAR_LOAD_TEST_URL` and read `DB_TOKEN` from the environment. For example, after provisioning a disposable authenticated database on a dedicated test port:

```bash
export DB_TOKEN='<disposable-server-token>'
JAR_INDEX_TEST_URL=quack:127.0.0.1:19494 go test ./pkg/duck -run '^TestRemoteIndexReplacesCatalogTransactionally$' -v
```

`JAR_TEST_REMOTE=1` enables older transaction/authentication tests that use `127.0.0.1:9494` directly. Inspect and isolate that endpoint before enabling them; it must not be the real index.
