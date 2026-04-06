package storage

import (
	"context"
	"fmt"
)

// LoadEntityEmbeddings returns all entity embeddings for a KB as id -> []float32.
// Used to hydrate the in-memory vector index at startup.
func (s *SQLiteStore) LoadEntityEmbeddings(ctx context.Context, kbID string) (map[string][]float32, error) {
	return s.loadEmbeddings(ctx, kbID,
		`SELECT id, embedding FROM entities WHERE kb_id = ? AND embedding IS NOT NULL`)
}

// LoadRelationEmbeddings returns all valid relation embeddings for a KB as id -> []float32.
func (s *SQLiteStore) LoadRelationEmbeddings(ctx context.Context, kbID string) (map[string][]float32, error) {
	return s.loadEmbeddings(ctx, kbID,
		`SELECT id, embedding FROM relations
		 WHERE kb_id = ? AND embedding IS NOT NULL AND invalid_at IS NULL`)
}

func (s *SQLiteStore) loadEmbeddings(ctx context.Context, kbID, query string) (map[string][]float32, error) {
	rows, err := s.db.QueryContext(ctx, query, kbID)
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	defer rows.Close()

	vecs := make(map[string][]float32)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("scan embedding: %w", err)
		}
		if emb := decodeEmbedding(blob); len(emb) > 0 {
			vecs[id] = emb
		}
	}
	return vecs, rows.Err()
}
