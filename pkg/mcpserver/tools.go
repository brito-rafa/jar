package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/brito-rafa/jar/pkg/inference"
	"github.com/brito-rafa/jar/pkg/inference/model"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultSearchLimit = 5
	maxSearchLimit     = 30 // the same vector top-k that chat retrieves
	defaultListLimit   = 50
	maxListLimit       = 200
	defaultMaxChars    = 6000
	minMaxChars        = 500
	maxMaxChars        = 20000
	embeddingTimeout   = 2 * time.Minute
	filenameNote       = "Titles, types, and dates come from filenames; they are not verified bibliographic metadata."
)

const instructions = `Read-only access to the JAR document index.
Call search_passages before answering a question about the documents, and cite the chunk ids and source locations it returns.
Use get_passage_context to read around a cited chunk and get_conference_text for a whole conference date.
` + filenameNote + ` Indexed text keeps its original language.`

// Backend is the read-only subset of the duck package that the tools use.
type Backend interface {
	Status() (duck.DatabaseStatus, error)
	Search(vector []float32, limit int, filter duck.SourceFilter) ([]duck.Document, error)
	BrowseSources(kind, query string) ([]catalog.Origin, error)
	RetrieveConferences(filter duck.SourceFilter) (string, error)
	ChunkSourcePaths(chunkID int) ([]string, error)
	RetrieveReferenceContext(paths []string, chunkID int) (string, error)
}

type duckBackend struct{ db *sql.DB }

func (b duckBackend) Status() (duck.DatabaseStatus, error) { return duck.Status(b.db) }
func (b duckBackend) Search(vector []float32, limit int, filter duck.SourceFilter) ([]duck.Document, error) {
	return duck.Search(b.db, vector, limit, filter)
}
func (b duckBackend) BrowseSources(kind, query string) ([]catalog.Origin, error) {
	return duck.BrowseSources(b.db, kind, query)
}
func (b duckBackend) RetrieveConferences(filter duck.SourceFilter) (string, error) {
	return duck.RetrieveConferences(b.db, filter)
}
func (b duckBackend) ChunkSourcePaths(chunkID int) ([]string, error) {
	return duck.ChunkSourcePaths(b.db, chunkID)
}
func (b duckBackend) RetrieveReferenceContext(paths []string, chunkID int) (string, error) {
	return duck.RetrieveReferenceContext(b.db, paths, chunkID)
}

type tools struct {
	backend    Backend
	embedder   inference.Embedder
	dimensions int
}

type statusInput struct{}

type searchInput struct {
	Query string `json:"query" jsonschema:"The question or topic to search for, in any language."`
	Limit int    `json:"limit,omitempty" jsonschema:"Number of passages to return, 1-30. Default 5."`
	Book  string `json:"book,omitempty" jsonschema:"Optional: only search sources whose filename or title contains this text. Do not combine with date."`
	Date  string `json:"date,omitempty" jsonschema:"Optional: only search sources whose filename date starts with YYYY, YYYY-MM, or YYYY-MM-DD. Do not combine with book."`
}

type listInput struct {
	Kind   string `json:"kind" jsonschema:"books (all non-conference documents) or conferences."`
	Query  string `json:"query,omitempty" jsonschema:"Optional title or filename fragment; for conferences also a date prefix such as 1958 or 1958-05."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Number of source files to return, 1-200. Default 50."`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of matching source files to skip, for paging. Default 0."`
}

type contextInput struct {
	ChunkID  int `json:"chunk_id" jsonschema:"A chunk id returned by search_passages."`
	Offset   int `json:"offset,omitempty" jsonschema:"Character offset to continue from, as given by the previous call. Default 0."`
	MaxChars int `json:"max_chars,omitempty" jsonschema:"Maximum characters to return, 500-20000. Default 6000."`
}

type conferenceInput struct {
	Date     string `json:"date" jsonschema:"Filename date: YYYY, YYYY-MM, or YYYY-MM-DD."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Character offset to continue from, as given by the previous call. Default 0."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum characters to return, 500-20000. Default 6000."`
}

