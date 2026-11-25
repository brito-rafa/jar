package mcpserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brito-rafa/jar/pkg/catalog"
	"github.com/brito-rafa/jar/pkg/duck"
	"github.com/brito-rafa/jar/pkg/inference/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type fakeEmbedder struct {
	vector []float32
	inputs []string
}

func (e *fakeEmbedder) Embeddings(_ context.Context, d model.D) (model.EmbedReponse, error) {
	e.inputs = append(e.inputs, d["input"].(string))
	return model.EmbedReponse{Data: []model.EmbedData{{Embedding: e.vector}}}, nil
}
func (e *fakeEmbedder) Unload(context.Context) error { return nil }

type fakeBackend struct {
	docs        []duck.Document
	sources     []catalog.Origin
	text        string
	paths       map[int][]string
	lastLimit   int
	lastFilter  duck.SourceFilter
	lastPaths   []string
	lastKind    string
	lastQuery   string
	searchCalls int
}

func (b *fakeBackend) Status() (duck.DatabaseStatus, error) {
	return duck.DatabaseStatus{Ready: true, Chunks: 42, EmbeddingModel: "synthetic-embedding", Dimensions: 3, IndexedAt: "2026-01-02 03:04:05"}, nil
}
func (b *fakeBackend) Search(vector []float32, limit int, filter duck.SourceFilter) ([]duck.Document, error) {
	b.searchCalls++
	b.lastLimit, b.lastFilter = limit, filter
	return b.docs, nil
}
func (b *fakeBackend) BrowseSources(kind, query string) ([]catalog.Origin, error) {
	if kind != "books" && kind != "conferences" {
		return nil, fmt.Errorf("use books or conferences")
	}
	b.lastKind, b.lastQuery = kind, query
	return b.sources, nil
}
func (b *fakeBackend) RetrieveConferences(filter duck.SourceFilter) (string, error) {
	if err := filter.Validate(); err != nil || filter.Date == "" {
		return "", fmt.Errorf("a conference date is required")
	}
	return b.text, nil
}
func (b *fakeBackend) ChunkSourcePaths(chunkID int) ([]string, error) {
	paths, ok := b.paths[chunkID]
	if !ok {
		return nil, fmt.Errorf("chunk %d is not in the index", chunkID)
	}
	return paths, nil
}
func (b *fakeBackend) RetrieveReferenceContext(paths []string, chunkID int) (string, error) {
	b.lastPaths = paths
	return b.text, nil
}

