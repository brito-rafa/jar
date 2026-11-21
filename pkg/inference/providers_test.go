package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brito-rafa/jar/pkg/inference/model"
)

func TestRemoteProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			ioJSON(w, `{"data":[{"id":"fixture-model"}]}`)
		case "/v1/embeddings":
			var d model.D
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				t.Error(err)
			}
			if d["model"] != "fixture-model" {
				t.Errorf("wrong model: %v", d["model"])
			}
			ioJSON(w, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`)
		case "/v1/chat/completions":
			var d model.D
			_ = json.NewDecoder(r.Body).Decode(&d)
			if d["stream"] != false || d["model"] != "fixture-model" {
				t.Errorf("invalid request: %v", d)
			}
			if _, ok := d["stream_options"]; ok {
				t.Error("non-streaming request includes stream_options")
			}
			ioJSON(w, `{"choices":[{"message":{"content":"fixture answer"},"finish_reason":"length"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	r, err := NewRemote(server.URL+"/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Unload(context.Background())
	if err = r.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	vectors, err := r.Embeddings(context.Background(), model.D{"input": []string{"first", "second"}})
	if err != nil || len(vectors.Data) != 2 || vectors.Data[0].Index != 1 {
		t.Fatalf("vectors: %+v, %v", vectors, err)
	}
	ch, err := r.ChatStreaming(context.Background(), model.D{"messages": []model.D{model.TextMessage("user", "question")}, "stream_options": model.D{}})
	if err != nil {
		t.Fatal(err)
	}
	response := <-ch
	if response.Choices[0].Delta.Content != "fixture answer" || response.Choices[0].FinishReason() != model.FinishReasonLength {
		t.Fatal("lost content or finish reason")
	}
}
func ioJSON(w http.ResponseWriter, s string) { _, _ = w.Write([]byte(s)) }
func TestRemoteFailures(t *testing.T) {
	for _, body := range []string{`{"choices":[]}`, `{"choices":[{"message":{"content":""}}]}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ioJSON(w, body) }))
			defer server.Close()
			r, _ := NewRemote(server.URL, "fixture")
			defer r.Unload(context.Background())
			if _, err := r.ChatStreaming(context.Background(), model.D{}); err == nil {
				t.Fatal("expected response error")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	r, _ := NewRemote(server.URL, "fixture")
	defer r.Unload(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.Embeddings(ctx, model.D{}); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestRemoteEmbeddingContextRecovery(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tokenize":
			ioJSON(w, `{"tokens":[1,2,3,4,5,6,7,8,9,10]}`)
		case "/v1/embeddings":
			calls++
			if calls == 1 {
				w.WriteHeader(400)
				ioJSON(w, `{"error":{"type":"exceed_context_size_error","n_ctx":8,"n_prompt_tokens":10,"message":"private text must not appear"}}`)
				return
			}
			var d struct {
				Input [][]int `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				t.Error(err)
			}
			if len(d.Input) != 1 || len(d.Input[0]) != 8 || d.Input[0][7] != 10 {
				t.Errorf("invalid truncation: %v", d.Input)
			}
			ioJSON(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	remote, _ := NewRemote(server.URL, "fixture")
	defer remote.Unload(context.Background())
	result, err := remote.Embeddings(context.Background(), model.D{"input": []string{"synthetic passage"}, "truncate": true})
	if err != nil || len(result.Data) != 1 || calls != 2 {
		t.Fatalf("result %+v, calls %d, err %v", result, calls, err)
	}
}

func TestRemoteHTTPErrorDoesNotEchoServerMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		ioJSON(w, `{"error":{"type":"invalid_request_error","message":"private content and token must not appear"}}`)
	}))
	defer server.Close()
	remote, _ := NewRemote(server.URL, "fixture")
	defer remote.Unload(context.Background())
	_, err := remote.Embeddings(context.Background(), model.D{"input": "fixture", "truncate": true})
	if err == nil || !strings.Contains(err.Error(), "invalid_request_error") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}

func TestOpenProvidersRequireEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", " \t "} {
		if _, err := OpenEmbedding(context.Background(), endpoint, "fixture"); err == nil || !strings.Contains(err.Error(), "endpoint is required") {
			t.Fatalf("embedding endpoint %q: %v", endpoint, err)
		}
		if _, err := OpenChat(context.Background(), endpoint, "fixture"); err == nil || !strings.Contains(err.Error(), "endpoint is required") {
			t.Fatalf("chat endpoint %q: %v", endpoint, err)
		}
	}
}

func TestOpenProvidersDiscoverOrUseExplicitModel(t *testing.T) {
	for _, configured := range []string{"", "explicit-model"} {
		t.Run(configured, func(t *testing.T) {
			discoveryCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				discoveryCalls++
				ioJSON(w, `{"data":[{"id":"discovered-model"}]}`)
			}))
			defer server.Close()
			embed, err := OpenEmbedding(context.Background(), server.URL, configured)
			if err != nil {
				t.Fatal(err)
			}
			defer embed.Unload(context.Background())
			chat, err := OpenChat(context.Background(), server.URL, configured)
			if err != nil {
				t.Fatal(err)
			}
			defer chat.Unload(context.Background())
			wantName, wantCalls := configured, 0
			if configured == "" {
				wantName, wantCalls = "discovered-model", 2
			}
			if embed.(*Remote).name != wantName || chat.(*Remote).name != wantName || discoveryCalls != wantCalls {
				t.Fatalf("models=%q/%q discovery calls=%d", embed.(*Remote).name, chat.(*Remote).name, discoveryCalls)
			}
		})
	}
}

func TestRemoteProtocolPreservesCompletionDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ioJSON(w, `{"id":"fixture","model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"fixture answer","reasoning_content":"fixture reasoning"},"finish_reason":"length"}],"usage":{"prompt_tokens":8,"completion_tokens":5,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3},"tokens_per_second":2.5,"time_to_first_token_ms":40}}`)
	}))
	defer server.Close()
	remote, err := NewRemote(server.URL, "fixture-model")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Unload(context.Background())
	ch, err := remote.ChatStreaming(context.Background(), model.D{"messages": []model.D{model.TextMessage("user", "fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	response := <-ch
	choice := response.Choices[0]
	if choice.FinishReason() != model.FinishReasonLength || choice.Message.Content != "fixture answer" || choice.Delta.Reasoning != "fixture reasoning" {
		t.Fatalf("completion details lost: %+v", choice)
	}
	usage := response.Usage
	if usage == nil || usage.TotalTokens != 13 || usage.PromptTokensDetails.CachedTokens != 2 || usage.CompletionTokensDetails.ReasoningTokens != 3 || usage.TokensPerSecond != 2.5 || usage.TimeToFirstTokenMS != 40 {
		t.Fatalf("usage details lost: %+v", usage)
	}
}