func newServer(backend Backend, embedder inference.Embedder, dimensions int, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "jar-mcp", Title: "JAR document index", Version: version},
		&mcp.ServerOptions{Instructions: instructions})
	t := &tools{backend: backend, embedder: embedder, dimensions: dimensions}
	readOnly := func(title string) *mcp.ToolAnnotations {
		closed := false
		return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
	}
	listSchema, err := jsonschema.For[listInput](nil)
	if err != nil {
		panic(err)
	}
	listSchema.Properties["kind"].Enum = []any{"books", "conferences"}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_status",
		Description: "Report whether the document index is ready, how many passages it holds, and which embedding model built it.",
		Annotations: readOnly("Index status"),
	}, logged("index_status", t.status))
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_passages",
		Description: "Semantic search over the indexed documents. Returns the most similar passages with chunk ids, similarity scores, " +
			"source locations, and original text (all languages). Optionally restrict to one book/file or a filename date.",
		Annotations: readOnly("Search passages"),
	}, logged("search_passages", t.search))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sources",
		Description: "List distinct indexed source files (books/documents or conferences), optionally filtered by a title, filename, or date fragment.",
		InputSchema: listSchema,
		Annotations: readOnly("List sources"),
	}, logged("list_sources", t.list))
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_passage_context",
		Description: "Read the context around a chunk returned by search_passages: the whole conference, the explicit chapter of a book, " +
			"or the cited passage with neighbouring paragraphs when chapter boundaries are unclear. Long text is paged.",
		Annotations: readOnly("Passage context"),
	}, logged("get_passage_context", t.passageContext))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_conference_text",
		Description: "Read the full indexed text of every conference whose filename date starts with the given date. Long text is paged.",
		Annotations: readOnly("Conference text"),
	}, logged("get_conference_text", t.conference))
	return server
}

// logged records the tool name, duration, and outcome, never arguments or document text.
func logged[In any](name string, handler mcp.ToolHandlerFor[In, any]) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		result, out, err := handler(ctx, request, input)
		if err != nil {
			log.Printf("tool=%s duration=%s error=%q", name, time.Since(start).Round(time.Millisecond), err)
		} else {
			log.Printf("tool=%s duration=%s ok", name, time.Since(start).Round(time.Millisecond))
		}
		return result, out, err
	}
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func (t *tools) status(context.Context, *mcp.CallToolRequest, statusInput) (*mcp.CallToolResult, any, error) {
	s, err := t.backend.Status()
	if err != nil {
		return nil, nil, err
	}
	if !s.Ready {
		return text("Index not ready: no completed JAR index in the database."), nil, nil
	}
	return text(fmt.Sprintf("Index ready.\nPassages: %d\nEmbedding model: %s\nDimensions: %d\nIndexed at: %s",
		s.Chunks, s.EmbeddingModel, s.Dimensions, s.IndexedAt)), nil, nil
}

func (t *tools) search(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return nil, nil, fmt.Errorf("query must not be empty")
	}
	filter := duck.SourceFilter{Book: strings.TrimSpace(in.Book), Date: strings.TrimSpace(in.Date)}
	if err := filter.Validate(); err != nil {
		return nil, nil, err
	}
	limit := clamp(in.Limit, defaultSearchLimit, 1, maxSearchLimit)
	ctx, cancel := context.WithTimeout(ctx, embeddingTimeout)
	defer cancel()
	resp, err := t.embedder.Embeddings(ctx, model.D{"input": query, "truncate": true})
	if err != nil {
		return nil, nil, fmt.Errorf("embed query: %w", err)
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, nil, fmt.Errorf("embedding service returned an empty query vector")
	}
	vector := resp.Data[0].Embedding
	if t.dimensions > 0 && len(vector) != t.dimensions {
		return nil, nil, fmt.Errorf("embedding service returned %d dimensions but the index uses %d; check EMBEDDING_URL/EMBEDDING_API_MODEL or restart jar-mcp after reindexing", len(vector), t.dimensions)
	}
	docs, err := t.backend.Search(vector, limit, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("search index: %w", err)
	}
	return text(renderSearch(docs, filter)), nil, nil
}

func renderSearch(docs []duck.Document, filter duck.SourceFilter) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Search scope: %s. %d passage(s), most similar first. %s\n", filter.Description(), len(docs), filenameNote)
	if len(docs) == 0 {
		b.WriteString("\nNo indexed passages matched.")
		return b.String()
	}
	for _, doc := range docs {
		body := doc.Text
		if first, rest, found := strings.Cut(body, "\n"); strings.HasPrefix(first, "Source: ") {
			body = ""
			if found {
				body = rest
			}
		}
		fmt.Fprintf(&b, "\n[chunk %d] similarity %.4f\n", doc.ID, doc.Similarity)
		for _, source := range documentSources(doc) {
			fmt.Fprintf(&b, "Source: %s\n", source)
		}
		b.WriteString(strings.TrimSpace(body))
		b.WriteByte('\n')
	}
	return b.String()
}