func connect(t *testing.T, backend Backend, embedder *fakeEmbedder) *mcp.ClientSession {
	t.Helper()
	server := newServer(backend, embedder, 3, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var parts []string
	for _, content := range result.Content {
		parts = append(parts, content.(*mcp.TextContent).Text)
	}
	return strings.Join(parts, "\n"), result.IsError
}

func TestToolsAreReadOnlyAndExposeNoIndexMutation(t *testing.T) {
	session := connect(t, &fakeBackend{}, &fakeEmbedder{vector: []float32{1, 0, 0}})
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be annotated read-only", tool.Name)
		}
		if tool.OutputSchema != nil {
			t.Errorf("%s should return compact text only, not duplicated structured output", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"get_conference_text", "get_passage_context", "index_status", "list_sources", "search_passages"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestSearchReturnsTraceableSourcesAndBoundsLimit(t *testing.T) {
	backend := &fakeBackend{docs: []duck.Document{{
		ID: 7, Similarity: 0.87654, Text: "Source: library/Example Book.pdf | page 3\nSynthetic passage text.",
		Origins: []catalog.Origin{catalog.Parse("library/Example Book.pdf | page 3"), catalog.Parse("archive/580508EN.txt | section 2")},
	}, {
		ID: 9, Similarity: 0.5, Text: "Unlabelled synthetic text.",
	}}}
	embedder := &fakeEmbedder{vector: []float32{1, 0, 0}}
	session := connect(t, backend, embedder)
	out, isError := call(t, session, "search_passages", map[string]any{"query": "  synthetic question  ", "limit": 500, "date": "1958"})
	if isError {
		t.Fatal(out)
	}
	if backend.lastLimit != maxSearchLimit || backend.lastFilter.Date != "1958" {
		t.Fatalf("limit=%d filter=%+v", backend.lastLimit, backend.lastFilter)
	}
	if embedder.inputs[0] != "synthetic question" {
		t.Fatalf("embedded %q", embedder.inputs[0])
	}
	for _, want := range []string{
		"[chunk 7] similarity 0.8765",
		"Source: library/Example Book.pdf | page 3 [filename title: Example Book",
		"Source: archive/580508EN.txt | section 2 [filename title: 580508EN; type: conference; filename date: 1958-05-08",
		"Synthetic passage text.",
		"[chunk 9] similarity 0.5000\nSource: source filename unavailable\nUnlabelled synthetic text.",
		"not verified bibliographic metadata",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("search output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Source: library/Example Book.pdf | page 3\nSynthetic") {
		t.Error("the indexed Source header should be replaced by catalog descriptions")
	}
	_, _ = call(t, session, "search_passages", map[string]any{"query": "default"})
	if backend.lastLimit != defaultSearchLimit {
		t.Fatalf("default limit = %d", backend.lastLimit)
	}
}

func TestSearchRejectsInvalidInputBeforeQueryingIndex(t *testing.T) {
	backend := &fakeBackend{}
	session := connect(t, backend, &fakeEmbedder{vector: []float32{1, 0}})
	for _, args := range []map[string]any{
		{"query": "   "},
		{"query": "x", "book": "title", "date": "1958"},
		{"query": "x", "date": "58-05"},
		{"query": "dimension mismatch"},
	} {
		out, isError := call(t, session, "search_passages", args)
		if !isError {
			t.Errorf("%v: expected a tool error, got %s", args, out)
		}
	}
	if backend.searchCalls != 0 {
		t.Fatalf("index queried %d times for invalid input", backend.searchCalls)
	}
}

func TestListSourcesPagesAndNormalizesKind(t *testing.T) {
	var sources []catalog.Origin
	for i := range 3 {
		sources = append(sources, catalog.Parse(fmt.Sprintf("talks/58050%dEN.txt", i+1)))
	}
	backend := &fakeBackend{sources: sources}
	session := connect(t, backend, &fakeEmbedder{vector: []float32{1, 0, 0}})
	out, isError := call(t, session, "list_sources", map[string]any{"kind": "conferences", "query": " 1958 ", "limit": 2})
	if isError {
		t.Fatal(out)
	}
	if backend.lastKind != "conferences" || backend.lastQuery != "1958" {
		t.Fatalf("kind=%q query=%q", backend.lastKind, backend.lastQuery)
	}
	for _, want := range []string{"3 matching conferences", "talks/580501EN.txt", "filename date: 1958-05-02", "offset=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "580503EN") {
		t.Error("limit was not applied")
	}
	out, _ = call(t, session, "list_sources", map[string]any{"kind": "conferences", "offset": 2})
	if !strings.Contains(out, "580503EN") || strings.Contains(out, "offset=") {
		t.Errorf("second page:\n%s", out)
	}
	if out, isError := call(t, session, "list_sources", map[string]any{"kind": "videos"}); !isError {
		t.Errorf("schema should reject an unknown kind: %s", out)
	}
}

func TestPassageContextUsesEveryIndexedPathAndPages(t *testing.T) {
	long := strings.Repeat("palavra ", 2000) // 16000 characters, including accented-free ASCII
	backend := &fakeBackend{text: long, paths: map[int][]string{11: {"a/book.pdf", "b/copy.pdf"}}}
	session := connect(t, backend, &fakeEmbedder{vector: []float32{1, 0, 0}})
	out, isError := call(t, session, "get_passage_context", map[string]any{"chunk_id": 11, "max_chars": 1000})
	if isError {
		t.Fatal(out)
	}
	if !slices.Equal(backend.lastPaths, []string{"a/book.pdf", "b/copy.pdf"}) {
		t.Fatalf("paths = %v", backend.lastPaths)
	}
	if !strings.Contains(out, "Characters 0-1000 of 16000") || !strings.Contains(out, "offset=1000") {
		t.Fatalf("unexpected first page header/footer:\n%.200s", out)
	}
	out, _ = call(t, session, "get_passage_context", map[string]any{"chunk_id": 11, "offset": 15500, "max_chars": 1000})
	if !strings.Contains(out, "[End of text.]") {
		t.Errorf("last page:\n%s", out)
	}
	if out, isError := call(t, session, "get_passage_context", map[string]any{"chunk_id": 404}); !isError {
		t.Errorf("unknown chunk should be a tool error: %s", out)
	}
}

func TestPagedWindowsCoverTextExactlyOnce(t *testing.T) {
	body := strings.Repeat("ação e reação ", 300) + "fim"
	var rebuilt strings.Builder
	offset := 0
	for range 100 {
		result, _, err := paged("H.", body, offset, 700)
		if err != nil {
			t.Fatal(err)
		}
		page := result.Content[0].(*mcp.TextContent).Text
		_, chunk, _ := strings.Cut(page, "\n\n")
		next := -1
		if i := strings.LastIndex(chunk, "\n\n[More text follows: call again with offset="); i >= 0 {
			fmt.Sscanf(chunk[i:], "\n\n[More text follows: call again with offset=%d.]", &next)
			chunk = chunk[:i]
		} else {
			chunk = strings.TrimSuffix(chunk, "\n\n[End of text.]")
		}
		rebuilt.WriteString(chunk)
		if next < 0 {
			break
		}
		if next <= offset {
			t.Fatalf("offset did not advance: %d -> %d", offset, next)
		}
		offset = next
	}
	if rebuilt.String() != body {
		t.Fatalf("pages lost or duplicated text: got %d runes, want %d", utf8.RuneCountInString(rebuilt.String()), utf8.RuneCountInString(body))
	}
	if _, _, err := paged("H.", "abc", 3, 0); err == nil {
		t.Error("offset at the end should be rejected")
	}
	if _, _, err := paged("H.", "abc", -1, 0); err == nil {
		t.Error("negative offset should be rejected")
	}
}

func TestConferenceTextAndStatus(t *testing.T) {
	backend := &fakeBackend{text: "Conference text for filename date 1958-05 (inferred). Synthetic talk."}
	session := connect(t, backend, &fakeEmbedder{vector: []float32{1, 0, 0}})
	out, isError := call(t, session, "get_conference_text", map[string]any{"date": "1958-05"})
	if isError || !strings.Contains(out, "Complete text") || !strings.Contains(out, "Synthetic talk.") {
		t.Fatalf("conference output (error=%v):\n%s", isError, out)
	}
	if out, isError := call(t, session, "get_conference_text", map[string]any{"date": "May 1958"}); !isError {
		t.Errorf("invalid date should be a tool error: %s", out)
	}
	out, isError = call(t, session, "index_status", nil)
	if isError || !strings.Contains(out, "Passages: 42") || !strings.Contains(out, "Dimensions: 3") {
		t.Fatalf("status output:\n%s", out)
	}
}

func TestStreamableHTTPHandlerIsStatelessAndRejectsCrossOriginBrowsers(t *testing.T) {
	server := newServer(&fakeBackend{}, &fakeEmbedder{vector: []float32{1, 0, 0}}, 3, "test")
	httpServer := httptest.NewServer(Handler(server, "/mcp"))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	out, isError := call(t, session, "index_status", nil)
	if isError || !strings.Contains(out, "Index ready.") {
		t.Fatalf("index_status over HTTP:\n%s", out)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"browser","version":"0"}}}`
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin browser request status = %d, want 403", response.StatusCode)
	}
	response, err = http.Get(httpServer.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path status = %d", response.StatusCode)
	}
}
