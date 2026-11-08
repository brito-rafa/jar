package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/brito-rafa/jar/pkg/duck"
)

func TestQueryTimingIncludesRetriesAndResetsPerQuery(t *testing.T) {
	s := fixtureSession()
	calls := 0
	s.generate = func(context.Context, []model.D) (string, error) {
		calls++
		if calls == 1 {
			return "Evidence [99].", nil
		}
		return "A journal helps us reflect on decisions [1].", nil
	}
	reply, err := s.handle(context.Background(), "What helps reflection?")
	if err != nil {
		t.Fatal(err)
	}
	m := reply.Timing
	if m == nil || m.GenerationAttempts != 2 || m.GenerationMS <= 0 || m.RetrievalMS <= 0 || m.TotalMS < m.GenerationMS+m.ValidationMS {
		t.Fatalf("invalid query timing: %+v", m)
	}
	command, err := s.handle(context.Background(), "/help")
	if err != nil || command.Timing != nil {
		t.Fatalf("command inherited query timings: %+v %v", command.Timing, err)
	}
	s.search = func(context.Context, []model.D, duck.SourceFilter, string) ([]duck.Document, error) {
		return nil, errors.New("fixture search failure")
	}
	failed, err := s.handle(context.Background(), "Another question")
	if err == nil || failed.Timing == nil || failed.Timing.GenerationAttempts != 0 || failed.Timing.GenerationMS != 0 || failed.Timing.TotalMS <= 0 {
		t.Fatalf("invalid failed-query timing: %+v %v", failed.Timing, err)
	}
}

func TestWebQueryTimingOnSuccessAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s := fixtureSession()
			if fail {
				s.generate = func(context.Context, []model.D) (string, error) { return "", errors.New("fixture model failure") }
			}
			server := httptest.NewServer(webHandler(s))
			defer server.Close()
			for _, stream := range []bool{false, true} {
				req, _ := http.NewRequest("POST", server.URL+"/api/chat", strings.NewReader(`{"message":"What helps reflection?"}`))
				req.Header.Set("Content-Type", "application/json")
				if stream {
					req.Header.Set("Accept", "application/x-ndjson")
				}
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(response.Body)
				var timing *QueryTiming
				if stream {
					for decoder.More() {
						var event struct {
							Type   string       `json:"type"`
							Result chatReply    `json:"result"`
							Timing *QueryTiming `json:"timing"`
						}
						if err = decoder.Decode(&event); err != nil {
							t.Fatal(err)
						}
						if event.Type == "result" {
							timing = event.Result.Timing
						}
						if event.Type == "error" {
							timing = event.Timing
						}
					}
				} else if fail {
					var body struct {
						Timing *QueryTiming `json:"timing"`
					}
					err = decoder.Decode(&body)
					timing = body.Timing
				} else {
					var body chatReply
					err = decoder.Decode(&body)
					timing = body.Timing
				}
				response.Body.Close()
				if err != nil || timing == nil || timing.GenerationAttempts != 1 || timing.TotalMS <= 0 {
					t.Fatalf("stream=%v, timing=%+v, err=%v", stream, timing, err)
				}
			}
		})
	}
}
