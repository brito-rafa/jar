package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

// Small interfaces let the existing Kronk implementation and HTTP providers share callers.
type Embedder interface {
	Embeddings(context.Context, model.D) (model.EmbedReponse, error)
	Unload(context.Context) error
}
type ChatModel interface {
	ChatStreaming(context.Context, model.D) (<-chan model.ChatResponse, error)
	Unload(context.Context) error
}

// HTTPError exposes structured diagnostics without echoing server text or document input.
type HTTPError struct {
	Status                      int
	Kind                        string
	ContextTokens, PromptTokens int
}

func (e *HTTPError) Error() string {
	detail := e.Kind
	if detail == "" {
		detail = "request rejected; inspect server logs"
	}
	if e.ContextTokens > 0 {
		return fmt.Sprintf("HTTP %d: %s (input tokens: %d; context limit: %d)", e.Status, detail, e.PromptTokens, e.ContextTokens)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, detail)
}

type Remote struct {
	base, name string
	client     *http.Client
	logger     *log.Logger
}

func NewRemote(endpoint, name string) (*Remote, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid remote model endpoint")
	}
	base := strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/v1")
	return &Remote{base: base, name: name, client: &http.Client{Timeout: 2 * time.Minute}}, nil
}
func (r *Remote) Unload(context.Context) error { r.client.CloseIdleConnections(); return nil }
func (r *Remote) request(ctx context.Context, path string, data any, out any) error {
	var body io.Reader
	if data != nil {
		b, e := json.Marshal(data)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	method := "GET"
	if data != nil {
		method = "POST"
	}
	target := r.base + "/v1/" + path
	if strings.HasPrefix(path, "/") {
		target = r.base + path
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := r.client.Do(req)
	if e != nil {
		return fmt.Errorf("remote %s request failed: %w", path, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Context int    `json:"n_ctx"`
				Prompt  int    `json:"n_prompt_tokens"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&payload)
		kind := payload.Error.Type
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(kind) {
			kind = ""
		}
		return fmt.Errorf("remote %s: %w", path, &HTTPError{Status: resp.StatusCode, Kind: kind, ContextTokens: payload.Error.Context, PromptTokens: payload.Error.Prompt})
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); e != nil {
		return fmt.Errorf("remote %s response: %w", path, e)
	}
	return nil
}
func (r *Remote) Discover(ctx context.Context) error {
	if r.name != "" {
		return nil
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e := r.request(ctx, "models", nil, &models); e != nil {
		return e
	}
	if len(models.Data) != 1 || models.Data[0].ID == "" {
		return fmt.Errorf("remote server must advertise exactly one model, or specify its model name")
	}
	r.name = models.Data[0].ID
	return nil
}
func (r *Remote) Embeddings(ctx context.Context, d model.D) (model.EmbedReponse, error) {
	var out model.EmbedReponse
	payload := model.D{"model": r.name, "input": d["input"]}
	err := r.request(ctx, "embeddings", payload, &out)
	var limit *HTTPError
	if err != nil && errors.As(err, &limit) && limit.Status == 400 && limit.Kind == "exceed_context_size_error" && limit.ContextTokens > 1 && d["truncate"] == true {
		var texts []string
		switch input := d["input"].(type) {
		case string:
			texts = []string{input}
		case []string:
			texts = input
		default:
			return out, err
		}
		tokenized := make([][]int, len(texts))
		truncated := 0
		for i, text := range texts {
			var result struct {
				Tokens []int `json:"tokens"`
			}
			if e := r.request(ctx, "/tokenize", model.D{"content": text, "add_special": true}, &result); e != nil {
				return out, fmt.Errorf("embedding token limit exceeded; server tokenizer unavailable: %w", e)
			}
			if len(result.Tokens) == 0 {
				return out, fmt.Errorf("server tokenizer returned no tokens")
			}
			tokens := result.Tokens
			if len(tokens) > limit.ContextTokens {
				// Retain the final special token as well as the beginning of the passage.
				tokens = append(append([]int(nil), tokens[:limit.ContextTokens-1]...), tokens[len(tokens)-1])
				truncated++
			}
			tokenized[i] = tokens
		}
		if truncated == 0 {
			return out, err
		}
		fmt.Printf("Embedding context limit: truncated %d input(s) to %d server tokens; original indexed text retained.\n", truncated, limit.ContextTokens)
		out = model.EmbedReponse{}
		err = r.request(ctx, "embeddings", model.D{"model": r.name, "input": tokenized}, &out)
	}
	if err != nil {
		return out, err
	}
	count := 1
	if texts, ok := d["input"].([]string); ok {
		count = len(texts)
	}
	if len(out.Data) != count {
		return out, fmt.Errorf("remote embedding count %d does not match input count %d", len(out.Data), count)
	}
	seen := make([]bool, count)
	dimensions := 0
	for _, entry := range out.Data {
		if entry.Index < 0 || entry.Index >= count || seen[entry.Index] || len(entry.Embedding) == 0 {
			return out, fmt.Errorf("invalid remote embedding entry")
		}
		if dimensions != 0 && dimensions != len(entry.Embedding) {
			return out, fmt.Errorf("inconsistent remote embedding dimensions")
		}
		seen[entry.Index] = true
		dimensions = len(entry.Embedding)
	}
	return out, nil
}
func (r *Remote) ChatStreaming(ctx context.Context, d model.D) (<-chan model.ChatResponse, error) {
	payload := model.D{}
	for k, v := range d {
		if k != "stream_options" {
			payload[k] = v
		}
	}
	payload["model"] = r.name
	payload["stream"] = false
	var raw json.RawMessage
	if e := r.request(ctx, "chat/completions", payload, &raw); e != nil {
		return nil, e
	}
	var response remoteChatResponse
	if e := json.Unmarshal(raw, &response); e != nil {
		return nil, fmt.Errorf("decode remote response metrics: %w", e)
	}
	r.logResponseMetrics(response)
	// Keep the original full response, including usage fields beyond our metrics.
	var out model.ChatResponse
	if e := json.Unmarshal(raw, &out); e != nil {
		return nil, fmt.Errorf("decode remote chat response: %w", e)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message == nil || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("remote chat returned no text")
	}
	for i := range out.Choices {
		out.Choices[i].Delta = out.Choices[i].Message
		if out.Choices[i].Delta == nil {
			return nil, fmt.Errorf("remote chat returned an invalid choice")
		}
	}
	ch := make(chan model.ChatResponse, 1)
	ch <- out
	close(ch)
	return ch, nil
}
func OpenEmbedding(ctx context.Context, source, endpoint, name string, workers int) (Embedder, error) {
	if endpoint != "" {
		r, e := NewRemote(endpoint, name)
		if e != nil {
			return nil, e
		}
		if e = r.Discover(ctx); e != nil {
			return nil, e
		}
		return r, nil
	}
	p, _, e := Install(source, "")
	if e != nil {
		return nil, e
	}
	return New(p, model.WithNSeqMax(workers))
}
func OpenChat(ctx context.Context, source, endpoint, name string) (ChatModel, error) {
	if endpoint != "" {
		r, e := NewRemote(endpoint, name)
		if e != nil {
			return nil, e
		}
		if e = r.Discover(ctx); e != nil {
			return nil, e
		}
		return r, nil
	}
	p, _, e := Install(source, "")
	if e != nil {
		return nil, e
	}
	return New(p)
}
