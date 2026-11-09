# JAR - Just Another RAG

JAR is a multilingual retrieval-augmented generation (RAG) application that recursively indexes **PDF (`.pdf`), Word (`.doc` and `.docx`), and plain-text (`.txt`) files**. It extracts text, stores searchable passages and embeddings in DuckDB, and answers questions using retrieved evidence with source references.

By default, **IBM Granite Embedding 278M Multilingual (Q8_0)** converts document passages and questions into vectors for multilingual search. **Mistral Ministral 3 8B Instruct (Q4_K_M)** generates cited answers and requested translations. JAR consumes these models in two ways:

- **Local:** Kronk runs the models on the application machine through llama.cpp, with Metal acceleration on supported Macs.
- **Remote:** KServe runs the models in Kubernetes and exposes OpenAI-compatible HTTP APIs at configured URLs. JAR calls those endpoints instead of loading models locally.

See [setup.md](setup.md) for requirements, **local** and **Kubernetes** setup, database configuration, remote connectivity, and application commands.

## Start locally

From the repository root, with Go, a C compiler, Docker Compose, and the [document converters](setup.md#requirements) installed:

```sh
make local-db-up
make index DOCS="/absolute/path/to/your/documents"
make chat-web
```

Replace `DOCS` with an existing document directory, or several directories separated by `;`. Wait for indexing to finish before starting chat. The first run downloads dependencies and local models; browser chat opens at `http://127.0.0.1:18080`. Use `make chat-terminal` for terminal chat. On later starts, run `make local-db-up` and `make chat-web` to use the saved index.

## Start with KServe and remote DuckDB

With the [Kubernetes services deployed](setup.md#kubernetes-setup), point JAR at your KServe chat and embedding APIs and DuckDB service. Add these settings to the ignored `.env`, replacing the example addresses and token:

```dotenv
REMOTE_CHAT=https://chat.example.com
REMOTE_EMBEDDING=https://embedding.example.com
REMOTE_DB=duckdb.example.com:9494
REMOTE_DB_TOKEN=<your-remote-server-token>
```

Then run from the repository root:

```sh
make remote-index DOCS="/absolute/path/to/your/documents"
make remote-chat
```

Both commands check connectivity first. Wait for indexing to finish before starting chat; later starts only need `make remote-chat`. JAR runs locally while the configured services handle inference and storage. For plain HTTP DuckDB over a trusted encrypted network, add `REMOTE_DB_DISABLE_SSL=true` to `.env`. See [remote connectivity](setup.md#remote-connectivity) for endpoint and model configuration.

## Overview

Created by [brito-rafa](https://github.com/brito-rafa) with [OpenAI Codex](https://openai.com/codex/).

Based on the [Kronk RAG example](https://github.com/ardanlabs/kronk/blob/main/examples/rag/main.go) by [Ardan Labs](https://github.com/ardanlabs/kronk). Models run through local Kronk inference or remote KServe endpoints; DuckDB runs as a separate service accessed through Quack over TCP.

![JAR web interface showing the prompt, compact vector search, and a sourced answer](assets/images/web-interface.jpg)

*Example interface using synthetic demonstration text, not private documents.*


## Import behavior

- Recursively imports `.doc`, `.docx`, `.pdf`, and `.txt`, including uppercase extensions.
- Creates overlapping passages of up to 180 whitespace-separated words with 30 words of overlap. PDF pages are chunked separately; the embedding model's token limit still applies.
- Includes relative filenames and PDF page numbers in context; Word/TXT references use section numbers rather than rendered page numbers.
- Skips symlinks, hidden files/directories, Word lock files, and unsupported formats.
- Password-protected PDFs and documents with no extractable text are skipped with warnings. Scanned/image-only PDFs need OCR before indexing; LibreOffice does not perform OCR.
- Conversion failures stop indexing with the filename and a diagnostic. Legacy Word output containing NUL bytes is rejected or sent through converter fallbacks rather than indexed as binary text.

`-docs` is required for indexing (except `-status`). Prepared `.chunks` files and the `CHUNKS` Make variable are no longer supported. Pass a directory containing DOC, DOCX, PDF, or TXT files; JAR creates retrieval passages internally.

At startup, the indexer extracts the corpus and fingerprints its text and embedding configuration. Unchanged indexes are reused without loading a model. Changed corpora replace the index in one transaction; the previous committed corpus stays intact if indexing fails. Indexing is **not resumable** in this version: an interrupted new corpus must be indexed again. Use one indexer at a time and finish indexing before starting a chat session, especially when changing the embedding model.

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

New indexes include this metadata. See [metadata maintenance](setup.md#metadata-maintenance) for existing indexes.



## Attribution and license

Adapted from Kronk's `examples/rag/main.go` and `examples/rag/duck` at revision `50ace045`. The standalone application adds configurable models, recursive document extraction, safe database insertion, separate index/chat commands, and containerized remote storage. Adapted code is distributed under Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
