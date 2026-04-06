package extraction

import "context"

// ExtractionResult holds entities and relations extracted from text.
type ExtractionResult struct {
	Entities  []ExtractedEntity   `json:"entities"`
	Relations []ExtractedRelation `json:"relations"`
}

// ExtractedEntity is a raw entity found by the LLM.
type ExtractedEntity struct {
	Name    string `json:"name"`
	Type    string `json:"type"`    // "person", "project", "concept", "organization", "tool", "event", "location"
	Summary string `json:"summary"` // one-line description
}

// ExtractedRelation is a raw relation found by the LLM.
type ExtractedRelation struct {
	Source  string  `json:"source"`  // entity name (resolved later)
	Target  string  `json:"target"`  // entity name
	Type    string  `json:"type"`    // "works_on", "knows", "uses", etc.
	Summary string  `json:"summary"` // description of the relationship
	Weight  float64 `json:"weight"`  // confidence 0-1
}

// Provider extracts structured knowledge from text using an LLM.
type Provider interface {
	// Extract entities and relations from the given text.
	Extract(ctx context.Context, text string) (*ExtractionResult, error)

	// Summarize produces a concise summary of the given text.
	Summarize(ctx context.Context, text string) (string, error)

	// ResolveEntity determines if two entity descriptions refer to the same entity.
	ResolveEntity(ctx context.Context, a, b ExtractedEntity) (bool, error)
}
