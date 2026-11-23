# JAR - Just Another RAG

## Overview

JAR is a multilingual retrieval-augmented generation (RAG) application that recursively indexes **PDF (`.pdf`), Word (`.doc` and `.docx`), and plain-text (`.txt`) files** and answers questions using retrieved evidence with source references.

**JAR in this repository is intended to run as a cloud native application on Kubernetes.** A `jar-index` Job extracts documents and stores passages and embeddings in a separate DuckDB service. A `jar-chat` Deployment serves the browser interface, using OpenAI-compatible chat and embedding services such as KServe. Document converters are included in the index image; inference models run in their own services.

Created by [brito-rafa](https://github.com/brito-rafa) with [OpenAI Codex](https://openai.com/codex/).

Based on the [Kronk RAG example](https://github.com/ardanlabs/kronk/blob/main/examples/rag/main.go) by [Ardan Labs](https://github.com/ardanlabs/kronk). For local inference through Kronk and llama.cpp, including DuckDB Docker Compose setup, use the independent [local-jar repository](https://github.com/brito-rafa/local-jar). Both repositories retain the `cmd/jar-index`, `cmd/jar-chat`, and `pkg/` Go structure and compatible index formats.

![JAR web interface showing the prompt, compact vector search, and a sourced answer](assets/images/web-interface.jpg)

*Example interface using synthetic demonstration text, not private documents.*

## Deploy on Kubernetes

Everything runs in the `jar` namespace by default.

1. **Set up DuckDB and KServe.** Follow [SETUP.md](SETUP.md#1-install-kubernetes-services) to deploy DuckDB, install KServe, and serve the chat and embedding models.

2. **Create `.env`.** Copy the example, then set the database token, service URLs, and document paths as described in [SETUP.md](SETUP.md#2-create-and-configure-env):

   ```sh
   cp .env.example .env
   ```

3. **Deploy JAR.** From the repository root, with the intended kubectl context selected:

   ```sh
   make deploy
   ```

   This fills the templates in `config/manifests/kustomize/` from `.env`, writes them to the Git-ignored `config/manifests/generated/`, and applies them. See [SETUP.md](SETUP.md#3-deploy-jar) to follow indexing and open the chat.

## Import behavior

- Recursively imports `.doc`, `.docx`, `.pdf`, and `.txt`, including uppercase extensions.
- Creates overlapping passages of up to 180 whitespace-separated words with 30 words of overlap. PDF pages are chunked separately; the embedding model's token limit still applies.
- Includes relative filenames and PDF page numbers in context; Word/TXT references use section numbers rather than rendered page numbers.
- Skips symlinks, hidden files/directories, Word lock files, and unsupported formats.
- Password-protected PDFs and documents with no extractable text are skipped with warnings. Scanned/image-only PDFs need OCR before indexing; LibreOffice does not perform OCR.
- Conversion failures stop indexing with the filename and a diagnostic. Legacy Word output containing NUL bytes is rejected or sent through converter fallbacks rather than indexed as binary text.

`-docs` is required for indexing; status, metadata maintenance, and reset do not read documents. Prepared `.chunks` files and the `CHUNKS` Make variable are no longer supported. Pass a directory containing DOC, DOCX, PDF, or TXT files; JAR creates retrieval passages internally.

During an intentional index update, the indexer extracts the corpus and fingerprints its text and embedding configuration. Unchanged indexes are reused without re-embedding the corpus. Changed corpora replace the index in one transaction; the previous committed corpus stays intact if indexing fails. Indexing is **not resumable** in this version: an interrupted new corpus must be indexed again. Use one indexer at a time and finish indexing before starting a chat session, especially when changing the embedding model.

## Chat behavior

English, Portuguese, and Spanish are the primary languages. JAR retrieves multilingual passages, prefers relevant evidence in the question's language, and answers in that language unless `/language` overrides it. Vector results remain visible while the answer is generated.

Answers include numbered citations and source filenames with page or section labels. Invalid citation IDs trigger one retry; personal-name checks are advisory and do not suppress citation-valid answers. Neither check proves factual accuracy—use `/retrieveref` to inspect the evidence.

## Chat commands

Tab completes commands, languages, indexed titles, dates, and reference numbers in the browser and interactive terminal.

| Command | Action |
| --- | --- |
| `/help` | Show all commands |
| `/list books [name]` | List non-conference documents |
| `/list conferences [name or date]` | List conferences |
| `/search book <title or filename>` | Restrict retrieval to matching files |
| `/search date <date>` | Restrict retrieval by filename-derived date |
| `/search clear` | Clear the search filter and conversation |
| `/clearsearch` | Clear only the search filter |
| `/language <language>` | Set `en`, `pt`, `es`, or `auto` detection |
| `/reset` | Clear conversation and search; retain language setting |
| `/retrieveref <number>` | Show a cited passage and download its source context |
| `/retrieve <date>` | Retrieve and download all indexed conference text for a date |
| `/quit` | End the conversation |

Changing a search filter or language clears conversation history and saved references. `/clearsearch` preserves them. Reference numbers belong to the latest answer; vector-search candidate numbers are separate.

`/retrieveref` preserves the exact cited passage. It expands conferences in full and books to detected chapters; unclear chapter boundaries use nearby paragraph/chunk context with an explicit explanation. Downloads retain the original text and append a translation in the query language, skipping translation when the content already confidently matches that language. Model-generated translations may take time and should be checked against the original.

`/retrieve` accepts a year, month, or day (`1958`, `1958-05`, `1958-05-08`), or a legacy date code (`580508`). It joins indexed conference passages in their original language, without changing the conversation or search filter.

### Filename metadata

Titles and dates are inferred from filenames, not verified bibliographic facts. For legacy conference codes, `580508RN.DOC` means `1958-05-08`, `320600CF.DOC` has month precision (`1932-06`), and `3100-1CF.DOC` has year precision (`1931`); these codes assume the 1900s. Unknown dates and abbreviations are not invented. Book filters ignore case and accents and apply before vector ranking.

New indexes include this metadata. See [metadata maintenance](SETUP.md#metadata-maintenance) for existing indexes.

## Attribution and license

Adapted from Kronk's `examples/rag/main.go` and `examples/rag/duck` at revision `50ace045`. The standalone application adds configurable models, recursive document extraction, safe database insertion, separate index/chat commands, and containerized remote storage. Adapted code is distributed under Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Build and publish application images

If you need your own images, build and publish them before deployment:

```sh
make build
make push
```

Builds default to `PLATFORM=linux/amd64`; use `PLATFORM=linux/arm64` for ARM64 nodes. The default destinations are `ghcr.io/brito-rafa/jar-index:latest` and `ghcr.io/brito-rafa/jar-chat:latest`; override `REGISTRY` and `IMAGE_TAG` to publish under an account you control. Authenticate to the registry first. See [container images](SETUP.md#container-images) for individual image targets, runtime contents, and registry authentication. `make deploy` does not build or push images.

## Troubleshooting and development

Local application commands are troubleshooting and development tools. They require host Go, a C compiler, and access to the configured services; local indexing also needs document converters. Follow the final [troubleshooting and development procedures](SETUP.md#troubleshooting-and-development) for workstation endpoint settings, port forwarding, converter installation, direct CLI/container runs, and maintenance.

```sh
make check-connectivity
make status
make chat
# Or use terminal chat:
make chat-terminal
```

`make chat` runs the browser interface on your workstation; it does not deploy JAR. For intentional index updates, use the maintenance procedures in SETUP.md and run only one indexer at a time. Native commands and Docker runs are separate from the Kubernetes installation procedure.
