# JAR setup

JAR supports local inference through Kronk and remote inference through KServe URLs. Both use a separate DuckDB service. Start with [requirements](#requirements), then choose [local setup](#local-setup) or [Kubernetes setup](#kubernetes-setup). Application settings and operational commands follow both paths.

Run repository commands from the repository root unless noted otherwise. Keep credentials in the ignored `.env`; do not source it as a shell script.

## Requirements

- Go 1.27.3 or newer and a C compiler for the DuckDB Go client. On macOS, use native ARM64 Go and install Apple command-line tools with `xcode-select --install` if needed.
- Docker running with Compose support (Docker Engine on Linux or Docker Desktop on macOS).
- Disk space and an initial internet connection for dependencies, models, native libraries, the container image, and DuckDB extensions.

### Document converters on Linux

On Debian/Ubuntu, install the local converters:

```sh
sudo apt-get install poppler-utils libreoffice-writer wv
```

PDF extraction uses `pdftotext`. Word extraction uses `wvWare` for DOC when available, then headless LibreOffice for DOC/DOCX. LibreOffice alone handles both Word formats. Scanned PDFs without a text layer still need OCR before indexing; empty documents are skipped with a warning.

### Document converters on macOS

LibreOffice is not required for JAR itself. Some very old Word files, including Word 2.0 documents such as `HSM.DOC`, need it because other converters can return binary data or incorrect text.

| Input | Converter | Installation |
| --- | --- | --- |
| TXT | Go file reading | None |
| PDF | Poppler `pdftotext` | `brew install poppler` |
| DOCX and most DOC | macOS `textutil` | Included with macOS |
| Legacy DOC | `wvWare` fallback | `brew install wv` |
| DOC still unreadable after those converters | LibreOffice fallback | `brew install --cask libreoffice` |

JAR runs converters locally. LibreOffice is detected through `soffice` on PATH or its standard macOS application path, and runs headlessly with temporary output and a separate temporary profile. Source documents are unchanged. Extracted text is sent to your configured DuckDB server for storage. In remote mode, passages and prompts are also sent to your configured model endpoints.

## Local setup

### Quick start

With Go 1.27.3+, a C compiler, and Docker Desktop running (see [Requirements](#requirements)):

```bash
git clone https://github.com/brito-rafa/jar.git
cd jar
make local-db-up
make index DOCS="/absolute/path/to/your/documents"
make status
make chat-web
```

Replace `DOCS` with one or more **existing directories separated by `;`** containing your PDF, DOC, DOCX, or TXT files. For PDFs, install Poppler with `brew install poppler`; Word converter options are in [Requirements](#requirements). The first run downloads dependencies and models. `make chat` opens the browser at `http://127.0.0.1:18080`.

`make index` fails immediately if `DOCS` is missing, blank, contains an empty list entry, or any entry is nonexistent or a file rather than a directory. `DOCS` has no default; it can also be explicitly exported in your shell. Chat, help, database setup, status, and metadata commands do not require it.

### Database

`make` uses `config/manifests/compose/compose.yaml` with the repository root as the Compose project directory, preserving relative mount paths and the project name.

Make creates `DB_DATA_DIR` automatically if it is missing, including parent directories. Empty paths, existing files, and directories that cannot be created cause setup to fail. `make local-db-up` creates a private, ignored `.env` with a random token and waits for the database health check. The service publishes **127.0.0.1:9494** and bind-mounts the project’s `./data` directory to `/data`, so the database is stored at **`./data/jar.db` on your host**. Its write-ahead log is stored alongside it. `DB_DATA_DIR` can select another host directory. Change `DB_PORT` in `.env` and rerun `make local-db-up` to select a different host port.

```bash
make local-db-logs                    # Follow database logs
make local-db-down                    # Stop the service, preserving its local database file
make local-db-up                      # Start again with stored data intact
```

Do not delete `jar.db` or its WAL unless you intend to erase the index. `.env` contains the authentication token: do not commit it. Keep the same token for both the server and application commands. The default connection is plain HTTP over loopback TCP; access from another machine requires deliberate port/TLS configuration. The single-user token permits SQL access; chat only issues read queries, but it is not a separate server-enforced read-only role.

To choose another directory, set `DB_DATA_DIR=/absolute/path/to/database` in `.env`, or run:

```bash
make local-db-up DB_DATA_DIR="/absolute/path/to/database"
```

Keep the same directory for later database commands. `make local-db-check` creates it if needed without starting Docker. `local-db-init`, `local-db-up`, `local-db-start`, and `local-db-reset` run this check before proceeding. Running Compose directly requires the directory to exist.

### Local database storage

The container mounts `DB_DATA_DIR` (default `./data`) at `/data`; the active database is **`./data/jar.db`**, with any WAL alongside it. Keep using Quack while the server runs; do not open the same file from another DuckDB process. Back up or copy the database only after stopping the server cleanly.

Earlier installations used a named Docker volume. Changing the mount alone does not migrate that database. Stop the service, preserve any pre-existing local `jar.db`, and copy the stopped volume's database and WAL into `./data` before restarting. Keep the old volume until the migrated index is verified. `data/training/` and backups remain local and Git-ignored.

Integration tests must use a separate temporary `DB_DATA_DIR` and port. A different Compose project name alone does not isolate a bind-mounted database.

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

The generated file is ignored local content containing a credential. Copy its token privately into `remote-db-token` in `.env`. Preserve the generated file and token on subsequent runs rather than generating a replacement for an existing deployment.

The manifest stores `/data/lab.duckdb` on the `duckdb-data` 20 GiB PVC. Quack listens inside the pod on loopback; an nginx sidecar exposes it through `svc/duckdb:9494`. This storage is separate from local Compose's `DB_DATA_DIR/jar.db`. Keep the PVC to preserve the index, and stop the owning database process cleanly before file-level backups. The cluster needs a working storage provisioner and extension-download access.

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
remote-embedding=http://127.0.0.1:8081
remote-chat=http://127.0.0.1:8080
remote-db=127.0.0.1:9494
remote-db-token=<token-from-your-private-duckdb-manifest>
remote-db-disable-ssl=true
remote-embedding-model=granite-embedding
remote-chat-model=ministral-3-8b
```

For a client on another machine, replace loopback with the deliberately exposed server address. See [Remote connectivity](#remote-connectivity) for TLS and endpoint settings.

```bash
make check-connectivity
make remote-index DOCS="/absolute/path/to/documents"
make status
make remote-chat
```

The browser chat uses port 18080 by default, separate from the forwarded model port 8080. Document extraction happens on the client; embeddings and generation run remotely.

### Optional shared AI Gateway URL

`hack/k8s-setup/05-aigateway.sh` installs Envoy Gateway and AI Gateway, applies a gateway manifest, and forwards the resulting service on port 8080. It still refers to `ai-gateway.yaml` in the working directory. Use the installation commands in that script, then apply `config/manifests/k8s/ai-gateway/example-ai-gateway.yaml` in place of the old path and use its service-discovery/port-forward commands. Stop the direct chat forward first, or choose a different local gateway port.

The gateway routes requests by model ID to the predictors in the `default` namespace. Set both `remote-chat` and `remote-embedding` to the gateway URL, retaining the explicit model IDs above. The database keeps its own endpoint. This gateway is optional; direct predictor URLs are sufficient.

## Remote connectivity

Set the following keys in the ignored `.env` file:

```dotenv
remote-embedding=your-server.example:8081
remote-chat=your-server.example:8080
remote-db=your-server.example:9494
remote-db-token=<remote-server-token>
```

Run `make check-connectivity` or `./hack/check-connectivity.sh` to check every configured service. The script reads the repository’s `.env` regardless of the current working directory; explicit `JAR_REMOTE_*` environment values take precedence (including empty values to skip a service). Empty endpoints are skipped; failures return a nonzero exit code. Python 3 is required. Granite is checked with a small multilingual embedding request, Ministral through its model-list endpoint and a short chat completion (up to 32 tokens, with a 60-second timeout), and DuckDB through a TCP connection. The generation check reports total request time and, when the server supplies completion-token usage, tokens per second. This includes network and prompt-processing time. The TCP check does not validate the DuckDB token; `make status` validates the authenticated database connection without changing data.

For a remote DuckDB server intentionally serving plain HTTP, set `remote-db-disable-ssl=true` (default `false`). Direct CLI calls can use `--db-disable-ssl`. This sends the database token without application TLS; use it only over a trusted encrypted network. If using Tailscale, traffic between peers is encrypted by the Tailscale connection.

`remote-db` selects the remote database for index, chat, metadata, and status; its token stays separate from the local Docker token. Local file details in `make status` still describe `DB_DATA_DIR`. `make remote-index DOCS="/path/to/documents"` uses remote embeddings and the remote database. `make remote-chat` opens web chat using both remote models and the remote database. Both require all four remote settings and run `check-connectivity` first; any failed check prevents the application from starting. `make status` uses `remote-db` when configured. Ordinary `make index` and `make chat` retain local Kronk inference. Fully remote runs skip local model downloads and Metal initialization.

Optional `remote-chat-model` and `remote-embedding-model` select API model IDs. If omitted, each endpoint must advertise exactly one model via `/v1/models`. For direct CLI use, pass `--remote-chat`, `--remote-embedding`, and optionally `--remote-chat-model` / `--remote-embedding-model`; the indexer accepts only the embedding flags. An empty endpoint retains local inference. Remote failures are reported without automatic local fallback.

Indexing installs and loads the VSS extension on the connected DuckDB server before creating the index. The server needs extension-download access on first use; installing VSS on the client alone does not provision the server.

`EMBEDDING_MODEL` remains the canonical identity stored with the index; the remote API alias is separate. The remote embedding server must serve that exact model with the same preprocessing and dimensions. JAR checks vector sizes; it cannot prove that an API alias serves identical weights. Changing the embedding model requires rebuilding the index. Existing batching, deduplication, citations and reference translations apply to both providers.

Make bridges these hyphenated keys to `JAR_REMOTE_EMBEDDING`, `JAR_REMOTE_CHAT`, `JAR_REMOTE_DB`, and `JAR_REMOTE_DB_TOKEN` for child processes. Do not source `.env` as a shell script.

Loading reports embedding batch-request latency (average/maximum and failures), insert-call latency, elapsed time, written chunks per second, and result-queue wait summed across workers. Embedding latency includes network, server queueing and any token-limit recovery; insert latency includes serialization and source-metadata writes. These are client timings, not GPU kernel timings. Progress is provisional until the transaction commits; a failed load rolls back its writes. Metrics are also printed on failure.

For llama.cpp servers reporting a structured context-limit error, JAR retries embedding requests using the server tokenizer and token arrays truncated to the reported limit, retaining boundary special tokens. Original indexed text is unchanged. Other HTTP errors fail with their status and structured error type; server response text and document content are not logged. Servers without a compatible tokenizer must accept the input length or require smaller document passages.

The completed-embedding queue defaults to 16 batches, independently of worker concurrency. With batches of 8, it buffers up to 128 completed chunks while inserts run. Additional chunks may be in flight or awaiting an insert. This bounds memory without forcing a single embedding worker to pause after one queued batch. A larger buffer can reduce temporary backpressure; it cannot overcome a sustained database bottleneck.

```bash
# One remote embedding worker, 8 chunks per request, 128-chunk queue.
make remote-index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=8 EMBEDDING_BUFFER_BATCHES=16

# Compare larger requests while retaining a 128-chunk queue.
make remote-index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=16 EMBEDDING_BUFFER_BATCHES=8

# Same option is available for local inference.
make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BUFFER_BATCHES=16
```

The direct indexer flag is `--embedding-buffer-batches=16`. Set `EMBEDDING_BUFFER_BATCHES=16` in `.env` to persist it. An already unchanged index is reused; add `FORCE_INDEX=true` only when intentionally rebuilding it for a comparison.

Chat answers include client timing for query embedding, database vector search, total retrieval (including embedding and result preparation), generation across all attempts, citation/grounding validation, and total handling time. The terminal and web interface also report timing for failed queries. Values include network and server waits; total handling excludes HTTP session-lock wait, browser rendering, and response delivery. Retrieval includes its embedding/search substeps and should not be added to them. Remote answers are buffered, so time to first token is not measured.

Each remote chat completion logs a JSON `model_response_metrics` event with `usage.prompt_tokens`, `usage.completion_tokens`, `timings.prompt_ms`, and `timings.predicted_ms`, as supplied by llama.cpp. This includes each answer retry and reference-translation part. Missing values are `null`; actual zeroes are retained. Timings are server-reported milliseconds, distinct from JAR's client request durations. Logs contain response ID, model ID and numeric metrics, without prompts or generated text.
## Connection and model settings

| Make variable | Default / purpose |
| --- | --- |
| `GO`, `DOCKER` | Executable overrides; defaults to `go`, `docker` |
| `DB_URL` | `quack:127.0.0.1:9494`, adjusted to `DB_PORT` |
| `DB_PORT` | Published host port; default `9494` |
| `DB_DATA_DIR` | Host directory, default `./data`; contains `jar.db`; created by Make if missing |
| `CHAT_PORT` | Local browser chat TCP port; default `18080` |
| `OPEN_BROWSER` | Open the default browser on `make chat`; default `true` |
| `JAR_DB_TOKEN` | Token from `.env`, exported to both commands |
| `DOCS` | Semicolon-separated directories to scan recursively; indexing only |
| `DIMENSIONS` | `768`; must match the embedding model output |
| `EMBEDDING_MODEL` | `bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0` |
| `CHAT_MODEL` | `mistralai/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M` |

Mistral supplies the chat model; IBM supplies the embedding model, converted to GGUF by bartowski. No Qwen model is selected. Kronk keeps model downloads in its usual `~/.kronk` store, and automatically detects Metal support. Model licenses and converter provenance should be reviewed separately.

### Direct commands

Load the generated database token before using Go commands or binaries directly (Make reads `.env` itself):

```bash
export JAR_DB_TOKEN="$(sed -n 's/^JAR_DB_TOKEN=//p' .env)"

go run ./cmd/jar-index -docs "/path/to/documents" -db-url quack:127.0.0.1:9494
go run ./cmd/jar-index -status -db-url quack:127.0.0.1:9494
go run ./cmd/jar-chat -db-url quack:127.0.0.1:9494
```

Use `make build` for `bin/jar-index` and `bin/jar-chat`, or `make cli-help` for all flags. Old combined-command flags such as `-db` and `-docs` on chat are no longer accepted.

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

Reduce workers if memory usage is high, especially with LibreOffice. More workers do not always mean faster indexing; compare the reported stage timings on your documents. Use `FORCE_INDEX=true` to benchmark an unchanged corpus, with no chat model running at the same time.

## Reset and erase the database

To permanently erase the local database and start with an empty index:

```bash
make local-db-reset CONFIRM=erase
```

Without `CONFIRM=erase`, the command refuses to proceed. Stop indexing and chat first. This deletes only **`jar.db` and `jar.db.wal` inside `DB_DATA_DIR`**, including their stored passages, embeddings, and index metadata, then restarts DuckDB. Training files, backups, source documents, and the rest of `data/` are preserved. Run `make index DOCS="/path/to/documents"` afterwards to rebuild the index.

The token in `.env`, source documents, cached models, training files, and database backups are preserved. This target resets the local Compose service, regardless of a `DB_URL` override; it does not erase an arbitrary remote endpoint. Normal `make local-db-down` preserves data.

To erase only the JAR index on the configured remote server:

```bash
make remote-db-reset CONFIRM=erase
```

This requires `remote-db` and `remote-db-token`, connects directly to that endpoint, and transactionally drops JAR's `items`, `item_sources`, and `jar_index_metadata` tables. Unrelated tables, the server process, and local files are preserved. It does not require model endpoints or load models. Stop indexing and chat before resetting, then rebuild with `make remote-index DOCS="/path/to/documents"`.

## Index status

The [status command](setup.md#database) also displays the configured local database directory and `jar.db` size in bytes and MiB (or reports that the file is missing). These local file details describe `DB_DATA_DIR`, even when `DB_URL` points to a remote server. Status reports readiness, chunk count, embedding identity, dimensions, and completion timestamp in UTC. It does not count source documents or maintain a per-file skip history. Runtime warnings identify skipped files.

## Metadata maintenance

New indexes include this metadata. To add it to an existing index without regenerating embeddings:

```bash
make metadata
```

## Development and validation

```bash
make test
make vet
```

Converter tests skip when their tools are unavailable. Optional network integration tests require the running service and the token:

```bash
export JAR_DB_TOKEN="$(sed -n 's/^JAR_DB_TOKEN=//p' .env)"
JAR_TEST_REMOTE=1 go test ./pkg/duck -run TestRemote -v
```

Verified on this Apple Silicon Mac: sample indexing with Granite only, status without models, Spanish chat through the remote index, authentication rejection, Unicode/NUL roundtrips, transaction rollback, container restart persistence, unchanged-index reuse, empty-index chat rejection, and explicit database reset in an isolated Compose project. Unit tests, Go vet, staticcheck, and command builds pass.

For an optional local-model check of an English answer with multilingual context:

```bash
JAR_LIVE_CHAT_TEST=1 go test ./pkg/chat -run TestLiveEnglishMultilingualContext -v
```

This opt-in test loads the local chat model; ordinary tests do not.
