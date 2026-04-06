package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const maxHydrationLookupIDs = 900

// SubgraphEntityMetadata is the lightweight entity shape needed to hydrate a subgraph.
type SubgraphEntityMetadata struct {
	ID      string
	Name    string
	Type    string
	Summary string
}

// SubgraphRelationMetadata is the lightweight relation shape needed to hydrate a subgraph.
type SubgraphRelationMetadata struct {
	ID        string
	SourceID  string
	TargetID  string
	Type      string
	Weight    float64
	ValidAt   time.Time
	InvalidAt *time.Time
}

// GetSubgraphEntitiesByIDs returns lightweight entity metadata for subgraph hydration.
func (s *SQLiteStore) GetSubgraphEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]SubgraphEntityMetadata, error) {
	entities := make(map[string]SubgraphEntityMetadata)
	if len(ids) == 0 {
		return entities, nil
	}

	for start := 0; start < len(ids); start += maxHydrationLookupIDs {
		end := min(start+maxHydrationLookupIDs, len(ids))
		batch := ids[start:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		query := `SELECT id, name, type, summary
			FROM entities WHERE kb_id = ? AND id IN (` + placeholders + `)`

		args := make([]any, 0, len(batch)+1)
		args = append(args, kbID)
		for _, id := range batch {
			args = append(args, id)
		}

		if err := func() error {
			rows, err := s.db.QueryContext(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("query subgraph entities by ids: %w", err)
			}
			defer rows.Close()

			for rows.Next() {
				var meta SubgraphEntityMetadata
				if err := rows.Scan(&meta.ID, &meta.Name, &meta.Type, &meta.Summary); err != nil {
					return fmt.Errorf("scan subgraph entity: %w", err)
				}
				entities[meta.ID] = meta
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate subgraph entities by ids: %w", err)
			}
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	return entities, nil
}

// GetSubgraphRelationsByIDs returns lightweight relation metadata for subgraph hydration.
func (s *SQLiteStore) GetSubgraphRelationsByIDs(ctx context.Context, kbID string, ids []string) (map[string]SubgraphRelationMetadata, error) {
	rels := make(map[string]SubgraphRelationMetadata)
	if len(ids) == 0 {
		return rels, nil
	}

	for start := 0; start < len(ids); start += maxHydrationLookupIDs {
		end := min(start+maxHydrationLookupIDs, len(ids))
		batch := ids[start:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		query := `SELECT id, source_id, target_id, type, weight, valid_at, invalid_at
			FROM relations WHERE kb_id = ? AND id IN (` + placeholders + `)`

		args := make([]any, 0, len(batch)+1)
		args = append(args, kbID)
		for _, id := range batch {
			args = append(args, id)
		}

		if err := func() error {
			rows, err := s.db.QueryContext(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("query subgraph relations by ids: %w", err)
			}
			defer rows.Close()

			for rows.Next() {
				var meta SubgraphRelationMetadata
				var validAt string
				var invalidAt sql.NullString
				if err := rows.Scan(
					&meta.ID,
					&meta.SourceID,
					&meta.TargetID,
					&meta.Type,
					&meta.Weight,
					&validAt,
					&invalidAt,
				); err != nil {
					return fmt.Errorf("scan subgraph relation: %w", err)
				}
				meta.ValidAt, _ = time.Parse(time.RFC3339Nano, validAt)
				if invalidAt.Valid {
					t, _ := time.Parse(time.RFC3339Nano, invalidAt.String)
					meta.InvalidAt = &t
				}
				rels[meta.ID] = meta
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate subgraph relations by ids: %w", err)
			}
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	return rels, nil
}
