package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

// embedConfigStore is the DB-internal representation that includes API keys.
type embedConfigStore struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Dim      int    `json:"dim,omitempty"`
}

type llmConfigStore struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

func toEmbedStore(cfg domain.EmbedConfig) embedConfigStore {
	return embedConfigStore{
		Provider: cfg.Provider, Model: cfg.Model,
		BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Dim: cfg.Dim,
	}
}

func fromEmbedStore(s embedConfigStore) domain.EmbedConfig {
	return domain.EmbedConfig{
		Provider: s.Provider, Model: s.Model,
		BaseURL: s.BaseURL, APIKey: s.APIKey, Dim: s.Dim,
	}
}

func toLLMStore(cfg domain.LLMConfig) llmConfigStore {
	return llmConfigStore{
		Provider: cfg.Provider, Model: cfg.Model,
		BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
	}
}

func fromLLMStore(s llmConfigStore) domain.LLMConfig {
	return domain.LLMConfig{
		Provider: s.Provider, Model: s.Model,
		BaseURL: s.BaseURL, APIKey: s.APIKey,
	}
}

func (s *SQLiteStore) CreateKB(ctx context.Context, kb *domain.KnowledgeBase) error {
	if err := validateKBProviders(kb); err != nil {
		return err
	}

	embedJSON, err := json.Marshal(toEmbedStore(kb.EmbedConfig))
	if err != nil {
		return fmt.Errorf("marshal embed config: %w", err)
	}
	llmJSON, err := json.Marshal(toLLMStore(kb.LLMConfig))
	if err != nil {
		return fmt.Errorf("marshal llm config: %w", err)
	}
	settingsJSON, err := json.Marshal(kb.Settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO knowledge_bases (id, name, description, embed_config, llm_config, settings, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		kb.ID, kb.Name, kb.Description, string(embedJSON), string(llmJSON), string(settingsJSON),
		kb.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert kb: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetKB(ctx context.Context, id string) (*domain.KnowledgeBase, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, embed_config, llm_config, settings, created_at
		 FROM knowledge_bases WHERE id = ?`, id)

	return scanKB(row)
}

func (s *SQLiteStore) ListKBs(ctx context.Context) ([]*domain.KnowledgeBase, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, embed_config, llm_config, settings, created_at
		 FROM knowledge_bases ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query kbs: %w", err)
	}
	defer rows.Close()

	var kbs []*domain.KnowledgeBase
	for rows.Next() {
		kb, err := scanKB(rows)
		if err != nil {
			return nil, err
		}
		kbs = append(kbs, kb)
	}
	return kbs, rows.Err()
}

func (s *SQLiteStore) DeleteKB(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Older databases may not have cascading FKs on lifecycle tables yet.
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_access_log WHERE kb_id = ?`, id); err != nil {
		return fmt.Errorf("delete access log: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM decay_state WHERE kb_id = ?`, id); err != nil {
		return fmt.Errorf("delete decay state: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM knowledge_bases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete kb: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanKB(row scanner) (*domain.KnowledgeBase, error) {
	var kb domain.KnowledgeBase
	var embedJSON, llmJSON, settingsJSON, createdAt string

	err := row.Scan(&kb.ID, &kb.Name, &kb.Description, &embedJSON, &llmJSON, &settingsJSON, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("scan kb: %w", err)
	}

	var embedStore embedConfigStore
	if err := json.Unmarshal([]byte(embedJSON), &embedStore); err != nil {
		return nil, fmt.Errorf("unmarshal embed config: %w", err)
	}
	kb.EmbedConfig = fromEmbedStore(embedStore)

	var llmStore llmConfigStore
	if err := json.Unmarshal([]byte(llmJSON), &llmStore); err != nil {
		return nil, fmt.Errorf("unmarshal llm config: %w", err)
	}
	kb.LLMConfig = fromLLMStore(llmStore)
	if err := json.Unmarshal([]byte(settingsJSON), &kb.Settings); err != nil {
		return nil, fmt.Errorf("unmarshal settings: %w", err)
	}

	kb.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &kb, nil
}
