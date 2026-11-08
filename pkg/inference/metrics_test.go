package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

func TestRemoteResponseMetrics(t *testing.T) {
	for _, test := range []struct {
		name, extras                              string
		prompt, completion, promptMS, predictedMS any
	}{
		{"reported", `,"usage":{"prompt_tokens":123,"completion_tokens":7,"total_tokens":130},"timings":{"prompt_ms":12.5,"predicted_ms":34.75}`, float64(123), float64(7), 12.5, 34.75},
		{"zero", `,"usage":{"prompt_tokens":0,"completion_tokens":0},"timings":{"prompt_ms":0,"predicted_ms":0}`, float64(0), float64(0), float64(0), float64(0)},
		{"absent", "", nil, nil, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ioJSON(w, `{"id":"fixture-response","choices":[{"message":{"content":"private response text"}}]`+test.extras+`}`)
			}))
			defer server.Close()
			remote, _ := NewRemote(server.URL, "fixture-model")
			defer remote.Unload(context.Background())
			var logs bytes.Buffer
			remote.logger = log.New(&logs, "", 0)
			for i := 0; i < 2; i++ {
				ch, err := remote.ChatStreaming(context.Background(), model.D{"messages": []model.D{model.TextMessage("user", "private prompt text")}})
				if err != nil {
					t.Fatal(err)
				}
				response := <-ch
				if test.name == "reported" && (response.Usage == nil || response.Usage.TotalTokens != 130) {
					t.Fatal("response usage was lost")
				}
			}
			if strings.Contains(logs.String(), "private") {
				t.Fatal("metrics leaked response or prompt text")
			}
			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("expected per-response logs, got %d", len(lines))
			}
			for _, line := range lines {
				var event struct {
					Event   string         `json:"event"`
					Usage   map[string]any `json:"usage"`
					Timings map[string]any `json:"timings"`
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Event != "model_response_metrics" || event.Usage["prompt_tokens"] != test.prompt || event.Usage["completion_tokens"] != test.completion || event.Timings["prompt_ms"] != test.promptMS || event.Timings["predicted_ms"] != test.predictedMS {
					t.Fatalf("unexpected event: %s", line)
				}
			}
		})
	}
}
