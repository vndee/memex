package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vndee/memex/internal/domain"
)

const maxDedupGroups = 500 // bound dedup work per call

func (s *SQLiteStore) CreateRelation(ctx context.Context, r *domain.Relation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := validateRelationRefs(ctx, tx, r); err != nil {
		return err
	}
	if err := insertRelationTx(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

// validateRelationRefs checks that source, target, and episode exist in the KB.
func validateRelationRefs(ctx context.Context, tx *sql.Tx, r *domain.Relation) error {
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
	return nil
}

// insertRelationTx inserts a relation row within an existing transaction.
func insertRelationTx(ctx context.Context, tx *sql.Tx, r *domain.Relation) error {
	var invalidAt *string
	if r.InvalidAt != nil {
		v := r.InvalidAt.UTC().Format(time.RFC3339Nano)
		invalidAt = &v
	}
	_, err := tx.ExecContext(ctx,
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
	return nil
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

// UpsertRelation creates a new relation or strengthens an existing active edge with the
// same (source, target, type) tuple. Returns true if a new relation was created.
func (s *SQLiteStore) UpsertRelation(ctx context.Context, r *domain.Relation) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check for an existing active relation with the same edge tuple.
	var existingID, existingSummary string
	var existingWeight float64
	err = tx.QueryRowContext(ctx,
		`SELECT id, summary, weight FROM relations
		 WHERE kb_id = ? AND source_id = ? AND target_id = ? AND type = ? AND invalid_at IS NULL
		 LIMIT 1`,
		r.KBID, r.SourceID, r.TargetID, r.Type,
	).Scan(&existingID, &existingSummary, &existingWeight)

	if err == sql.ErrNoRows {
		// No existing edge — validate and insert.
		if err := validateRelationRefs(ctx, tx, r); err != nil {
			return false, err
		}
		if err := insertRelationTx(ctx, tx, r); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if err != nil {
		return false, fmt.Errorf("find active relation: %w", err)
	}

	// Existing edge found — strengthen weight and conditionally update summary/embedding.
	newWeight := domain.CombineWeights(existingWeight, r.Weight)
	newSummary := domain.BetterSummary(existingSummary, r.Summary)

	if r.Embedding != nil {
		_, err = tx.ExecContext(ctx,
			`UPDATE relations SET weight = ?, summary = ?, embedding = ? WHERE id = ?`,
			newWeight, newSummary, encodeEmbedding(r.Embedding), existingID)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE relations SET weight = ?, summary = ? WHERE id = ?`,
			newWeight, newSummary, existingID)
	}
	if err != nil {
		return false, fmt.Errorf("strengthen relation: %w", err)
	}

	return false, tx.Commit()
}

// DeduplicateRelationsForKB finds and merges all duplicate active edges in a knowledge base.
// Duplicate edges share the same (source_id, target_id, type). Self-loops are also removed.
// Returns the number of relation rows deleted.
func (s *SQLiteStore) DeduplicateRelationsForKB(ctx context.Context, kbID string) (int64, error) {
	return s.deduplicateRelations(ctx, kbID, "")
}

// DeduplicateRelationsForEntity deduplicates active edges involving a specific entity.
// Returns the number of relation rows deleted.
func (s *SQLiteStore) DeduplicateRelationsForEntity(ctx context.Context, kbID, entityID string) (int64, error) {
	return s.deduplicateRelations(ctx, kbID, entityID)
}

// deduplicateRelations is the shared implementation. If entityID is empty, it scans the whole KB.
func (s *SQLiteStore) deduplicateRelations(ctx context.Context, kbID, entityID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete self-loops created by entity consolidation.
	var selfLoopQuery string
	var selfLoopArgs []any
	if entityID == "" {
		selfLoopQuery = `DELETE FROM relations WHERE kb_id = ? AND source_id = target_id AND invalid_at IS NULL`
		selfLoopArgs = []any{kbID}
	} else {
		selfLoopQuery = `DELETE FROM relations WHERE kb_id = ? AND source_id = target_id AND invalid_at IS NULL AND (source_id = ? OR target_id = ?)`
		selfLoopArgs = []any{kbID, entityID, entityID}
	}
	res, err := tx.ExecContext(ctx, selfLoopQuery, selfLoopArgs...)
	if err != nil {
		return 0, fmt.Errorf("delete self-loops: %w", err)
	}
	totalDeleted, _ := res.RowsAffected()

	// Find duplicate groups (bounded to prevent unbounded work).
	var groupQuery string
	var groupArgs []any
	if entityID == "" {
		groupQuery = `SELECT source_id, target_id, type FROM relations
			WHERE kb_id = ? AND invalid_at IS NULL
			GROUP BY source_id, target_id, type HAVING COUNT(*) > 1
			LIMIT ?`
		groupArgs = []any{kbID, maxDedupGroups}
	} else {
		groupQuery = `SELECT source_id, target_id, type FROM relations
			WHERE kb_id = ? AND invalid_at IS NULL AND (source_id = ? OR target_id = ?)
			GROUP BY source_id, target_id, type HAVING COUNT(*) > 1
			LIMIT ?`
		groupArgs = []any{kbID, entityID, entityID, maxDedupGroups}
	}

	groups, err := tx.QueryContext(ctx, groupQuery, groupArgs...)
	if err != nil {
		return 0, fmt.Errorf("find duplicate groups: %w", err)
	}

	type dupGroup struct {
		sourceID, targetID, relType string
	}
	var dupes []dupGroup
	for groups.Next() {
		var g dupGroup
		if err := groups.Scan(&g.sourceID, &g.targetID, &g.relType); err != nil {
			groups.Close()
			return 0, fmt.Errorf("scan duplicate group: %w", err)
		}
		dupes = append(dupes, g)
	}
	groups.Close()
	if err := groups.Err(); err != nil {
		return 0, fmt.Errorf("iterate duplicate groups: %w", err)
	}

	// Merge each group: keep the highest-weight row, combine weights, keep longest summary.
	for _, g := range dupes {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, summary, weight, valid_at FROM relations
			 WHERE kb_id = ? AND source_id = ? AND target_id = ? AND type = ? AND invalid_at IS NULL
			 ORDER BY weight DESC, created_at ASC`,
			kbID, g.sourceID, g.targetID, g.relType)
		if err != nil {
			return 0, fmt.Errorf("load duplicate group: %w", err)
		}

		var survivorID, bestSummary, oldestValidAt string
		var weights []float64
		var deleteIDs []string
		first := true
		for rows.Next() {
			var id, summary, validAt string
			var w float64
			if err := rows.Scan(&id, &summary, &w, &validAt); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scan duplicate row: %w", err)
			}
			weights = append(weights, w)
			if first {
				survivorID = id
				bestSummary = summary
				oldestValidAt = validAt
				first = false
			} else {
				deleteIDs = append(deleteIDs, id)
				bestSummary = domain.BetterSummary(bestSummary, summary)
				if validAt < oldestValidAt {
					oldestValidAt = validAt
				}
			}
		}
		rows.Close()

		if len(deleteIDs) == 0 {
			continue
		}

		// Update survivor with combined weight and best summary.
		mergedWeight := domain.CombineWeightsMulti(weights)
		_, err = tx.ExecContext(ctx,
			`UPDATE relations SET weight = ?, summary = ?, valid_at = ? WHERE id = ?`,
			mergedWeight, bestSummary, oldestValidAt, survivorID)
		if err != nil {
			return 0, fmt.Errorf("update surviving relation: %w", err)
		}

		// Batch delete the duplicates.
		placeholders := strings.Repeat("?,", len(deleteIDs))
		placeholders = placeholders[:len(placeholders)-1]
		delArgs := make([]any, len(deleteIDs))
		for i, id := range deleteIDs {
			delArgs[i] = id
		}
		res, err = tx.ExecContext(ctx,
			"DELETE FROM relations WHERE id IN ("+placeholders+")", delArgs...)
		if err != nil {
			return 0, fmt.Errorf("delete duplicate relations: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n

		slog.Debug("deduplicated relation edge",
			"kb_id", kbID,
			"merged", len(deleteIDs), "new_weight", mergedWeight)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit dedup: %w", err)
	}
	return totalDeleted, nil
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
