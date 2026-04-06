package search

import (
	"context"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

// searchBM25 runs FTS5 BM25 keyword search and returns ranked results.
func searchBM25(ctx context.Context, store storage.Store, kbID, query string, limit int) ([]*domain.SearchResult, error) {
	return store.SearchFTS(ctx, kbID, query, limit)
}
