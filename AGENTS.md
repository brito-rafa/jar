# Working on JAR

These instructions apply throughout this repository. Follow explicit user instructions; do not ask again for actions already authorized in the current task.

## Project layout

JAR is a multilingual RAG client written in Go. Chat and embedding inference use configured OpenAI-compatible HTTP endpoints; DuckDB is a separate service accessed through Quack over TCP. Local Kronk inference and DuckDB Compose setup belong to the independent sibling repository `github.com/brito-rafa/local-jar`. Do not reintroduce provider selection or a Kronk dependency here.

- `cmd/jar-index`: document ingestion, index status, and source metadata maintenance.
- `cmd/jar-chat`: shared chat engine exposed through browser and terminal interfaces.
- `pkg/ingest`: recursive document extraction and index orchestration.
- `pkg/chat`: retrieval, conversation state, citations, slash commands, translation, and HTTP handlers.
- `pkg/chat/web`: embedded HTML, CSS, and JavaScript; no frontend build tool is required.
- `pkg/catalog`: filename-derived source metadata.
- `pkg/duck`: remote SQL, indexing, deduplication, vector search, and source context retrieval.
- `pkg/inference`: OpenAI-compatible HTTP inference, response metrics, and application-owned request/response types.
- `pkg/connection`: shared database connection flags.
- `hack/check-connectivity.sh`: configured model/database connectivity checks.
- `hack/k8s-setup` and `config/manifests/k8s`: example Kubernetes model and database deployments.
- `assets/images`: screenshots and other README assets.

Keep the two explicit command entry points. Do not recreate a root `main.go`, bundled document directory, or public prebuilt-chunks input without a user request.

## Local data and privacy

- `.env`, `data/`, `bin/`, `benchmark/`, and `benchmarks/` are Git-ignored local content. Preserve those ignore rules.
- Treat document collections, database files, model caches, and training datasets as user data. Do not alter or erase them unless the task explicitly authorizes that action.
- Local training examples belong under `data/training/`. Do not move them into source code, test fixtures, screenshots, or public documentation.
- Never stage, commit, push, or upload local data, credentials, tokens, or model files without explicit authorization for those particular files.
- Never print authentication tokens or dump `.env` into command output.
- Use synthetic content for screenshots and tests. Do not expose private document contents.
- Do not hardcode real personal names, collection-specific identities, or biographical associations in application code or tests. Use neutral placeholders in fixtures. Collection-specific training facts stay in the ignored dataset.

## Protect ongoing work

- Do not stage, commit, push, or create a PR unless requested.
- For commits, use the Git author and committer identity associated with GitHub `brito-rafa`, matching commit `1899b15` (`Rafael Brito <rafaelbrito@gmail.com>`). Do not append the handle to commit titles.
- Preserve unrelated edits and user processes. Do not stop or restart a running chat, indexer, or database unless authorized.
- Do not start training, reindex the user's corpus, or download/load additional GPU models unless requested or already explicitly authorized as part of verification.
- Check for competing model processes before an authorized GPU benchmark or live-model test. Do not change concurrency defaults based only on a small synthetic benchmark.
- Do not change network exposure, Host/Origin policies, authentication, firewall rules, or Tailscale configuration unless requested. Preserve explicit local configuration choices.

## Database and integration testing

- Preserve all existing database files, persistent volumes, and backups. Never use the main endpoint for tests that create, replace, or drop corpus tables.
- Use a dedicated disposable database service and a nondefault port for destructive integration fixtures. If using the sibling repository’s Compose service, select a uniquely named project and a dedicated temporary `DB_DATA_DIR`; a different project name alone does not isolate bind-mounted files. Never use a real corpus directory or PVC for destructive tests. Ensure the test endpoint and token match the disposable service.
- Clean up only containers, volumes, files, and processes created for the test.
- Keep transaction safety, bounded ingestion concurrency, deduplication, and all source locations intact.
- Use the existing remote query helpers and their safe argument encoding. Do not interpolate document text or user input into SQL.

## Chat behavior to preserve

- English, Portuguese, and Spanish are the primary languages. Prefer relevant same-language evidence while retaining relevant evidence in other languages; honor an explicit language override.
- Keep source names and citations traceable to actual retrieved records. Do not invent bibliographic facts or identities.
- Name detection is advisory: it can warn but must not suppress an otherwise citation-valid answer or translation. Citation ID validation remains blocking after the existing retry.
- Keep vector-search results visible independently of generation/validation outcomes. Maintain consecutive final citation numbering and matching `/retrieveref` behavior.
- Preserve original indexed text in downloads. Skip translation when content confidently matches the query/override language; do not infer document language from filenames alone.
- Book reference expansion stays within identifiable chapters. When boundaries are unclear, return the cited passage and bounded neighboring context, explaining paragraph/chunk approximations. Never silently fall back to assembling the entire book.
- Browser content must be rendered safely; do not insert document/model HTML using `innerHTML`.

## Implementation and verification

Use the Go version declared in `go.mod`. On Apple Silicon, use native ARM64 tools; executable overrides such as `GO=/opt/homebrew/bin/go` are supported by Make.

```sh
make test
make vet
make build
```

For relevant concurrency changes:

```sh
go test -race ./pkg/chat ./pkg/duck
```

Run `gofmt` on modified Go files. Run `staticcheck ./...` when available. Choose checks appropriate to the change; documentation-only edits do not require loading models or running database integration tests.

Use temporary test directories and fake model callbacks for ordinary tests. Opt-in network integration tests must stay opt-in and use their documented environment variables; inspect a test before enabling it, particularly if it mutates database state.

Keep README examples, Makefile help, CLI flags, and web slash-command help synchronized when behavior changes. `make index` requires an explicit `DOCS` list of existing directories separated by semicolons and must fail before starting Go/model/database work if it is missing or invalid. Chat, status, metadata, help, and reset do not require `DOCS`. Keep `CHAT_URL`, `EMBEDDING_URL`, `DB_URL`, and `DB_TOKEN` configuration consistent between Make, CLI, connectivity checks, `.env.example`, and documentation. API embedding aliases remain separate from the canonical embedding identity stored in index fingerprints; preserve existing schema and fingerprint compatibility with `local-jar`.

After modifying embedded web assets, rebuild/restart the server for verification; reloading a tab alone does not reload embedded source files. Use sample-data browser fixtures to check UI flows without competing with the user's GPU workload.
