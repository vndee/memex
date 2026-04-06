package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

// CreateFeedback persists a feedback record.
func (s *SQLiteStore) CreateFeedback(ctx context.Context, fb *domain.Feedback) error {
	meta, err := json.Marshal(fb.Metadata)
	if err != nil {
		return fmt.Errorf("marshal feedback metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO feedback (id, kb_id, topic, content, correction, source, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fb.ID, fb.KBID, fb.Topic, fb.Content, fb.Correction, fb.Source, string(meta), fb.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert feedback: %w", err)
	}
	return nil
}

// SearchFeedback searches feedback using FTS5 full-text search.
func (s *SQLiteStore) SearchFeedback(ctx context.Context, kbID, query string, limit int) ([]*domain.Feedback, error) {
	if limit <= 0 {
		limit = 50
	}

	sanitized := sanitizeFTS5(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.kb_id, f.topic, f.content, f.correction, f.source, f.metadata, f.created_at
		 FROM feedback f
		 JOIN feedback_fts fts ON f.rowid = fts.rowid
		 WHERE fts.feedback_fts MATCH ? AND f.kb_id = ?
		 ORDER BY rank
		 LIMIT ?`,
		sanitized, kbID, limit)
	if err != nil {
		return nil, fmt.Errorf("search feedback: %w", err)
	}
	defer rows.Close()

	return scanFeedbackRows(rows)
}

// ListFeedbackByTopic lists feedback for a specific topic.
func (s *SQLiteStore) ListFeedbackByTopic(ctx context.Context, kbID, topic string, limit int) ([]*domain.Feedback, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error
	if topic != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kb_id, topic, content, correction, source, metadata, created_at
			 FROM feedback WHERE kb_id = ? AND topic = ? ORDER BY created_at DESC LIMIT ?`,
			kbID, topic, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, kb_id, topic, content, correction, source, metadata, created_at
			 FROM feedback WHERE kb_id = ? ORDER BY created_at DESC LIMIT ?`,
			kbID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()

	return scanFeedbackRows(rows)
}

// GetFeedbackStats returns aggregate feedback metrics for a KB.
func (s *SQLiteStore) GetFeedbackStats(ctx context.Context, kbID string) (*domain.FeedbackStats, error) {
	stats := &domain.FeedbackStats{
		KBID:        kbID,
		TopicCounts: make(map[string]int),
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT topic, COUNT(*) FROM feedback WHERE kb_id = ? GROUP BY topic`,
		kbID)
	if err != nil {
		return nil, fmt.Errorf("feedback stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var topic string
		var count int
		if err := rows.Scan(&topic, &count); err != nil {
			return nil, fmt.Errorf("scan feedback stats: %w", err)
		}
		stats.TopicCounts[topic] = count
		stats.TotalCount += count
	}

	return stats, rows.Err()
}

func scanFeedbackRows(rows *sql.Rows) ([]*domain.Feedback, error) {
	var results []*domain.Feedback
	for rows.Next() {
		fb := &domain.Feedback{}
		var metaStr string
		var createdAt string
		if err := rows.Scan(&fb.ID, &fb.KBID, &fb.Topic, &fb.Content, &fb.Correction, &fb.Source, &metaStr, &createdAt); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		if metaStr != "" && metaStr != "null" {
			if err := json.Unmarshal([]byte(metaStr), &fb.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal feedback metadata: %w", err)
			}
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			fb.CreatedAt = t
		}
		results = append(results, fb)
	}
	return results, rows.Err()
}
