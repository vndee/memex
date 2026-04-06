package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vndee/memex/internal/domain"
)

// sanitizeFTS5 escapes a user query for safe use with FTS5 MATCH.
// It wraps each word in double quotes so special characters like ?, ., *, etc.
// are treated as literals rather than FTS5 operators.
func sanitizeFTS5(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return `""`
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		// Escape any internal double quotes by doubling them.
		w = strings.ReplaceAll(w, `"`, `""`)
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " ")
}

// SearchFTS performs a BM25 full-text search across entities, relations, and episodes
// within a knowledge base. Results are ranked by FTS5 relevance.
func (s *SQLiteStore) SearchFTS(ctx context.Context, kbID, query string, limit int) ([]*domain.SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	var results []*domain.SearchResult

	// Search entities
	entityResults, err := s.searchEntitiesFTS(ctx, kbID, query, limit)
	if err != nil {
		return nil, err
	}
	results = append(results, entityResults...)

	// Search relations
	relationResults, err := s.searchRelationsFTS(ctx, kbID, query, limit)
	if err != nil {
		return nil, err
	}
	results = append(results, relationResults...)

	// Search episodes
	episodeResults, err := s.searchEpisodesFTS(ctx, kbID, query, limit)
	if err != nil {
		return nil, err
	}
	results = append(results, episodeResults...)

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			if results[i].Type == results[j].Type {
				return results[i].ID < results[j].ID
			}
			return results[i].Type < results[j].Type
		}
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (s *SQLiteStore) searchEntitiesFTS(ctx context.Context, kbID, query string, limit int) ([]*domain.SearchResult, error) {
	safeQuery := sanitizeFTS5(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.kb_id, e.name, e.summary, rank
		 FROM entities_fts f
		 JOIN entities e ON e.rowid = f.rowid
		 WHERE entities_fts MATCH ? AND e.kb_id = ?
		 ORDER BY rank
		 LIMIT ?`,
		safeQuery, kbID, limit)
	if err != nil {
		return nil, fmt.Errorf("search entities fts: %w", err)
	}
	defer rows.Close()

	var results []*domain.SearchResult
	for rows.Next() {
		var r domain.SearchResult
		var name, summary string
		if err := rows.Scan(&r.ID, &r.KBID, &name, &summary, &r.Score); err != nil {
			return nil, fmt.Errorf("scan entity fts: %w", err)
		}
		r.Type = "entity"
		r.Content = name + ": " + summary
		r.Score = -r.Score // FTS5 rank is negative (lower = better)
		results = append(results, &r)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) searchRelationsFTS(ctx context.Context, kbID, query string, limit int) ([]*domain.SearchResult, error) {
	safeQuery := sanitizeFTS5(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.kb_id, r.type, r.summary, rank
		 FROM relations_fts f
		 JOIN relations r ON r.rowid = f.rowid
		 WHERE relations_fts MATCH ? AND r.kb_id = ?
		 ORDER BY rank
		 LIMIT ?`,
		safeQuery, kbID, limit)
	if err != nil {
		return nil, fmt.Errorf("search relations fts: %w", err)
	}
	defer rows.Close()

	var results []*domain.SearchResult
	for rows.Next() {
		var r domain.SearchResult
		var relType, summary string
		if err := rows.Scan(&r.ID, &r.KBID, &relType, &summary, &r.Score); err != nil {
			return nil, fmt.Errorf("scan relation fts: %w", err)
		}
		r.Type = "relation"
		r.Content = relType + ": " + summary
		r.Score = -r.Score
		results = append(results, &r)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) searchEpisodesFTS(ctx context.Context, kbID, query string, limit int) ([]*domain.SearchResult, error) {
	safeQuery := sanitizeFTS5(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.kb_id, e.content, rank
		 FROM episodes_fts f
		 JOIN episodes e ON e.rowid = f.rowid
		 WHERE episodes_fts MATCH ? AND e.kb_id = ?
		 ORDER BY rank
		 LIMIT ?`,
		safeQuery, kbID, limit)
	if err != nil {
		return nil, fmt.Errorf("search episodes fts: %w", err)
	}
	defer rows.Close()

	var results []*domain.SearchResult
	for rows.Next() {
		var r domain.SearchResult
		if err := rows.Scan(&r.ID, &r.KBID, &r.Content, &r.Score); err != nil {
			return nil, fmt.Errorf("scan episode fts: %w", err)
		}
		r.Type = "episode"
		r.Score = -r.Score
		results = append(results, &r)
	}
	return results, rows.Err()
}
