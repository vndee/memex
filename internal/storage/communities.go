package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) CreateCommunity(ctx context.Context, c *domain.Community) error {
	memberJSON, err := json.Marshal(c.MemberIDs)
	if err != nil {
		return fmt.Errorf("marshal member_ids: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO communities (id, kb_id, name, summary, member_ids, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.KBID, c.Name, c.Summary, string(memberJSON),
		c.CreatedAt.UTC().Format(time.RFC3339Nano),
		c.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert community: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateCommunity(ctx context.Context, c *domain.Community) error {
	memberJSON, err := json.Marshal(c.MemberIDs)
	if err != nil {
		return fmt.Errorf("marshal member_ids: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE communities SET name = ?, summary = ?, member_ids = ?, updated_at = ?
		 WHERE kb_id = ? AND id = ?`,
		c.Name, c.Summary, string(memberJSON),
		time.Now().UTC().Format(time.RFC3339Nano), c.KBID, c.ID,
	)
	if err != nil {
		return fmt.Errorf("update community: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ListCommunities(ctx context.Context, kbID string) ([]*domain.Community, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, name, summary, member_ids, created_at, updated_at
		 FROM communities WHERE kb_id = ? ORDER BY created_at`,
		kbID)
	if err != nil {
		return nil, fmt.Errorf("query communities: %w", err)
	}
	defer rows.Close()

	var comms []*domain.Community
	for rows.Next() {
		var c domain.Community
		var memberJSON, createdAt, updatedAt string
		if err := rows.Scan(&c.ID, &c.KBID, &c.Name, &c.Summary, &memberJSON, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan community: %w", err)
		}
		if err := json.Unmarshal([]byte(memberJSON), &c.MemberIDs); err != nil {
			return nil, fmt.Errorf("unmarshal member_ids: %w", err)
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		comms = append(comms, &c)
	}
	return comms, rows.Err()
}
