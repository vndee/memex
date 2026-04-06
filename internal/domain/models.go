package domain

import (
	"encoding/json"
	"time"
)

// Provider name constants used in EmbedConfig.Provider and LLMConfig.Provider.
const (
	ProviderOllama = "ollama"
	ProviderOpenAI = "openai"
	ProviderGenAI  = "genai"
	ProviderGemini = "gemini" // alias for genai
	ProviderVertex = "vertex"
	ProviderAzure  = "azure"
	ProviderGroq   = "groq"
)

// Default model names used when creating knowledge bases without explicit config.
const (
	DefaultEmbedModel = "nomic-embed-text"
	DefaultLLMModel   = "llama3.2"
)

// Item type constants used in SearchResult.Type, DecayState.EntityType, and LogAccess.
const (
	ItemEntity   = "entity"
	ItemRelation = "relation"
	ItemEpisode  = "episode"
)

// Ingestion job statuses.
const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
)

// KnowledgeBase defines an isolated memory space with its own LLM/embedding config.
type KnowledgeBase struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	EmbedConfig EmbedConfig       `json:"embed_config"`
	LLMConfig   LLMConfig         `json:"llm_config"`
	Settings    map[string]string `json:"settings,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// EmbedConfig specifies which embedding provider and model to use.
type EmbedConfig struct {
	Provider string `json:"provider"` // "ollama", "openai", "gemini"
	Model    string `json:"model"`    // "nomic-embed-text", "text-embedding-3-small"
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"-"`             // excluded from JSON output; stored separately
	Dim      int    `json:"dim,omitempty"` // embedding dimensions (0 = auto-detect)
}

// LLMConfig specifies which LLM provider and model to use for extraction.
type LLMConfig struct {
	Provider string `json:"provider"` // "ollama", "openai", "genai", "gemini", "vertex", "azure", "groq"
	Model    string `json:"model"`    // "llama3.2", "gemini-2.5-flash", "gpt-4o-mini"
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"-"` // excluded from JSON output; stored separately
}

// Episode represents a raw interaction stored verbatim.
type Episode struct {
	ID        string            `json:"id"`
	KBID      string            `json:"kb_id"`
	Content   string            `json:"content"`
	Source    string            `json:"source"` // "mcp", "api", "cli"
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// Entity is an extracted node in the knowledge graph.
type Entity struct {
	ID        string    `json:"id"`
	KBID      string    `json:"kb_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // "person", "project", "concept", etc.
	Summary   string    `json:"summary"`
	Embedding []float32 `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Relation is a bitemporal edge between two entities.
type Relation struct {
	ID        string     `json:"id"`
	KBID      string     `json:"kb_id"`
	SourceID  string     `json:"source_id"`
	TargetID  string     `json:"target_id"`
	Type      string     `json:"type"` // "works_on", "knows", "uses", etc.
	Summary   string     `json:"summary"`
	Weight    float64    `json:"weight"`
	Embedding []float32  `json:"-"`
	EpisodeID string     `json:"episode_id,omitempty"` // provenance
	ValidAt   time.Time  `json:"valid_at"`             // when the fact became true
	InvalidAt *time.Time `json:"invalid_at,omitempty"` // nil = still valid
	CreatedAt time.Time  `json:"created_at"`           // transaction time
}

// Community is a cluster summary over a group of entities.
type Community struct {
	ID        string    `json:"id"`
	KBID      string    `json:"kb_id"`
	Name      string    `json:"name"`
	Summary   string    `json:"summary"`
	MemberIDs []string  `json:"member_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SearchResult is a unified result from hybrid retrieval.
type SearchResult struct {
	ID       string            `json:"id"`
	KBID     string            `json:"kb_id"`
	Type     string            `json:"type"` // "entity", "relation", "episode"
	Content  string            `json:"content"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DecayState tracks memory strength for a single item.
type DecayState struct {
	EntityType  string    `json:"entity_type"` // "entity", "relation", "episode"
	EntityID    string    `json:"entity_id"`
	KBID        string    `json:"kb_id"`
	Strength    float64   `json:"strength"` // [0, 1]
	AccessCount int       `json:"access_count"`
	LastAccess  time.Time `json:"last_access"`
}

// MemoryStats holds system health metrics.
type MemoryStats struct {
	KBID             string    `json:"kb_id,omitempty"`
	TotalEpisodes    int       `json:"total_episodes"`
	TotalEntities    int       `json:"total_entities"`
	TotalRelations   int       `json:"total_relations"`
	TotalCommunities int       `json:"total_communities"`
	DBSizeBytes      int64     `json:"db_size_bytes"`
	LastIngestion    time.Time `json:"last_ingestion,omitempty"`
	LastDecayRun     time.Time `json:"last_decay_run,omitempty"`
}

// IngestionJob tracks an ingestion request through its lifecycle.
type IngestionJob struct {
	ID          string            `json:"id"`
	KBID        string            `json:"kb_id"`
	Status      string            `json:"status"`
	Content     string            `json:"-"`
	Source      string            `json:"source"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	EpisodeID   string            `json:"episode_id,omitempty"`
	Result      json.RawMessage   `json:"result,omitempty"`
	Error       string            `json:"error,omitempty"`
	Attempts    int               `json:"attempts"`
	MaxAttempts int               `json:"max_attempts"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
}
