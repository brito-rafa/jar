package inference

import (
	"encoding/json"
	"log"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

// Pointer fields distinguish unavailable server metrics from measured zeroes.
type responseUsage struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
}
type responseTimings struct {
	PromptMS    *float64 `json:"prompt_ms"`
	PredictedMS *float64 `json:"predicted_ms"`
}
type remoteChatResponse struct {
	model.ChatResponse
	Usage   *responseUsage   `json:"usage"`
	Timings *responseTimings `json:"timings"`
}

func (r *Remote) logResponseMetrics(response remoteChatResponse) {
	metrics := struct {
		Event      string          `json:"event"`
		ResponseID string          `json:"response_id"`
		Model      string          `json:"model"`
		Usage      responseUsage   `json:"usage"`
		Timings    responseTimings `json:"timings"`
	}{Event: "model_response_metrics", ResponseID: response.ID, Model: r.name}
	if response.Usage != nil {
		metrics.Usage = *response.Usage
	}
	if response.Timings != nil {
		metrics.Timings = *response.Timings
	}
	data, err := json.Marshal(metrics)
	if err != nil {
		return
	}
	logger := r.logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Print(string(data))
}
