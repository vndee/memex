package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) CreateRelation(ctx context.Context, r *domain.Relation) error {
	var invalidAt *string
	if r.InvalidAt != nil {
		v := r.InvalidAt.UTC().Format(time.RFC3339Nano)
		invalidAt = &v
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := requireEntityInKB(ctx, tx, r.KBID, r.SourceID); err != nil {
		return fmt.Errorf("validate relation source: %w", err)
	}
	if err := requireEntityInKB(ctx, tx, r.KBID, r.TargetID); err != nil {
		return fmt.Errorf("validate relation target: %w", err)
	}
	if r.EpisodeID != "" {
		if err := requireEpisodeInKB(ctx, tx, r.KBID, r.EpisodeID); err != nil {
			return fmt.Errorf("validate relation episode: %w", err)
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO relations (id, kb_id, source_id, target_id, type, summary, weight, embedding, episode_id, valid_at, invalid_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.KBID, r.SourceID, r.TargetID, r.Type, r.Summary, r.Weight,
		encodeEmbedding(r.Embedding), nilIfEmpty(r.EpisodeID),
		r.ValidAt.UTC().Format(time.RFC3339Nano), invalidAt,
		r.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert relation: %w", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) GetRelation(ctx context.Context, kbID, id string) (*domain.Relation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kb_id, source_id, target_id, type, summary, weight, embedding, episode_id, valid_at, invalid_at, created_at
		 FROM relations WHERE kb_id = ? AND id = ?`, kbID, id)

	return scanRelation(row)
}

func (s *SQLiteStore) InvalidateRelation(ctx context.Context, kbID, id string, invalidAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE relations SET invalid_at = ? WHERE kb_id = ? AND id = ? AND invalid_at IS NULL`,
		invalidAt.UTC().Format(time.RFC3339Nano), kbID, id,
	)
	if err != nil {
		return fmt.Errorf("invalidate relation: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) GetRelationsForEntity(ctx context.Context, kbID, entityID string) ([]*domain.Relation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, source_id, target_id, type, summary, weight, embedding, episode_id, valid_at, invalid_at, created_at
		 FROM relations WHERE kb_id = ? AND (source_id = ? OR target_id = ?)
		 ORDER BY created_at DESC`,
		kbID, entityID, entityID)
	if err != nil {
		return nil, fmt.Errorf("query relations for entity: %w", err)
	}
	defer rows.Close()

	return scanRelations(rows)
}

func (s *SQLiteStore) GetValidRelations(ctx context.Context, kbID string, at time.Time) ([]*domain.Relation, error) {
	ts := at.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, source_id, target_id, type, summary, weight, embedding, episode_id, valid_at, invalid_at, created_at
		 FROM relations WHERE kb_id = ? AND valid_at <= ? AND (invalid_at IS NULL OR invalid_at > ?)
		 ORDER BY valid_at DESC`,
		kbID, ts, ts)
	if err != nil {
		return nil, fmt.Errorf("query valid relations: %w", err)
	}
	defer rows.Close()

	return scanRelations(rows)
}

func (s *SQLiteStore) ListRelations(ctx context.Context, kbID string, limit, offset int) ([]*domain.Relation, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, source_id, target_id, type, summary, weight, embedding, episode_id, valid_at, invalid_at, created_at
		 FROM relations WHERE kb_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		kbID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query relations: %w", err)
	}
	defer rows.Close()

	return scanRelations(rows)
}

// RedirectRelations rewrites all active relations pointing at fromEntityID to point at toEntityID.
// Both updates run in a single transaction. Returns the number of updated rows.
func (s *SQLiteStore) RedirectRelations(ctx context.Context, kbID, fromEntityID, toEntityID string) (int64, error) {
	if fromEntityID == toEntityID {
		return 0, fmt.Errorf("redirect relations: from and to entity IDs are identical (%s)", fromEntityID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var total int64

	// Redirect source_id references.
	res, err := tx.ExecContext(ctx,
		`UPDATE relations SET source_id = ? WHERE kb_id = ? AND source_id = ? AND invalid_at IS NULL`,
		toEntityID, kbID, fromEntityID)
	if err != nil {
		return 0, fmt.Errorf("redirect source relations: %w", err)
	}
	n, _ := res.RowsAffected()
	total += n

	// Redirect target_id references.
	res, err = tx.ExecContext(ctx,
		`UPDATE relations SET target_id = ? WHERE kb_id = ? AND target_id = ? AND invalid_at IS NULL`,
		toEntityID, kbID, fromEntityID)
	if err != nil {
		return 0, fmt.Errorf("redirect target relations: %w", err)
	}
	n, _ = res.RowsAffected()
	total += n

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit redirect: %w", err)
	}
	return total, nil
}

func scanRelation(row scanner) (*domain.Relation, error) {
	var r domain.Relation
	var embBlob []byte
	var episodeID sql.NullString
	var validAt, createdAt string
	var invalidAt sql.NullString

	err := row.Scan(&r.ID, &r.KBID, &r.SourceID, &r.TargetID, &r.Type, &r.Summary,
		&r.Weight, &embBlob, &episodeID, &validAt, &invalidAt, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("scan relation: %w", err)
	}

	r.Embedding = decodeEmbedding(embBlob)
	r.EpisodeID = episodeID.String
	r.ValidAt, _ = time.Parse(time.RFC3339Nano, validAt)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if invalidAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, invalidAt.String)
		r.InvalidAt = &t
	}
	return &r, nil
}

func scanRelations(rows *sql.Rows) ([]*domain.Relation, error) {
	var rels []*domain.Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		rels = append(rels, r)
	}
	return rels, rows.Err()
}
