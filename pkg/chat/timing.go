package chat

import (
	"context"
	"fmt"
	"time"
)

// QueryTiming measures client wall time in milliseconds, including remote waits.
// Retrieval includes embedding, database search, and result preparation.
type QueryTiming struct {
	TotalMS            float64 `json:"totalMs"`
	EmbeddingMS        float64 `json:"embeddingMs"`
	SearchMS           float64 `json:"searchMs"`
	RetrievalMS        float64 `json:"retrievalMs"`
	GenerationMS       float64 `json:"generationMs"`
	ValidationMS       float64 `json:"validationMs"`
	GenerationAttempts int     `json:"generationAttempts"`
}
type timingContextKey struct{}

func timingFrom(ctx context.Context) *QueryTiming {
	m, _ := ctx.Value(timingContextKey{}).(*QueryTiming)
	return m
}
func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func (t *QueryTiming) String() string {
	return fmt.Sprintf("Timing: total %.3fs | embedding %.3fs | vector search %.3fs | retrieval %.3fs | generation %.3fs (%d attempts) | validation %.3fs", t.TotalMS/1000, t.EmbeddingMS/1000, t.SearchMS/1000, t.RetrievalMS/1000, t.GenerationMS/1000, t.GenerationAttempts, t.ValidationMS/1000)
}
