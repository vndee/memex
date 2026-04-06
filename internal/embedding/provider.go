package embedding

import "context"

// Provider generates vector embeddings from text.
type Provider interface {
	// Embed returns the embedding vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding dimension size.
	Dimensions() int
}
