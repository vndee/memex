package storage

import (
	"container/heap"
	"context"
	"fmt"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/vecstore"
)

// SearchVectorEntities performs brute-force cosine similarity search over entity embeddings
// stored in the database. For indexed search, use vecstore.Engine.
func (s *SQLiteStore) SearchVectorEntities(ctx context.Context, kbID string, query []float32, limit int) ([]*domain.SearchResult, error) {
	return s.searchVectorGeneric(ctx, kbID, query, limit,
		`SELECT id, name, summary, embedding FROM entities WHERE kb_id = ? AND embedding IS NOT NULL`,
		domain.ItemEntity,
		func(col1, col2 string) string { return col1 + ": " + col2 },
	)
}

// SearchVectorRelations performs brute-force cosine similarity search over relation embeddings.
func (s *SQLiteStore) SearchVectorRelations(ctx context.Context, kbID string, query []float32, limit int) ([]*domain.SearchResult, error) {
	return s.searchVectorGeneric(ctx, kbID, query, limit,
		`SELECT id, type, summary, embedding FROM relations
		 WHERE kb_id = ? AND embedding IS NOT NULL AND invalid_at IS NULL`,
		domain.ItemRelation,
		func(col1, col2 string) string { return col1 + ": " + col2 },
	)
}

// searchVectorGeneric performs brute-force cosine similarity search using a given SQL query.
// The query must return columns (id, col1, col2, embedding) with a single ? placeholder for kbID.
func (s *SQLiteStore) searchVectorGeneric(
	ctx context.Context, kbID string, query []float32, limit int,
	sql string, itemType string,
	buildContent func(col1, col2 string) string,
) ([]*domain.SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, sql, kbID)
	if err != nil {
		return nil, fmt.Errorf("vector search %s: %w", itemType, err)
	}
	defer rows.Close()

	h := &maxScoreHeap{}
	heap.Init(h)

	for rows.Next() {
		var id, col1, col2 string
		var embBlob []byte
		if err := rows.Scan(&id, &col1, &col2, &embBlob); err != nil {
			return nil, fmt.Errorf("scan %s embedding: %w", itemType, err)
		}

		emb := decodeEmbedding(embBlob)
		if len(emb) != len(query) {
			continue // dimension mismatch, skip
		}

		sim := vecstore.CosineSimilarity(query, emb)

		r := &domain.SearchResult{
			ID:      id,
			KBID:    kbID,
			Type:    itemType,
			Content: buildContent(col1, col2),
			Score:   float64(sim),
		}

		if h.Len() < limit {
			heap.Push(h, scoredResult{result: r})
		} else if float64(sim) > (*h)[0].result.Score {
			(*h)[0] = scoredResult{result: r}
			heap.Fix(h, 0)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector search %s iterate: %w", itemType, err)
	}

	return extractSorted(h), nil
}

// scoredResult wraps a SearchResult for heap operations.
type scoredResult struct {
	result *domain.SearchResult
}

// maxScoreHeap is a min-heap on score (lowest score at top) for top-k selection.
// We keep the worst result at the top so we can replace it efficiently.
type maxScoreHeap []scoredResult

func (h maxScoreHeap) Len() int            { return len(h) }
func (h maxScoreHeap) Less(i, j int) bool   { return h[i].result.Score < h[j].result.Score } // min = worst
func (h maxScoreHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *maxScoreHeap) Push(x any)          { *h = append(*h, x.(scoredResult)) }
func (h *maxScoreHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// extractSorted returns results in descending score order.
func extractSorted(h *maxScoreHeap) []*domain.SearchResult {
	results := make([]*domain.SearchResult, h.Len())
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(scoredResult).result
	}
	return results
}
