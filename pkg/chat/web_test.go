package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserRoutes(t *testing.T) {
	server := httptest.NewServer(webHandler(fixtureSession()))
	defer server.Close()
	for _, path := range []string{"/", "/app.js", "/style.css", "/api/info", "/api/complete?line=%2Fhe"} {
		r, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(body) == 0 || r.Header.Get("Content-Security-Policy") == "" {
			t.Fatal(path, r.StatusCode, string(body))
		}
	}
	post := func(body string) chatReply {
		t.Helper()
		r, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			b, _ := io.ReadAll(r.Body)
			t.Fatal(r.StatusCode, string(b))
		}
		var reply chatReply
		if err := json.NewDecoder(r.Body).Decode(&reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if r := post(`{"message":"What does a journal help with?"}`); len(r.References) != 1 {
		t.Fatal(r)
	}
	if r := post(`{"message":"/retrieveref 1"}`); !strings.Contains(r.Text, "exact indexed passage") {
		t.Fatal(r)
	}
	if r := post(`{"message":"/retrieve 580508"}`); r.DownloadName != "conference-1958-05-08.txt" || !strings.Contains(r.Text, "1958-05-08") {
		t.Fatal(r)
	}
	if r := post(`{"message":"/reset"}`); len(r.References) != 0 {
		t.Fatal(r)
	}
	if r := post(`{"message":"/quit"}`); !r.Quit {
		t.Fatal(r)
	}
	for _, body := range []string{`{`, `{"message":""}`, `{"message":"x"} {}`, `{"message":"` + strings.Repeat("x", 70<<10) + `"}`} {
		r, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal(r.StatusCode)
		}
	}
	for _, tc := range []struct{ host, origin string }{{"evil.example", ""}, {"localhost", "https://evil.example"}, {"localhost", "null"}} {
		req := httptest.NewRequest("POST", "http://localhost/api/chat", strings.NewReader(`{"message":"/reset"}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		webHandler(fixtureSession()).ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal(tc, w.Code)
		}
	}
}

type serialBackend struct{ active, max atomic.Int32 }

func (b *serialBackend) handle(ctx context.Context, _ string) (chatReply, error) {
	n := b.active.Add(1)
	defer b.active.Add(-1)
	for old := b.max.Load(); n > old; old = b.max.Load() {
		if b.max.CompareAndSwap(old, n) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	return chatReply{Text: "ok"}, nil
}
func (*serialBackend) complete(string) []string { return nil }
func TestBrowserSerializesModelRequests(t *testing.T) {
	backend := &serialBackend{}
	server := httptest.NewServer(webHandler(backend))
	defer server.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r, err := http.Post(server.URL+"/api/chat", "application/json", strings.NewReader(`{"message":"hi"}`))
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		})
	}
	wg.Wait()
	if backend.max.Load() != 1 {
		t.Fatal("overlapping model access", backend.max.Load())
	}
}

// Browser QA fixture: no documents, database, or model are accessed.
func TestWebPreview(t *testing.T) {
	if os.Getenv("JAR_WEB_PREVIEW") != "1" {
		t.Skip("opt-in browser preview")
	}
	done := make(chan struct{})
	var once sync.Once
	session := fixtureSession()
	if os.Getenv("JAR_WEB_GROUNDING") == "1" {
		attempts := 0
		session.generate = func(ctx context.Context, _ []model.D) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
			}
			attempts++
			if attempts == 1 {
				return "Pessoa Inventada recommends reflection [1].", nil
			}
			return "A journal helps us reflect on decisions [1].", nil
		}
	}
	app := webHandler(session)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/stop" && r.Method == "POST" {
			once.Do(func() { close(done) })
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	fmt.Println("PREVIEW_URL=" + server.URL)
	select {
	case <-done:
	case <-time.After(180 * time.Second):
	}
}
