package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

// searchVector searches the in-memory vecstore index, then enriches hits
// with entity/relation content from the store.
func searchVector(
	ctx context.Context,
	store storage.Store,
	engine *vecstore.Engine,
	kbID string,
	queryVec []float32,
	limit int,
) ([]*domain.SearchResult, error) {
	if !engine.HasIndex(kbID) {
		return nil, nil
	}

	hits, err := engine.Search(kbID, queryVec, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	results := make([]*domain.SearchResult, 0, len(hits))
	for _, hit := range hits {
		r, err := enrichHit(ctx, store, kbID, hit)
		if err != nil {
			continue // skip items deleted since indexing
		}
		results = append(results, r)
	}
	return results, nil
}

// enrichHit looks up entity or relation content for a vecstore SearchHit.
func enrichHit(ctx context.Context, store storage.Store, kbID string, hit vecstore.SearchHit) (*domain.SearchResult, error) {
	sim := float64(1 - hit.Distance)

	// Try entity first.
	ent, err := store.GetEntity(ctx, kbID, hit.ID)
	if err == nil {
		return &domain.SearchResult{
			ID:      ent.ID,
			KBID:    ent.KBID,
			Type:    domain.ItemEntity,
			Content: ent.Name + ": " + ent.Summary,
			Score:   sim,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get entity %s: %w", hit.ID, err)
	}

	// Try relation.
	rel, err := store.GetRelation(ctx, kbID, hit.ID)
	if err == nil {
		return &domain.SearchResult{
			ID:      rel.ID,
			KBID:    rel.KBID,
			Type:    domain.ItemRelation,
			Content: rel.Type + ": " + rel.Summary,
			Score:   sim,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get relation %s: %w", hit.ID, err)
	}

	return nil, fmt.Errorf("no entity or relation found for %s", hit.ID)
}
