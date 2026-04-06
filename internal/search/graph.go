package search

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
)

// Personalized PageRank default parameters.
const (
	pprAlpha   = 0.15 // teleport probability (jump back to seed)
	pprMaxIter = 20   // max power iterations
	pprEpsilon = 1e-6 // convergence threshold
)

// searchGraph expands seed entity IDs via BFS on the knowledge graph,
// returning neighboring entities as SearchResults. Scoring strategy is
// controlled by opts.GraphScorer. Seeds themselves are excluded from results.
func searchGraph(
	ctx context.Context,
	store storage.Store,
	g *graph.Graph,
	kbID string,
	seeds []string,
	opts Options,
	limit int,
) ([]*domain.SearchResult, error) {
	if g == nil || len(seeds) == 0 {
		return nil, nil
	}

	seedSet := make(map[string]struct{}, len(seeds))
	for _, s := range seeds {
		seedSet[s] = struct{}{}
	}

	switch opts.GraphScorer {
	case GraphScorerPageRank:
		return searchGraphPPR(ctx, store, g, kbID, seeds, seedSet, limit)
	case GraphScorerWeighted:
		return searchGraphWeighted(ctx, store, g, kbID, seeds, seedSet, opts, limit)
	default:
		return searchGraphBFS(ctx, store, g, kbID, seeds, seedSet, opts, limit)
	}
}

func searchGraphBFS(
	ctx context.Context,
	store storage.Store,
	g *graph.Graph,
	kbID string,
	seeds []string,
	seedSet map[string]struct{},
	opts Options,
	limit int,
) ([]*domain.SearchResult, error) {
	var neighbors map[string]int

	switch {
	case opts.TemporalAt != nil:
		neighbors = g.NeighborsAt(seeds, opts.MaxHops, *opts.TemporalAt)
	case len(opts.EdgeTypes) > 0:
		neighbors = g.NeighborsFiltered(seeds, opts.MaxHops, opts.EdgeTypes)
	default:
		neighbors = g.Neighbors(seeds, opts.MaxHops)
	}

	type entry struct {
		id   string
		hops int
	}
	entries := make([]entry, 0, len(neighbors))
	for id, hops := range neighbors {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		if hops < 1 {
			continue
		}
		entries = append(entries, entry{id, hops})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].hops != entries[j].hops {
			return entries[i].hops < entries[j].hops
		}
		return entries[i].id < entries[j].id
	})

	scores := make(map[string]float64, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		scores[e.id] = 1.0 / float64(e.hops)
		ids = append(ids, e.id)
	}
	return hydrateEntityResults(ctx, store, kbID, ids, scores, limit)
}

func searchGraphPPR(
	ctx context.Context,
	store storage.Store,
	g *graph.Graph,
	kbID string,
	seeds []string,
	seedSet map[string]struct{},
	limit int,
) ([]*domain.SearchResult, error) {
	ranks := g.PersonalizedPageRank(seeds, pprAlpha, pprMaxIter, pprEpsilon)

	type entry struct {
		id   string
		hops int
	}
	entries := make([]entry, 0, len(ranks))
	for id := range ranks {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		entries = append(entries, entry{id, 1}) // hop distance not meaningful for PPR
	}

	sort.Slice(entries, func(i, j int) bool {
		return ranks[entries[i].id] > ranks[entries[j].id]
	})

	scores := make(map[string]float64, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		scores[e.id] = ranks[e.id]
		ids = append(ids, e.id)
	}
	return hydrateEntityResults(ctx, store, kbID, ids, scores, limit)
}

func searchGraphWeighted(
	ctx context.Context,
	store storage.Store,
	g *graph.Graph,
	kbID string,
	seeds []string,
	seedSet map[string]struct{},
	opts Options,
	limit int,
) ([]*domain.SearchResult, error) {
	weights := g.WeightedNeighbors(seeds, opts.MaxHops, opts.MinWeight)

	type entry struct {
		id   string
		hops int
	}
	entries := make([]entry, 0, len(weights))
	for id := range weights {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		entries = append(entries, entry{id, 1})
	}

	sort.Slice(entries, func(i, j int) bool {
		return weights[entries[i].id] > weights[entries[j].id]
	})

	scores := make(map[string]float64, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		scores[e.id] = weights[e.id]
		ids = append(ids, e.id)
	}
	return hydrateEntityResults(ctx, store, kbID, ids, scores, limit)
}

// hydrateEntityResults fetches entity metadata and builds SearchResults.
func hydrateEntityResults(
	ctx context.Context,
	store storage.Store,
	kbID string,
	ids []string,
	scores map[string]float64,
	limit int,
) ([]*domain.SearchResult, error) {
	entitiesByID, err := store.GetEntitiesByIDs(ctx, kbID, ids)
	if err != nil {
		slog.Warn("graph entity batch lookup failed", "count", len(ids), "error", err)
		return nil, fmt.Errorf("get entities by ids: %w", err)
	}

	results := make([]*domain.SearchResult, 0, min(len(ids), limit))
	for _, id := range ids {
		if len(results) >= limit {
			break
		}
		ent, ok := entitiesByID[id]
		if !ok {
			continue
		}
		results = append(results, &domain.SearchResult{
			ID:      ent.ID,
			KBID:    ent.KBID,
			Type:    domain.ItemEntity,
			Content: ent.Name + ": " + ent.Summary,
			Score:   scores[id],
		})
	}
	return results, nil
}

// expandWithCommunities augments the seed set by including all members of any
// community that contains at least one seed entity.
func expandWithCommunities(ctx context.Context, store storage.Store, kbID string, seeds []string) []string {
	communities, err := store.ListCommunities(ctx, kbID)
	if err != nil {
		slog.Warn("community expansion failed", "error", err)
		return seeds
	}

	seedSet := make(map[string]struct{}, len(seeds))
	for _, s := range seeds {
		seedSet[s] = struct{}{}
	}

	for _, c := range communities {
		hasSeed := false
		for _, mid := range c.MemberIDs {
			if _, ok := seedSet[mid]; ok {
				hasSeed = true
				break
			}
		}
		if hasSeed {
			for _, mid := range c.MemberIDs {
				seedSet[mid] = struct{}{}
			}
		}
	}

	expanded := make([]string, 0, len(seedSet))
	for id := range seedSet {
		expanded = append(expanded, id)
	}
	return expanded
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