// documentSources describes every retrieved source location; it never invents one.
func documentSources(doc duck.Document) []string {
	var result []string
	for _, o := range doc.Origins {
		result = append(result, o.Description())
	}
	if len(result) == 0 {
		for _, label := range doc.Sources {
			result = append(result, catalog.Parse(label).Description())
		}
	}
	if len(result) == 0 {
		if first, _, _ := strings.Cut(doc.Text, "\n"); strings.HasPrefix(first, "Source: ") {
			result = append(result, catalog.Parse(strings.TrimPrefix(first, "Source: ")).Description())
		}
	}
	if len(result) == 0 {
		result = append(result, "source filename unavailable")
	}
	return result
}

func (t *tools) list(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, any, error) {
	kind := in.Kind // the input schema restricts it to books or conferences
	if in.Offset < 0 {
		return nil, nil, fmt.Errorf("offset must not be negative")
	}
	limit := clamp(in.Limit, defaultListLimit, 1, maxListLimit)
	sources, err := t.backend.BrowseSources(kind, strings.TrimSpace(in.Query))
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d matching %s source file(s). %s\n", len(sources), kind, filenameNote)
	if in.Offset >= len(sources) {
		if len(sources) > 0 {
			fmt.Fprintf(&b, "Offset %d is past the end of the list.", in.Offset)
		}
		return text(b.String()), nil, nil
	}
	end := min(in.Offset+limit, len(sources))
	for _, o := range sources[in.Offset:end] {
		fmt.Fprintf(&b, "- %s (title: %s; type: %s", o.Path, o.Title, o.Kind)
		if o.Date != "" {
			fmt.Fprintf(&b, "; filename date: %s, %s precision", o.Date, o.Precision)
		}
		b.WriteString(")\n")
	}
	if end < len(sources) {
		fmt.Fprintf(&b, "Showing %d-%d. More files follow: call again with offset=%d.", in.Offset+1, end, end)
	}
	return text(b.String()), nil, nil
}

func (t *tools) passageContext(_ context.Context, _ *mcp.CallToolRequest, in contextInput) (*mcp.CallToolResult, any, error) {
	paths, err := t.backend.ChunkSourcePaths(in.ChunkID)
	if err != nil {
		return nil, nil, err
	}
	body, err := t.backend.RetrieveReferenceContext(paths, in.ChunkID)
	if err != nil {
		return nil, nil, err
	}
	header := fmt.Sprintf("Context for chunk %d from %s.", in.ChunkID, strings.Join(paths, "; "))
	return paged(header, body, in.Offset, in.MaxChars)
}

func (t *tools) conference(_ context.Context, _ *mcp.CallToolRequest, in conferenceInput) (*mcp.CallToolResult, any, error) {
	date := strings.TrimSpace(in.Date)
	body, err := t.backend.RetrieveConferences(duck.SourceFilter{Date: date})
	if err != nil {
		return nil, nil, err
	}
	return paged("Conference text for filename date "+date+".", body, in.Offset, in.MaxChars)
}

// paged returns one window of a long text, in characters, with explicit
// continuation instructions. It prefers to end the window at whitespace.
func paged(header, body string, offset, maxChars int) (*mcp.CallToolResult, any, error) {
	if offset < 0 {
		return nil, nil, fmt.Errorf("offset must not be negative")
	}
	size := clamp(maxChars, defaultMaxChars, minMaxChars, maxMaxChars)
	runes := []rune(body)
	total := len(runes)
	if offset > total || (offset == total && total > 0) {
		return nil, nil, fmt.Errorf("offset %d is past the end of the text (%d characters)", offset, total)
	}
	end := min(offset+size, total)
	if end < total {
		for i := end; i > offset+size*9/10; i-- {
			if unicode.IsSpace(runes[i-1]) {
				end = i
				break
			}
		}
	}
	var b strings.Builder
	b.WriteString(header)
	if offset == 0 && end == total {
		fmt.Fprintf(&b, " Complete text: %d characters.\n\n", total)
	} else {
		fmt.Fprintf(&b, " Characters %d-%d of %d.\n\n", offset, end, total)
	}
	b.WriteString(string(runes[offset:end]))
	if end < total {
		fmt.Fprintf(&b, "\n\n[More text follows: call again with offset=%d.]", end)
	} else if offset > 0 {
		b.WriteString("\n\n[End of text.]")
	}
	return text(b.String()), nil, nil
}

func clamp(value, fallback, low, high int) int {
	if value == 0 {
		return fallback
	}
	return max(low, min(value, high))
}
