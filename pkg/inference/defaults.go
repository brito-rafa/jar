// Package inference accesses remote OpenAI-compatible model services.
package inference

// DefaultEmbedding identifies the embedding space of new JAR indexes: Qwen3-Embedding-8B
// Q4_K_M with last-token pooling, as served by the KServe example. It is metadata, not a
// model to download or load on this machine.
const DefaultEmbedding = "Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M"

// DefaultDimensions is the vector size DefaultEmbedding produces.
const DefaultDimensions = 4096
