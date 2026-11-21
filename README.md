# JAR - Just Another RAG

JAR is a multilingual retrieval-augmented generation (RAG) application that recursively indexes **PDF (`.pdf`), Word (`.doc` and `.docx`), and plain-text (`.txt`) files**. It extracts text on the client, stores searchable passages and embeddings in DuckDB, and answers questions using retrieved evidence with source references.

**This repository runs inference through OpenAI-compatible HTTP endpoints.** Chat and embedding models run in services such as the included KServe deployment; JAR does not download or load models on the client. For local inference through Kronk and llama.cpp, including DuckDB Docker Compose setup, use the independent [local-jar repository](https://github.com/brito-rafa/local-jar). Both repositories retain the `cmd/jar-index`, `cmd/jar-chat`, and `pkg/` Go structure and compatible index formats.

See [setup.md](setup.md) for requirements, Kubernetes examples, endpoint configuration, index compatibility, and application commands.

## Quick start

With Go, a C compiler, the [document converters](setup.md#requirements), and running model/database services, create an ignored `.env` from `.env.example` for a new checkout and set:

```dotenv
CHAT_URL=https://chat.example.com
EMBEDDING_URL=https://embedding.example.com
DB_URL=quack:duckdb.example.com:9494
DB_TOKEN=<your-server-token>
```

These HTTP model APIs use `/v1/chat/completions`, `/v1/embeddings`, and model discovery through `/v1/models`. When an endpoint advertises several models, set `CHAT_MODEL` and/or `EMBEDDING_API_MODEL` to the desired API IDs. The current client does not send API-key authentication headers; use endpoints configured for this access model. See [model selection and index compatibility](setup.md#model-selection-and-index-compatibility).

Run from the repository root:

```sh
make index DOCS="/absolute/path/to/your/documents"
make chat-web
```

Replace `DOCS` with an existing document directory, or several directories separated by `;`. Wait for indexing to finish before starting chat. Browser chat opens at `http://127.0.0.1:18080`; use `make chat-terminal` for terminal chat. Later starts only need `make chat-web` to use the saved index. `make check-connectivity` checks configured services, and `make status` validates database access without modifying data.

For plain HTTP DuckDB over a trusted encrypted network, set `DB_DISABLE_SSL=true`. The [Kubernetes setup](setup.md#kubernetes-setup) includes KServe and DuckDB deployment examples; Docker Compose is maintained in [local-jar](https://github.com/brito-rafa/local-jar).

Existing installations should follow the [parameter migration](setup.md#parameter-migration) before running these commands. Credentials and database files are not moved automatically, and changing repositories alone does not require reindexing.

## Overview

Created by [brito-rafa](https://github.com/brito-rafa) with [OpenAI Codex](https://openai.com/codex/).

Based on the [Kronk RAG example](https://github.com/ardanlabs/kronk/blob/main/examples/rag/main.go) by [Ardan Labs](https://github.com/ardanlabs/kronk). This application uses OpenAI-compatible model services and a separate DuckDB service accessed through Quack over TCP. Kronk runtime support is maintained in [local-jar](https://github.com/brito-rafa/local-jar).

![JAR web interface showing the prompt, compact vector search, and a sourced answer](assets/images/web-interface.jpg)

*Example interface using synthetic demonstration text, not private documents.*

## Import behavior

- Recursively imports `.doc`, `.docx`, `.pdf`, and `.txt`, including uppercase extensions.
- Creates overlapping passages of up to 180 whitespace-separated words with 30 words of overlap. PDF pages are chunked separately; the embedding model's token limit still applies.
- Includes relative filenames and PDF page numbers in context; Word/TXT references use section numbers rather than rendered page numbers.
- Skips symlinks, hidden files/directories, Word lock files, and unsupported formats.
- Password-protected PDFs and documents with no extractable text are skipped with warnings. Scanned/image-only PDFs need OCR before indexing; LibreOffice does not perform OCR.
- Conversion failures stop indexing with the filename and a diagnostic. Legacy Word output containing NUL bytes is rejected or sent through converter fallbacks rather than indexed as binary text.

`-docs` is required for indexing; status, metadata maintenance, and reset do not read documents. Prepared `.chunks` files and the `CHUNKS` Make variable are no longer supported. Pass a directory containing DOC, DOCX, PDF, or TXT files; JAR creates retrieval passages internally.

At startup, the indexer extracts the corpus and fingerprints its text and embedding configuration. Unchanged indexes are reused without re-embedding the corpus. Changed corpora replace the index in one transaction; the previous committed corpus stays intact if indexing fails. Indexing is **not resumable** in this version: an interrupted new corpus must be indexed again. Use one indexer at a time and finish indexing before starting a chat session, especially when changing the embedding model.

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
