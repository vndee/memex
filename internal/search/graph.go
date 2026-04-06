package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
)

// searchGraph expands seed entity IDs via BFS on the knowledge graph,
// returning neighboring entities as SearchResults scored by hop distance.
// Seeds themselves are excluded from results.
func searchGraph(
	ctx context.Context,
	store storage.Store,
	g *graph.Graph,
	kbID string,
	seeds []string,
	maxHops int,
	limit int,
) ([]*domain.SearchResult, error) {
	if g == nil || len(seeds) == 0 {
		return nil, nil
	}

	neighbors := g.Neighbors(seeds, maxHops)

	// Build seed set for filtering.
	seedSet := make(map[string]struct{}, len(seeds))
	for _, s := range seeds {
		seedSet[s] = struct{}{}
	}

	// Sort neighbors for deterministic results (map iteration is random).
	type neighborEntry struct {
		id   string
		hops int
	}
	entries := make([]neighborEntry, 0, len(neighbors))
	for id, hops := range neighbors {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		if hops < 1 {
			continue // guard: seeds at distance 0 that escaped seedSet
		}
		entries = append(entries, neighborEntry{id, hops})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].hops != entries[j].hops {
			return entries[i].hops < entries[j].hops
		}
		return entries[i].id < entries[j].id
	})

	results := make([]*domain.SearchResult, 0, min(len(entries), limit))
	for _, ne := range entries {
		if len(results) >= limit {
			break
		}

		ent, err := store.GetEntity(ctx, kbID, ne.id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			slog.Warn("graph entity lookup failed", "id", ne.id, "error", err)
			return nil, fmt.Errorf("get entity %s: %w", ne.id, err)
		}

		results = append(results, &domain.SearchResult{
			ID:      ent.ID,
			KBID:    ent.KBID,
			Type:    domain.ItemEntity,
			Content: ent.Name + ": " + ent.Summary,
			Score:   1.0 / float64(ne.hops),
		})
	}

	return results, nil
}

// extractEntityIDs collects unique entity IDs from search results.
// For relation results, it extracts source_id and target_id from metadata.
func extractEntityIDs(lists ...[]*domain.SearchResult) []string {
	seen := make(map[string]struct{})
	var ids []string

	for _, list := range lists {
		for _, r := range list {
			if r.Type == domain.ItemEntity {
				if _, ok := seen[r.ID]; !ok {
					seen[r.ID] = struct{}{}
					ids = append(ids, r.ID)
				}
			}
			if r.Type == domain.ItemRelation {
				for _, key := range []string{"source_id", "target_id"} {
					if id, ok := r.Metadata[key]; ok && id != "" {
						if _, exists := seen[id]; !exists {
							seen[id] = struct{}{}
							ids = append(ids, id)
						}
					}
				}
			}
		}
	}
	return ids
}
