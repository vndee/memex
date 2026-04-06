package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds all runtime configuration for memex.
type Config struct {
	DBPath         string  // SQLite database path
	OllamaURL      string  // Ollama server URL
	EmbedModel     string  // Default embedding model
	LLMModel       string  // Default LLM model for extraction
	HTTPPort       int     // HTTP API port
	DecayHalfLife  float64 // Base half-life in hours
	PruneThreshold float64 // Minimum strength to keep
	LogLevel       string  // "debug", "info", "warn", "error"

	// Async ingestion
	AsyncIngest    bool   // enable async ingestion by default
	PoolSize       int    // worker pool size (default 4)
	MaxAttempts    int    // max job retry attempts (default 3)

	// Notifications
	NotifyWebhookURL string // webhook URL for job events
	NotifyFilePath   string // JSONL file path for job events
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DBPath:         filepath.Join(home, ".memex", "memex.db"),
		OllamaURL:      "http://localhost:11434",
		EmbedModel:     "nomic-embed-text",
		LLMModel:       "llama3.2",
		HTTPPort:       8080,
		DecayHalfLife:  168.0, // 1 week
		PruneThreshold: 0.05,
		LogLevel:       "info",
		PoolSize:       4,
		MaxAttempts:    3,
	}
}

// LoadFromEnv overrides config values from MEMEX_* environment variables.
func (c *Config) LoadFromEnv() {
	if v := os.Getenv("MEMEX_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("MEMEX_OLLAMA_URL"); v != "" {
		c.OllamaURL = v
	}
	if v := os.Getenv("MEMEX_EMBED_MODEL"); v != "" {
		c.EmbedModel = v
	}
	if v := os.Getenv("MEMEX_LLM_MODEL"); v != "" {
		c.LLMModel = v
	}
	if v := os.Getenv("MEMEX_HTTP_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.HTTPPort = port
		}
	}
	if v := os.Getenv("MEMEX_DECAY_HALF_LIFE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.DecayHalfLife = f
		}
	}
	if v := os.Getenv("MEMEX_PRUNE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.PruneThreshold = f
		}
	}
	if v := os.Getenv("MEMEX_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("MEMEX_ASYNC"); v == "1" || v == "true" {
		c.AsyncIngest = true
	}
	if v := os.Getenv("MEMEX_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.PoolSize = n
		}
	}
	if v := os.Getenv("MEMEX_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxAttempts = n
		}
	}
	if v := os.Getenv("MEMEX_NOTIFY_WEBHOOK_URL"); v != "" {
		c.NotifyWebhookURL = v
	}
	if v := os.Getenv("MEMEX_NOTIFY_FILE_PATH"); v != "" {
		c.NotifyFilePath = v
	}
}
