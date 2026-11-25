# JAR setup

Deploy DuckDB and the KServe chat/embedding services first, configure `.env`, then run `make deploy` to install JAR's indexing Job, chat Deployment, and read-only MCP Deployment. JAR uses OpenAI-compatible HTTP model APIs and a separate DuckDB service; its application images contain no inference models or database server.

Run commands from the repository root. Keep credentials and filled manifests private and Git-ignored. For Kronk/llama.cpp inference and DuckDB Compose, use [local-jar](https://github.com/brito-rafa/local-jar). Native commands and direct container runs are collected under [Troubleshooting and development](#troubleshooting-and-development).

## 1. Install Kubernetes services

### Cluster prerequisites

You need a Kubernetes cluster and the intended current kubectl context, Helm, Make, and Python 3. Manifest validation uses `kubectl kustomize` by default; standalone Kustomize can be selected later. A working storage provisioner is required for DuckDB's PVC. Model nodes need suitable GPU drivers, device plugins, available GPU memory, and access to download runtime images and model files. The database needs extension-download access.

The supplied model pair is a hardware-specific example: Ministral requests `nvidia.com/gpu: 1` with the CUDA runtime, while Granite requests `amd.com/gpu: 1` with ROCm. Review and adapt the runtime images, GPU resources, device mounts, and memory limits to your nodes before applying. The ROCm settings include a privileged RX 6600 compatibility configuration. Kubernetes must advertise the requested GPU resources; memory limits in these manifests describe host RAM rather than VRAM.

For the default `jarchat` browser exposure, a Tailscale Kubernetes operator must handle the Service annotations; `hack/k8s-setup/04-tailscale.sh` installs one (the Tailscale tags and OAuth client it needs are listed at the top of that script). The application nodes also need access to the JAR images you will configure in `.env`. Default image names are references, not a guarantee that packages exist or are publicly accessible; use pullable published images or follow the optional [image release procedure](#container-images).

### Deploy DuckDB

Use [the DuckDB example](config/manifests/k8s/duckdb/example-duckdb.yaml) to create a private manifest containing a generated token. This guide installs the infrastructure in the `jar` namespace, which is also JAR's default `NAMESPACE`, so create it first:

```bash
kubectl create namespace jar --dry-run=client -o yaml | kubectl apply -f -
umask 077
mkdir -p data/k8s
python3 - <<'PYTHON'
from pathlib import Path
import secrets
source = Path('config/manifests/k8s/duckdb/example-duckdb.yaml')
target = Path('data/k8s/duckdb.yaml')
if not target.exists():
    target.write_text(source.read_text().replace(
        'CHANGE-ME-to-a-long-random-token', secrets.token_hex(24)))
    target.chmod(0o600)
PYTHON
kubectl -n jar apply -f data/k8s/duckdb.yaml
kubectl -n jar rollout status deployment/duckdb
```

Preserve this private file and its existing token on later runs. Configure the same token as `DB_TOKEN` in `.env` in the next step. The manifest creates a 20 GiB `duckdb-data` PVC, a single owning Deployment, and `svc/duckdb:9494`. nginx forwards plain HTTP to Quack on pod loopback; the database file is `/data/lab.duckdb` on the PVC. Keep the PVC to retain the index, and stop the owner cleanly before file-level backups. Other processes must connect through Quack rather than open that file.

`hack/k8s-setup/03-duckdb.sh` is a shortcut that applies the same example to `jar` with a fresh token and prints that token; it does not keep a private copy, so prefer the procedure above.

### Install KServe

For a fresh cluster, review and run the supplied controller installation:

```bash
bash hack/k8s-setup/02-kserve.sh
```

The script installs cert-manager `v1.21.3` and KServe CRDs/resources `v0.20.0` into their own namespaces. Its cert-manager command assumes a fresh installation; adapt it if these controllers already exist. It uses the current kubectl context. GPU drivers/device plugins and the Tailscale operator are separate prerequisites.

### Deploy the chat and embedding models

After adapting the GPU configuration, apply the cluster runtime/downloader definitions before the InferenceServices:

```bash
kubectl apply -f config/manifests/k8s/kserve/example-cluster-serving-runtimes-cuda-and-rocm.yaml
kubectl -n jar apply -f config/manifests/k8s/kserve/example-isvc-ministral-granite.yaml
kubectl -n jar wait --for=condition=Ready inferenceservice/ministral-3-8b --timeout=30m
kubectl -n jar wait --for=condition=Ready inferenceservice/granite-embedding --timeout=30m
kubectl -n jar get inferenceservices,services
```

The [cluster definitions](config/manifests/k8s/kserve/example-cluster-serving-runtimes-cuda-and-rocm.yaml) provide `hf-gguf`, `llamacpp-cuda`, and `llamacpp-rocm`. The [model definitions](config/manifests/k8s/kserve/example-isvc-ministral-granite.yaml) serve Ministral 3 8B Instruct Q4_K_M as `ministral-3-8b` and Granite Embedding 278M Multilingual Q8_0 as `granite-embedding`. Models download during startup; gated repositories can use an optional `hf-secret` with `HF_TOKEN` in the model namespace.

The predictor Services are `ministral-3-8b-predictor:80` and `granite-embedding-predictor:80` in `jar`. Wait for both models and DuckDB to be ready before deploying JAR. Application Pods must reach these Services through cluster networking; use their namespace-qualified DNS names in `.env`. A laptop's localhost port-forward is for troubleshooting and is not a Pod endpoint.

## 2. Create and configure .env

For a new checkout, create the private settings file:

```bash
umask 077
cp .env.example .env
```

Preserve an existing `.env` and update its settings instead of overwriting it. Edit the new file for your cluster. With the infrastructure examples above in `jar`, use:

```dotenv
NAMESPACE=jar
DB_URL=quack:duckdb.jar.svc.cluster.local:9494
DB_TOKEN=<same-token-as-the-private-duckdb-manifest>
DB_DISABLE_SSL=true
CHAT_URL=http://ministral-3-8b-predictor.jar.svc.cluster.local
EMBEDDING_URL=http://granite-embedding-predictor.jar.svc.cluster.local
CHAT_API_MODEL=ministral-3-8b
EMBEDDING_API_MODEL=granite-embedding
EMBEDDING_MODEL=bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0
DIMENSIONS=768
DOCS=/srv/jar/books;/srv/jar/conferences
INDEX_NODE_NAME=documents-node
REGISTRY=ghcr.io/brito-rafa
IMAGE_TAG=latest
PLATFORM=linux/amd64
CHAT_PUBLIC_URL=http://jarchat
```

Replace the token, node, document paths, and image references with your settings. `NAMESPACE=jar` places JAR's application resources in the same `jar` namespace as the infrastructure above. If the services live in another namespace, update their DNS names. The example DuckDB proxy uses plain HTTP, so it requires `DB_DISABLE_SSL=true`; use `false` for an endpoint serving TLS.

Each absolute directory in `DOCS` becomes a read-only `hostPath` of type `Directory` mounted at exactly the same path inside the indexing Pod. Those directories must already exist on the indexing node and be readable by UID `65532`. `INDEX_NODE_NAME` pins the Job to that node; otherwise every eligible indexing node needs the paths. The generator checks path syntax without checking this machine's filesystem, copying documents, creating node directories, or provisioning a PVC. Kubernetes must mount the directories before the process can start, including when indexing will later be skipped.

Select application images the cluster can pull. By default, generated references are `REGISTRY/jar-index:IMAGE_TAG`, `REGISTRY/jar-chat:IMAGE_TAG`, and `REGISTRY/jar-mcp:IMAGE_TAG`; `K8S_INDEX_IMAGE`, `K8S_CHAT_IMAGE`, and `K8S_MCP_IMAGE` override them individually. If a registry requires authentication, set `IMAGE_PULL_SECRET` to an existing pull Secret in `NAMESPACE`. The generated Pods select Linux and the architecture in `PLATFORM`, default `linux/amd64`; match it to both the images and nodes.

For the short Tailscale hostname, use `CHAT_PUBLIC_URL=http://jarchat` (also the generator's default when empty). If your browser uses the full MagicDNS hostname or a TLS proxy, set that exact origin, for example `http://jarchat.your-tailnet.ts.net` or your proxy's HTTPS origin. Keep `CHAT_ALLOW_ANY_HOST=false` to retain the default Host/Origin policy.

## 3. Deploy JAR

With services ready, document paths present on the selected node, and application images available:

```bash
make deploy
kubectl -n jar wait --for=condition=complete job/jar-index --timeout=1h
kubectl -n jar rollout status deployment/jar-chat
kubectl -n jar rollout status deployment/jar-mcp
```

`make deploy` fills and validates the manifests, then runs `kubectl apply -k` against the current context. Replace `jar` in follow-up commands when using another `NAMESPACE`. It creates the indexing Job and the chat and MCP Deployments together; on an empty database, `jar-chat` and `jar-mcp` can restart until indexing finishes. Open `http://jarchat` through Tailscale, or the exact origin configured in `CHAT_PUBLIC_URL`, once chat is ready.

The generated `jar-index` Job is a bootstrap step. Its `-skip-if-populated` flag exits successfully when `items` contains any row, before document validation/extraction or model calls, even with `FORCE_INDEX=true` or missing ready-index metadata. An empty/missing `items` table follows normal indexing. A successful skip checks chunk existence rather than index readiness or embedding compatibility. This flag requires a current `jar-index` image containing the feature.

Deployment does not build/push images or automatically delete/recreate an existing Job. For updates, see [Regenerate and recreate the indexing Job](#regenerate-and-recreate-the-indexing-job).

## 4. Optional: research agent with kagent

`make deploy` also runs `jar-mcp`, which offers the existing index to agents as read-only [Model Context Protocol](https://modelcontextprotocol.io) tools: `index_status`, `search_passages`, `list_sources`, `get_passage_context`, and `get_conference_text`. It embeds search queries with the configured embedding service and reads DuckDB. It never indexes, rebuilds metadata, resets tables, or calls the chat model. The `jar-mcp` Service is cluster-internal (ClusterIP, port `80` to `MCP_PORT`, no Tailscale annotations) and unauthenticated: any Pod that reaches `http://jar-mcp.jar.svc.cluster.local/mcp` can read indexed text.

The kagent `librarian` agent reaches it over that Service. Its `RemoteMCPServer` names the URL, and its `AgentTemplate` binds that server and a `ModelConfig` for Ministral. The agent runs on an Agent Substrate Worker, and every outbound call leaves through the Substrate egress gateway in `ate-system`, which allows the bound MCP server and model URLs without a separate EgressPolicy. If you add NetworkPolicies to `jar`, admit `ate-system` to `jar-mcp` and `ministral-3-8b-predictor`.

kagent 1.0 needs Kubernetes 1.37 or later, Helm, `jq`, `openssl`, and `curl`. If the [`kubectl-ate` plugin](https://github.com/kagent-dev/substrate/releases) is missing, `hack/k8s-setup/05-kagent.sh` installs the build that matches its `SUBSTRATE_VERSION` into `~/.local/bin` after checking its published SHA-256 (`KUBECTL_ATE_DIR` picks another folder; `INSTALL_KUBECTL_ATE=false` only checks). Add that folder to your `PATH` to run `kubectl ate` yourself. Then create the agent:

```bash
bash hack/k8s-setup/05-kagent.sh
kubectl -n kagent apply -f config/manifests/k8s/kagent/example-librarian.yaml
kubectl -n kagent get agents,remotemcpservers
kubectl -n kagent port-forward svc/kagent-ui 8082:8080
```

Open `http://localhost:8082` and choose `librarian`; the UI shows each tool call.

The script follows kagent's Helm installation, except that PostgreSQL runs in the `kagent` namespace from [example-postgres.yaml](config/manifests/k8s/kagent/example-postgres.yaml): one Deployment, a 5 GiB PVC, and a NetworkPolicy that admits only `kagent` and `ate-system`. The script generates the superuser and login passwords into Secrets (`kagent-postgres-admin`, `kagent-postgres-owner`, and `kagent-postgres-readwrite` in `kagent`; `substrate-postgres-owner` and `substrate-postgres-readwrite` in `ate-system`). It runs kagent's and Agent Substrate's pinned `setup.sql` scripts, verified by SHA-256, inside the database Pod, and prints no secrets. Re-running it keeps existing pools, Secrets, and logins. The connection strings use `sslmode=disable` because the example database has no TLS; beyond a lab, use a TLS-enabled PostgreSQL and the `sslmode=verify-full` settings in the kagent documentation. The script also creates a placeholder `kagent-openai` Secret for the chart's default ModelConfig, which the Librarian does not use.

[example-librarian.yaml](config/manifests/k8s/kagent/example-librarian.yaml) defines the `ministral` ModelConfig (llama.cpp ignores its placeholder API key), the `jar-mcp` RemoteMCPServer, the `librarian` AgentTemplate with its prompt and tool list, a `librarian` Harness on the chart's `kagent-default` WorkerPool, and the Agent. Change both service URLs when JAR's `NAMESPACE` is not `jar`. Agent conditions (`ResolvedRefs`, `Ready`) report wiring problems; denied egress appears in `kubectl -n ate-system logs deploy/atenet-egress -c agentgateway`.

The KServe example serves Ministral with `-c 4096`. A system prompt, five tool definitions, and a few search results fill that window quickly, so raise the context in the InferenceService, within GPU memory, before relying on multi-step research. Tool calling needs `--jinja`, which the example already sets.

To try `jar-mcp` without kagent, run `kubectl -n jar port-forward svc/jar-mcp 18090:80` and point an MCP client, such as MCP Inspector, at `http://127.0.0.1:18090/mcp`.

## Deployment details

### Model selection and index compatibility

`CHAT_API_MODEL` and `EMBEDDING_API_MODEL` are the API model IDs. If empty, each endpoint must advertise exactly one model through `/v1/models`; shared multi-model endpoints need explicit IDs. JAR calls `/v1/chat/completions` and `/v1/embeddings` and accepts URLs with or without `/v1`. Bare model host/port values use HTTP. The current client does not send API-key authentication headers; configure the services for that access model.

`EMBEDDING_MODEL` is the separate canonical identity stored in index metadata and the corpus fingerprint. The existing default is `bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0` with `DIMENSIONS=768`; it describes Granite's weights/configuration rather than downloading or choosing a server model. The remote API alias can remain `granite-embedding`.

To reuse an index, retain the same embedding weights, preprocessing, vector dimensions, canonical identity, and source labels. JAR checks vector sizes but cannot establish that an API alias serves identical weights. Changing the embedding model requires intentional reindexing; matching dimensions alone is insufficient. The split into `jar`/`local-jar` preserves schema and fingerprint compatibility. Chat reads the stored identity from metadata.

For llama.cpp servers reporting structured context-limit errors, JAR retries embeddings using the server tokenizer and token arrays truncated to that limit, retaining boundary special tokens. Original indexed text is preserved. Other HTTP errors report status and structured type without logging response bodies or document input. Servers lacking a compatible tokenizer must accept the input length or use smaller passages. Index creation also installs/loads VSS on the connected DuckDB server, which needs extension-download access.

### Private generated configuration

For offline generation without applying resources:

```bash
make generate-manifests
# Optional settings/output overrides:
make generate-manifests ENV_FILE=/path/to/jar.env MANIFEST_DIR=/path/to/private/jar-manifests
make generate-manifests KUSTOMIZE='kustomize build'
```

`ENV_FILE` defaults to `.env`, `MANIFEST_DIR` to `config/manifests/generated` (Git-ignored), and `KUSTOMIZE` to `kubectl kustomize` (`KUBECTL` overrides the executable). The generation/deploy targets parse the selected file as settings data without shell sourcing or Make-expression evaluation. Explicit environment/Make overrides take precedence. Do not source `.env` as a shell script.

The committed templates in [config/manifests/kustomize](config/manifests/kustomize/) (`kustomization.yaml`, `namespace.yaml`, `configmap.yaml`, `secret.yaml`, `job.yaml`, `deployment.yaml`, `service.yaml`) contain `${KEY}` placeholders and no real settings. Generation fills each template with JSON-quoted values, including the `DOCS` hostPath volumes, images, node name, pull Secret, and checksums, and writes the filled copies to `MANIFEST_DIR`. It refuses to write into the template directory, and `DB_TOKEN` is accepted only by `secret.yaml`. The stable ConfigMap/Secret names are `jar-runtime` and `jar-secret`. `secret.yaml` contains base64-encoded `data.DB_TOKEN`; encoding does not hide the token. The output directory has mode `0700` and generated files `0600`. Keep the bundle private and Git-ignored even with a custom output path.

Kustomize validates the combined resources in memory, preserving the previous bundle if validation fails. No combined rendered manifest is saved. Manually printing `kubectl kustomize` output includes the Secret. `make deploy` always runs `generate-manifests` first and then applies `MANIFEST_DIR`; do not apply `config/manifests/kustomize` directly.

### Application resources and browser access

The bundle contains namespace `NAMESPACE`, a one-shot `jar-index` Job (`restartPolicy: Never`, `backoffLimit: 0`, one completion), one-replica `jar-chat` and `jar-mcp` Deployments, the `jarchat` Service on port `80`, and the cluster-internal `jar-mcp` Service on port `80`. They forward to `CHAT_PORT` (default `18080`) and `MCP_PORT` (default `18090`). `jar-chat` runs the `K8S_CHAT_IMAGE` image and `jar-mcp` runs the `K8S_MCP_IMAGE` image. Explicit workload flags apply the configured index batching/model/document options and browser settings.

The Service has `tailscale.com/expose: "true"` and `tailscale.com/hostname: "jarchat"`. The existing Tailscale operator exposes application HTTP; HTTPS needs your existing TLS endpoint and matching public origin. It does not rewrite Host or relax same-origin checks. JAR has no application login: users with access to the exposed endpoint can use the shared chat and indexed content.

Browser tabs share one conversation, language/search state, and references. Reloading keeps server history but does not restore the visible transcript. `/quit` ends the conversation without stopping the web server. See [README chat commands](README.md#chat-commands) for retrieval and language controls.

### Regenerate and recreate the indexing Job

Stable ConfigMap/Secret references use Pod-template checksum annotations. Configuration changes trigger a chat Deployment rollout, while a Job's Pod template is immutable; changed checksums can make updating an existing Job fail. Applying an unchanged completed Job does not rerun it.

To intentionally recreate the bootstrap Job after changing its settings:

```bash
make generate-manifests
kubectl -n jar delete job jar-index --cascade=foreground --wait=true
make deploy
```

Foreground deletion waits for the old Pod to stop before a replacement starts. Use your configured namespace. The recreated Job still skips if any chunks exist; changing `DOCS` or `FORCE_INDEX` does not bypass the guard. Intentional index replacement uses the indexer without `-skip-if-populated`, or the explicit [reset workflow](#reset-the-connected-index), both described at the end of this guide. No automatic reset is performed.

### Alternative models and optional gateway

[The Qwen embedding example](config/manifests/k8s/kserve/example-isvc-serving-runtime-qwen-8B-embedding.yaml) supplies a namespace-scoped ROCm runtime and `qwen3-embedding-8b` service in `jar`, using the same `hf-gguf` downloader. It is a replacement path for the AMD GPU used by Granite: plan the switch without requesting the same exclusive GPU for both. It uses last-token pooling, 4096 output dimensions, and a different model identity. Configure its predictor URL/API ID and matching canonical identity/dimensions, then deliberately rebuild the index; the bootstrap Job will still skip an existing corpus.

[The AI Gateway example](config/manifests/k8s/ai-gateway/example-ai-gateway.yaml) optionally routes both Ministral and Granite by model ID in `jar`. `hack/k8s-setup/05-aigateway.sh` installs its prerequisites with Helm and applies it. Once deployed, use the gateway's Pod-reachable Service URL for both model endpoints and retain the explicit API model IDs. Direct predictor Services are sufficient for the primary flow.

## Container images

This is an optional release procedure when the cluster cannot already pull suitable JAR images. Docker and a registry account with publish access are required. Build and publish before returning to `make deploy`; deployment itself does not build or push.

### Build

Run these commands from the repository root with Docker running:

```bash
make build                  # Build the jar-index, jar-chat, and jar-mcp images for linux/amd64
make build-index            # Build only jar-index from Dockerfile.index
make build-chat             # Build only jar-chat (Dockerfile target jar-chat)
make build-mcp              # Build only jar-mcp (Dockerfile target jar-mcp)
```

Image build targets default to `PLATFORM=linux/amd64`, including on ARM64 hosts. Override `PLATFORM` for another architecture; `DOCKER`, `INDEX_IMAGE`, `CHAT_IMAGE`, `MCP_IMAGE`, and `DOCKER_BUILD_FLAGS` remain configurable, for example:

```bash
make build INDEX_IMAGE=example/jar-index:dev CHAT_IMAGE=example/jar-chat:dev MCP_IMAGE=example/jar-mcp:dev DOCKER_BUILD_FLAGS=--pull
make build PLATFORM=linux/arm64
```

Use `PLATFORM` with `make build-index`, `make build-chat`, or `make build-mcp` to select the same architecture for an individual image. Reserve `DOCKER_BUILD_FLAGS` for additional build options such as `--pull`; the bundled DuckDB bindings support Linux AMD64 and ARM64.

Both Dockerfiles compile the Go commands with CGO in a multi-stage Go 1.27.3 build. The index image uses Debian Trixie slim with CA certificates, the C++ runtime, Poppler, headless LibreOffice Writer, `wvWare`, and DejaVu fonts for PDF/DOC/DOCX extraction. The smaller chat and MCP images are two build targets of `Dockerfile` on `gcr.io/distroless/cc-debian13:nonroot`: `jar-chat` (the default target) contains the chat command with its embedded browser interface, and `jar-mcp` contains only the MCP server. Neither has a shell or document converters.

All three images run as UID/GID `65532`, with writable `/home/nonroot` for the DuckDB client's extension cache and temporary space for runtime work. The build context is allowlisted in `.dockerignore`: source, module files, license/notice, and Dockerfiles are included; `.env`, documents, database files, local caches, and binaries are excluded. Models and the DuckDB server remain separate services.

### Push images

Build the local images, sign in, then publish them:

```bash
make build
docker login ghcr.io -u brito-rafa
make push
```

At the password prompt, enter a GitHub personal access token (classic) with `write:packages` permission. Follow the [GitHub Container registry authentication guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry); keep the token out of command arguments and repository files.

`make push` runs `push-index`, `push-chat`, and `push-mcp`. With `REGISTRY=ghcr.io/brito-rafa` and `IMAGE_TAG=latest`, it tags and pushes the existing local images to `ghcr.io/brito-rafa/jar-index:latest`, `ghcr.io/brito-rafa/jar-chat:latest`, and `ghcr.io/brito-rafa/jar-mcp:latest`. These targets do not build images or sign in automatically; rebuild explicitly when source changes. To publish one image, use `make push-index`, `make push-chat`, or `make push-mcp`.

Override the destination namespace and tag as needed (a trailing slash on `REGISTRY` is accepted):

```bash
make push REGISTRY=ghcr.io/another-owner IMAGE_TAG=v1.0.0
```

`INDEX_IMAGE`, `CHAT_IMAGE`, and `MCP_IMAGE` select the existing local source images, independently of the destination. The destination names remain `REGISTRY/jar-index:IMAGE_TAG`, `REGISTRY/jar-chat:IMAGE_TAG`, and `REGISTRY/jar-mcp:IMAGE_TAG`; custom source names do not rename the published packages. Sign in to the destination registry with an account permitted to publish there. Push sends the selected images as built; use `PLATFORM` on the build targets to choose their architecture.

## Troubleshooting and development

The commands below run on the client machine or are explicit maintenance/debugging operations. They are separate from the Kubernetes installation sequence above.

### Native requirements

Go 1.27.3 or newer and a C compiler are needed for the native DuckDB client. On Apple Silicon, use native ARM64 Go; install Apple command-line tools with `xcode-select --install` when required. Native indexing additionally needs document converters. The application images already include their runtime dependencies; deploying existing images needs no host Go installation.

#### Document converters on Linux

On Debian/Ubuntu, install the local converters:

```sh
sudo apt-get install poppler-utils libreoffice-writer wv
```

PDF extraction uses `pdftotext`. Word extraction uses `wvWare` for DOC when available, then headless LibreOffice for DOC/DOCX. LibreOffice alone handles both Word formats. Scanned PDFs without a text layer still need OCR before indexing; empty documents are skipped with a warning.

#### Document converters on macOS

LibreOffice is not required for JAR itself. Some very old Word files, including Word 2.0 documents, need it because other converters can return binary data or incorrect text.

| Input | Converter | Installation |
| --- | --- | --- |
| TXT | Go file reading | None |
| PDF | Poppler `pdftotext` | `brew install poppler` |
| DOCX and most DOC | macOS `textutil` | Included with macOS |
| Legacy DOC | `wvWare` fallback | `brew install wv` |
| DOC still unreadable after those converters | LibreOffice fallback | `brew install --cask libreoffice` |

Native indexing runs converters on the client machine. LibreOffice is detected through `soffice` on PATH or its standard macOS application path, and runs headlessly with temporary output and a separate temporary profile. Source documents are unchanged. Extracted text is sent to your configured DuckDB server for storage. Passages, questions, and reference-translation text are also sent to your configured model endpoints.

### Connectivity, endpoints, and port-forwards

Inspect workloads and recent startup failures in the intended namespace when a Job cannot mount documents, a model cannot obtain a GPU, or an image cannot be pulled. The index Pod needs every hostPath before it starts; the populated-index guard cannot bypass Kubernetes volume setup. Chat readiness depends on successful application startup against a usable index.

For client-side debugging of the infrastructure examples, keep these forwards in separate terminals:

```bash
kubectl -n jar port-forward svc/ministral-3-8b-predictor 8080:80
kubectl -n jar port-forward svc/granite-embedding-predictor 8081:80
kubectl -n jar port-forward svc/duckdb 9494:9494
```

A native client can then use `CHAT_URL=http://127.0.0.1:8080`, `EMBEDDING_URL=http://127.0.0.1:8081`, and `DB_URL=quack:127.0.0.1:9494` with the existing database token and `DB_DISABLE_SSL=true`. These localhost addresses apply to that client, not to JAR Pods. To debug only the application browser Service, use `kubectl -n jar port-forward svc/jarchat 18080:80` and browse `http://127.0.0.1:18080`; local Host/Origin checks remain enabled. For the MCP Service, use `kubectl -n jar port-forward svc/jar-mcp 18090:80` and connect an MCP client to `http://127.0.0.1:18090/mcp`.

`hack/k8s-setup/04-portforward.sh` requires Tailscale and additionally binds infrastructure forwards to the host's Tailscale IPv4 address and forwards DuckDB UI port `4213`. Use it only for the intended host exposure. `01-minikube-start.sh` is a separate Linux AMD lab helper (profile `kserve`, 10 CPUs/32 GiB by default); it passes AMD devices, turns on the `certificates.k8s.io/v1beta1` PodCertificateRequest API that `05-kagent.sh` needs (and warns if an existing profile still lacks it), and does not supply the NVIDIA GPU required by the unmodified chat example. Select the intended kubectl context before running other scripts.

```bash
make check-connectivity
# Or run the script directly:
./hack/check-connectivity.sh
```

The script reads the repository's `.env` from any working directory; explicit process environment settings override the file. It makes small synthetic embedding/chat requests and checks database TCP reachability. The chat check generates up to 32 tokens and reports time/throughput when usage is available. It does not authenticate to DuckDB; `make status` validates that without changing data. Run these native checks with client-reachable endpoint values, rather than cluster-only DNS names unavailable on the client.

`DB_DISABLE_SSL=false` selects the default database TLS transport; use `true` only for an intentionally plain-HTTP service. Plain HTTP sends the token without application TLS and needs a trusted network/transport. Keep using Quack while the server owns its file, and back up files after a clean server stop. DuckDB Compose belongs to [local-jar](https://github.com/brito-rafa/local-jar).

### Run indexing and chat from source

With native dependencies and client-reachable `.env` endpoints:

```bash
make index DOCS="/absolute/path/to/documents"
# Wait for indexing to finish.
make chat-web
make chat-terminal
make mcp
```

Choose one chat interface. `make mcp` serves the read-only MCP tools on `http://127.0.0.1:18090/mcp` (`MCP_HOST`, `MCP_PORT`) and needs only the embedding and database settings. Both chat targets run model/database connectivity checks before starting; indexing checks embedding/database connectivity. `make index` validates existing local directories before model/database work and has no `DOCS` default. It rejects blank lists, empty entries, files, and nonexistent directories. Status, metadata, chat, and help do not need `DOCS`.

Ordinary indexing retains the corpus-fingerprint/update behavior; it does not use the Kubernetes bootstrap guard. `FORCE_INDEX=true` intentionally rebuilds an otherwise unchanged corpus. Source commands execute extraction locally and send passages/questions to configured model endpoints and stored text to DuckDB.

### Direct CLI options

Make reads `.env`; direct binaries read exported runtime variables and CLI flags rather than parsing that file. For example:

```bash
export DB_TOKEN='<your-server-token>'
go run ./cmd/jar-index -docs "/path/to/documents" \
  -db-url quack:duckdb.example.com:9494 \
  -embedding-url https://embedding.example.com \
  -embedding-api-model granite-embedding

go run ./cmd/jar-chat -db-url quack:duckdb.example.com:9494 \
  -chat-url https://chat.example.com -chat-api-model ministral-3-8b \
  -embedding-url https://embedding.example.com \
  -embedding-api-model granite-embedding

go run ./cmd/jar-mcp -db-url quack:duckdb.example.com:9494 \
  -embedding-url https://embedding.example.com \
  -embedding-api-model granite-embedding -port 18090
```

`make build-binaries` creates native `bin/jar-index`, `bin/jar-chat`, and `bin/jar-mcp`. Use `make cli-help` or any command's `-help` for all flags. `jar-mcp` listens on `127.0.0.1` unless `-host` says otherwise and has no authentication. `-skip-if-populated` is optional for direct indexing and wins over `-force` when a chunk exists.

### Native browser options

```bash
make chat-web OPEN_BROWSER=false
make chat-web CHAT_PORT=8081
make chat-web CHAT_PUBLIC_URL=https://your-machine.your-tailnet.ts.net
```

Native web chat defaults to `127.0.0.1:18080`; `make chat` and `make run` alias it. An exact public origin supports an existing reverse proxy. For intentionally shared unauthenticated access on all IPv4 interfaces, use `CHAT_HOST=0.0.0.0 CHAT_ALLOW_ANY_HOST=true`; these options do not change firewall or Tailscale permissions. Browser same-origin checks remain enabled. The web interface supports Tab completion, Enter to send, and Shift+Enter for a newline; Ctrl+C stops the server.

### Multiple document directories

```bash
make index DOCS="/path/to/books;/path/to/conferences;/path/with spaces"
# Or set this in .env (no shell quotes needed in that file):
# DOCS=/path/to/books;/path/to/conferences;/path/with spaces
```

The same list works with `jar-index -docs "/path/one;/path/two"`. Quote the list on the command line so the shell does not interpret semicolons as commands. Whitespace around entries is trimmed; empty entries (including a trailing `;`) are rejected. A literal semicolon in a directory name is not supported. `.env` is read as Make settings; do not source a file containing an unquoted semicolon list as a shell script. For shell exports, use `export DOCS="/path/one;/path/two"`.

Every directory is validated before indexing starts. Roots are scanned recursively in deterministic order using one shared document-worker pool. Duplicate roots and overlapping file paths are read once. In multi-root imports, source labels include each root's directory name to distinguish identical relative filenames; roots with the same basename get a stable hash suffix. Single-root source labels remain unchanged. All roots feed the same deduplicated database index; include the full desired set of roots on each indexing run.

### Run application images directly

#### Index mounted documents

Configure a Docker-compatible environment file with the database/model settings from [deployment settings](#2-create-and-configure-env). The unquoted `KEY=value` entries in `.env.example` can be used with `--env-file .env`; Docker does not evaluate Make expressions, expand variables, or remove shell-style quotes from values. Preserve any existing credentials privately.

```bash
docker run --rm --env-file .env \
  -v "/absolute/path/to/your/documents:/documents:ro" \
  jar-index -docs /documents \
  -embedding-model "bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0" \
  -dimensions 768
```

The mounted directory and files must be readable by UID `65532`; extraction writes only to temporary container storage. For multiple roots, mount each directory read-only and pass their container paths as a quoted semicolon list, such as `-docs "/books;/conferences"`.

Container commands use the Go CLI directly. `DB_URL`, `DB_TOKEN`, `DB_DISABLE_SSL`, `CHAT_URL`, `EMBEDDING_URL`, `CHAT_API_MODEL`, and `EMBEDDING_API_MODEL` are read from the process environment. `DOCS`, `EMBEDDING_MODEL`, `DIMENSIONS`, batching options, and browser settings in a Make `.env` do not automatically become CLI flags: pass `-docs`, `-embedding-model`, `-dimensions`, and any other desired flags explicitly. Keep the canonical identity, dimensions, and source labels consistent with an existing index. Moving a multi-root corpus to different mount paths can change its source-label fingerprint.

Without arguments, `jar-index` displays CLI help. Status and metadata do not need a document mount:

```bash
docker run --rm --env-file .env jar-index -status
docker run --rm --env-file .env jar-index -metadata
```

#### Run chat

After indexing finishes:

```bash
docker run --rm --env-file .env \
  -p 127.0.0.1:18080:8080 jar-chat
```

Open `http://127.0.0.1:18080`. The image starts browser chat on `0.0.0.0:8080` inside the container with automatic browser launch disabled; the published host port remains loopback-only. Existing Host and same-origin checks still apply. Terminal chat replaces the image's default arguments:

```bash
docker run --rm -it --env-file .env jar-chat -web=false
```

Any arguments after `jar-chat` replace its complete default argument list. For example, with an existing reverse proxy, retain the web/listener flags when supplying its exact public origin:

```bash
docker run --rm --env-file .env -p 127.0.0.1:18080:8080 jar-chat \
  -web -host 0.0.0.0 -port 8080 -open-browser=false \
  -public-url https://your-machine.your-tailnet.ts.net
```

Use CLI flags to override the image defaults; Make variables such as `CHAT_PORT` and `CHAT_PUBLIC_URL` have no effect on direct container runs.

Run the MCP server from the `jar-mcp` image; by default it listens on port `8090` on all container interfaces:

```bash
docker run --rm --env-file .env -p 127.0.0.1:18090:8090 jar-mcp
```

Connect an MCP client to `http://127.0.0.1:18090/mcp`. The published port stays loopback-only; the server itself has no authentication.

#### Reach services on the host

Use remote service addresses as usual. For a service running on the Docker host, `localhost` and `127.0.0.1` inside the container refer to the container itself. On Docker Desktop, use `host.docker.internal` in the configured model/database URLs. On Linux, add `--add-host host.docker.internal:host-gateway` to the `docker run` command and use that hostname; the service must already listen on an address reachable through the host gateway. This option does not expose a host-loopback-only service or change its listener configuration.

### Deduplication and concurrent indexing

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

### Indexing performance and batching

#### Batch size and queue capacity

The completed-embedding queue defaults to 16 batches, independently of worker concurrency. With batches of 8, it buffers up to 128 completed chunks while inserts run. Additional chunks may be in flight or awaiting an insert. This bounds memory without forcing a single embedding worker to pause after one queued batch. A larger buffer can reduce temporary backpressure; it cannot overcome a sustained database bottleneck.

```bash
# One embedding worker, 8 chunks per request, 128-chunk queue.
make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=8 EMBEDDING_BUFFER_BATCHES=16

# Compare larger requests while retaining a 128-chunk queue.
make index DOCS="/path/to/documents" EMBEDDING_WORKERS=1 EMBEDDING_BATCH_SIZE=16 EMBEDDING_BUFFER_BATCHES=8
```

The direct indexer flag is `--embedding-buffer-batches=16`. Set `EMBEDDING_BUFFER_BATCHES=16` in `.env` to persist it. An already unchanged index is reused; add `FORCE_INDEX=true` only when intentionally rebuilding it for a comparison.

#### Indexing metrics

Loading reports embedding batch-request latency (average/maximum and failures), insert-call latency, elapsed time, written chunks per second, and result-queue wait summed across workers. Embedding latency includes network, server queueing and any token-limit recovery; insert latency includes serialization and source-metadata writes. These are client timings, not GPU kernel timings. Progress is provisional until the transaction commits; a failed load rolls back its writes. Metrics are also printed on failure.

### Chat timing and model metrics

#### Client timings

Chat answers include client timing for query embedding, database vector search, total retrieval (including embedding and result preparation), generation across all attempts, citation/grounding validation, and total handling time. The terminal and web interface also report timing for failed queries. Values include network and server waits; total handling excludes HTTP session-lock wait, browser rendering, and response delivery. Retrieval includes its embedding/search substeps and should not be added to them. Remote answers are buffered, so time to first token is not measured.

#### Remote server metrics

Each remote chat completion logs a JSON `model_response_metrics` event with `usage.prompt_tokens`, `usage.completion_tokens`, `timings.prompt_ms`, and `timings.predicted_ms`, as supplied by llama.cpp. This includes each answer retry and reference-translation part. Missing values are `null`; actual zeroes are retained. Timings are server-reported milliseconds, distinct from JAR's client request durations. Logs contain response ID, model ID and numeric metrics, without prompts or generated text.

### Reset the connected index

To erase only JAR's index tables on the configured database server:

```bash
make db-reset CONFIRM=erase
```

Without `CONFIRM=erase`, the command refuses to proceed. Stop indexing and chat first. This connects to `DB_URL` using `DB_TOKEN` and transactionally drops JAR's `items`, `item_sources`, and `jar_index_metadata` tables. Unrelated tables, the server process, and local files are preserved. It does not require model endpoints or load models. Run `make index DOCS="/path/to/documents"` afterwards to rebuild the index.

### Index status

```bash
make status
```

Status reports the connected endpoint, readiness, chunk count, embedding identity, vector dimensions, and completion timestamp in UTC. It does not load models, count source documents, maintain a per-file skip history, or inspect a local database file. Runtime indexing warnings identify skipped files.

### Metadata maintenance

New indexes include this metadata. To add it to an existing index without regenerating embeddings:

```bash
make metadata
```

### Parameter migration

Remote/local provider selection has been removed. Update an existing ignored `.env` to the new names; old `REMOTE_*`, `JAR_REMOTE_*`, and `remote-*` parameters are not aliases.

| Previous remote setting | Remote JAR setting |
| --- | --- |
| `REMOTE_CHAT` / `-remote-chat` | `CHAT_URL` / `-chat-url` |
| `REMOTE_EMBEDDING` / `-remote-embedding` | `EMBEDDING_URL` / `-embedding-url` |
| `REMOTE_CHAT_MODEL` / `-remote-chat-model` | `CHAT_API_MODEL` / `-chat-api-model` |
| `CHAT_MODEL` / `-chat-model` (previous remote name) | `CHAT_API_MODEL` / `-chat-api-model` |
| `REMOTE_EMBEDDING_MODEL` / `-remote-embedding-model` | `EMBEDDING_API_MODEL` / `-embedding-api-model` |
| `REMOTE_DB=host:port` | `DB_URL=quack:host:port` |
| `REMOTE_DB_TOKEN` | `DB_TOKEN` |
| `REMOTE_DB_DISABLE_SSL` | `DB_DISABLE_SSL` |
| `make remote-index` | `make index` |
| `make remote-chat` | `make chat-web` |
| `make remote-db-reset` | `make db-reset` |

Keep the existing canonical `EMBEDDING_MODEL` and `DIMENSIONS` for the saved index. The previous local `CHAT_MODEL` GGUF download reference is not automatically an API model ID: use the service's actual ID or leave `CHAT_API_MODEL` empty for discovery. Compose settings such as `DB_PORT` and `DB_DATA_DIR` belong in `local-jar`.

This split does not move credentials, database files, document collections, training data, or cached models. Both applications can connect to an existing compatible database, but finish indexing before starting chat and run only one indexer at a time. Preserve existing database files, persistent volumes, and backups.

### Development and validation

```bash
make test-manifests
make test
make vet
make build-binaries
make build
```

The test, vet, and native-binary targets use host Go; the image targets use Docker. Use native ARM64 Go on Apple Silicon, for example `make test GO=/opt/homebrew/bin/go`. Run `gofmt` on changed Go files, and `staticcheck ./...` when available. Relevant concurrency changes also require `go test -race ./pkg/chat ./pkg/duck`.

Ordinary tests use synthetic documents and fake model callbacks; they do not download inference models or require running services. Converter tests skip when their tools are unavailable. Remote database integration tests remain opt-in. Inspect a test before enabling it: some create/drop corpus tables and must use a dedicated disposable server, token, and nondefault port. Do not point destructive fixtures at a real index, and clean up only resources created for the test. The local-model live test belongs only to [local-jar](https://github.com/brito-rafa/local-jar).

The destructive database fixtures select isolated endpoints with `JAR_INDEX_TEST_URL`, `JAR_METADATA_TEST_URL`, or `JAR_LOAD_TEST_URL` and read `DB_TOKEN` from the environment. For example, after provisioning a disposable authenticated database on a dedicated test port:

```bash
export DB_TOKEN='<disposable-server-token>'
JAR_INDEX_TEST_URL=quack:127.0.0.1:19494 go test ./pkg/duck -run '^TestRemoteIndexReplacesCatalogTransactionally$' -v
```

`JAR_TEST_REMOTE=1` enables older transaction/authentication tests that use `127.0.0.1:9494` directly. Inspect and isolate that endpoint before enabling them; it must not be the real index.
