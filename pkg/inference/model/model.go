// Package model defines the OpenAI-compatible protocol types used by JAR.
// The response shapes and finish-reason helper are adapted from Ardan Labs
// Kronk v1.33.1, sdk/kronk/model/models.go, licensed under Apache-2.0.
// This subset contains no local inference or runtime dependencies.
package model

import "encoding/json"

// D is a JSON request object.
type D map[string]any

func TextMessage(role, content string) D {
	return D{"role": role, "content": content}
}

const (
	FinishReasonStop   = "stop"
	FinishReasonLength = "length"
	FinishReasonTool   = "tool_calls"
	FinishReasonError  = "error"
)

type ResponseMessage struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content"`
	Reasoning string          `json:"reasoning_content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

type Choice struct {
	Index           int              `json:"index"`
	Message         *ResponseMessage `json:"message,omitempty"`
	Delta           *ResponseMessage `json:"delta,omitempty"`
	Logprobs        json.RawMessage  `json:"logprobs,omitempty"`
	FinishReasonPtr *string          `json:"finish_reason"`
}

func (c Choice) FinishReason() string {
	if c.FinishReasonPtr == nil {
		return ""
	}
	return *c.FinishReasonPtr
}

type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type Usage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	PromptTokensDetails     PromptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokens        int                     `json:"completion_tokens"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details"`
	TotalTokens             int                     `json:"total_tokens"`
	TokensPerSecond         float64                 `json:"tokens_per_second"`
	TimeToFirstTokenMS      float64                 `json:"time_to_first_token_ms"`
	DraftTokens             int                     `json:"draft_tokens,omitempty"`
	DraftAcceptedTokens     int                     `json:"draft_accepted_tokens,omitempty"`
	DraftAcceptanceRate     float64                 `json:"draft_acceptance_rate,omitempty"`
	DraftCoverage           float64                 `json:"draft_coverage,omitempty"`
	DraftDisableReason      string                  `json:"draft_disable_reason,omitempty"`
}

type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
}

type EmbedData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type EmbedUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// EmbedReponse retains the existing public type name used by indexing callers.
type EmbedReponse struct {
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Data    []EmbedData `json:"data"`
	Usage   EmbedUsage  `json:"usage"`
}
