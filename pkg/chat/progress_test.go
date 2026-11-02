package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

func TestWebStreamsRetrievalBeforeAnswer(t *testing.T) {
	s := fixtureSession()
	release := make(chan struct{})
	defer close(release)
	s.generate = func(ctx context.Context, _ []model.D) (string, error) {
		select {
		case <-release:
			return "A journal helps us reflect [1].", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	server := httptest.NewServer(webHandler(s))
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", server.URL+"/api/chat", strings.NewReader(`{"message":"What helps reflection?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Fatal(response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		var event struct {
			Type     string       `json:"type"`
			Progress chatProgress `json:"progress"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "result" {
			t.Fatal("answer arrived before retrieval")
		}
		if event.Progress.Stage == "retrieved" {
			if len(event.Progress.Results) != 1 || event.Progress.Results[0].ChunkID != 99 || !strings.Contains(event.Progress.Results[0].Source, "Reflection.pdf") {
				t.Fatal(event)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("retrieval not flushed before generation", scanner.Err())
	}
	// Releasing generation is deliberately deferred: retrieval was received while
	// the model was still blocked, not just bundled with the final response.
	response.Body.Close()
}
func TestStreamedCommandReturnsResult(t *testing.T) {
	server := httptest.NewServer(webHandler(fixtureSession()))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/api/chat", strings.NewReader(`{"message":"/help"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `"type":"result"`) || strings.Contains(string(body), `"type":"progress"`) {
		t.Fatal(string(body))
	}
}

func TestStreamKeepsResultsAndReferencesWhenNameFlagged(t *testing.T) {
	session := fixtureSession()
	session.generate = func(context.Context, []model.D) (string, error) {
		return "Pessoa Inventada recommends reflection [1].", nil
	}
	server := httptest.NewServer(webHandler(session))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/api/chat", strings.NewReader(`{"message":"What helps reflection?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	retrieved, answered := false, false
	for scanner.Scan() {
		var event struct {
			Type     string       `json:"type"`
			Progress chatProgress `json:"progress"`
			Result   chatReply    `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Progress.Stage == "retrieved" {
			retrieved = len(event.Progress.Results) == 1
		}
		if event.Type == "result" {
			answered = true
			if !retrieved || !strings.Contains(event.Result.Text, "Pessoa Inventada") || len(event.Result.Warnings) != 1 || len(event.Result.References) != 1 {
				t.Fatal(event)
			}
		}
	}
	if scanner.Err() != nil || !retrieved || !answered {
		t.Fatal(scanner.Err(), retrieved, answered)
	}
	ref, err := session.handle(context.Background(), "/retrieveref 1")
	if err != nil || !strings.Contains(ref.Text, "exact indexed passage") {
		t.Fatal(ref, err)
	}
}
