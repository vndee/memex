package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) CreateEpisode(ctx context.Context, ep *domain.Episode) error {
	metaJSON, err := json.Marshal(ep.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO episodes (id, kb_id, content, source, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ep.ID, ep.KBID, ep.Content, ep.Source, string(metaJSON),
		ep.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert episode: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetEpisode(ctx context.Context, kbID, id string) (*domain.Episode, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kb_id, content, source, metadata, created_at
		 FROM episodes WHERE kb_id = ? AND id = ?`, kbID, id)

	var ep domain.Episode
	var metaJSON, createdAt string
	err := row.Scan(&ep.ID, &ep.KBID, &ep.Content, &ep.Source, &metaJSON, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("scan episode: %w", err)
	}

	if err := json.Unmarshal([]byte(metaJSON), &ep.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	ep.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &ep, nil
}

func (s *SQLiteStore) ListEpisodes(ctx context.Context, kbID string, limit, offset int) ([]*domain.Episode, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, content, source, metadata, created_at
		 FROM episodes WHERE kb_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		kbID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query episodes: %w", err)
	}
	defer rows.Close()

	var eps []*domain.Episode
	for rows.Next() {
		var ep domain.Episode
		var metaJSON, createdAt string
		if err := rows.Scan(&ep.ID, &ep.KBID, &ep.Content, &ep.Source, &metaJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan episode: %w", err)
		}
		if err := json.Unmarshal([]byte(metaJSON), &ep.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
		ep.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		eps = append(eps, &ep)
	}
	return eps, rows.Err()
}

func (s *SQLiteStore) DeleteEpisode(ctx context.Context, kbID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM episodes WHERE kb_id = ? AND id = ?`, kbID, id)
	if err != nil {
		return fmt.Errorf("delete episode: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
