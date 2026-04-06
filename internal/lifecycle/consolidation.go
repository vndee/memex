package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

// DefaultSimilarityThreshold is the default cosine similarity threshold for entity merging.
const DefaultSimilarityThreshold = 0.92

const defaultConsolidationLimit = 100 // max pairs to consolidate per run

// MergePair represents two entities that should be merged.
type MergePair struct {
	Survivor *domain.Entity `json:"survivor"`
	Merged   *domain.Entity `json:"merged"`
	Score    float64        `json:"score"` // cosine similarity
}

// ConsolidationResult summarizes a consolidation run.
type ConsolidationResult struct {
	KBID             string       `json:"kb_id"`
	Candidates       int          `json:"candidates"`
	Merged           int          `json:"merged"`
	Pairs            []*MergePair `json:"pairs,omitempty"`
	RelationsFixed   int64        `json:"relations_fixed"`
	RelationsDeduped int64        `json:"relations_deduped"`
}

// Consolidator finds and merges duplicate entities based on embedding similarity.
type Consolidator struct {
	store     storage.Store
	threshold float64
	limit     int
}

// NewConsolidator creates a consolidator with the given similarity threshold.
func NewConsolidator(store storage.Store, threshold float64) *Consolidator {
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultSimilarityThreshold
	}
	return &Consolidator{
		store:     store,
		threshold: threshold,
		limit:     defaultConsolidationLimit,
	}
}

// FindCandidates returns pairs of entities with similarity above threshold.
// This is a brute-force O(n²) scan — suitable for <10K entities per KB.
func (c *Consolidator) FindCandidates(ctx context.Context, kbID string) ([]*MergePair, error) {
	embeddings, err := c.store.LoadEntityEmbeddings(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}

	if len(embeddings) < 2 {
		return nil, nil
	}

	// Build sorted ID list for deterministic ordering.
	ids := make([]string, 0, len(embeddings))
	for id := range embeddings {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var pairs []*MergePair
	for i := 0; i < len(ids) && len(pairs) < c.limit; i++ {
		for j := i + 1; j < len(ids) && len(pairs) < c.limit; j++ {
			sim := float64(vecstore.CosineSimilarity(embeddings[ids[i]], embeddings[ids[j]]))
			if sim >= c.threshold {
				// Load entity metadata to determine survivor.
				ei, err := c.store.GetEntity(ctx, kbID, ids[i])
				if err != nil {
					continue
				}
				ej, err := c.store.GetEntity(ctx, kbID, ids[j])
				if err != nil {
					continue
				}

				survivor, merged := pickSurvivor(ctx, c.store, kbID, ei, ej)
				pairs = append(pairs, &MergePair{
					Survivor: survivor,
					Merged:   merged,
					Score:    sim,
				})
			}
		}
	}
	return pairs, nil
}

// MergeResult holds counters from a single entity merge operation.
type MergeResult struct {
	RelationsRedirected int64
	RelationsDeduped    int64
}

// Merge executes a consolidation: redirects relations from merged entity to survivor,
// deduplicates any resulting duplicate edges, then deletes the merged entity.
func (c *Consolidator) Merge(ctx context.Context, kbID string, pair *MergePair) (*MergeResult, error) {
	result := &MergeResult{}

	// Redirect all relations from merged -> survivor.
	n, err := c.store.RedirectRelations(ctx, kbID, pair.Merged.ID, pair.Survivor.ID)
	if err != nil {
		return nil, fmt.Errorf("redirect relations: %w", err)
	}
	result.RelationsRedirected = n

	// Deduplicate edges that became duplicates after redirect.
	deduped, err := c.store.DeduplicateRelationsForEntity(ctx, kbID, pair.Survivor.ID)
	if err != nil {
		slog.Warn("consolidation: dedup after redirect failed", "entity", pair.Survivor.ID, "error", err)
	} else {
		result.RelationsDeduped = deduped
	}

	// Delete the merged entity.
	if err := c.store.DeleteEntity(ctx, kbID, pair.Merged.ID); err != nil {
		return result, fmt.Errorf("delete merged entity: %w", err)
	}

	// Clean up decay state for the merged entity.
	if err := c.store.DeleteDecayState(ctx, kbID, domain.ItemEntity, pair.Merged.ID); err != nil {
		slog.Warn("consolidation: failed to clean decay state", "id", pair.Merged.ID, "error", err)
	}

	slog.Info("consolidated entities",
		"kb_id", kbID,
		"survivor_id", pair.Survivor.ID,
		"merged_id", pair.Merged.ID,
		"similarity", fmt.Sprintf("%.4f", pair.Score),
		"relations_redirected", n,
		"relations_deduped", deduped)

	return result, nil
}

// RunConsolidation finds candidates and merges them all. Returns a summary.
func (c *Consolidator) RunConsolidation(ctx context.Context, kbID string) (*ConsolidationResult, error) {
	pairs, err := c.FindCandidates(ctx, kbID)
	if err != nil {
		return nil, err
	}

	result := &ConsolidationResult{
		KBID:       kbID,
		Candidates: len(pairs),
	}

	for _, pair := range pairs {
		mr, err := c.Merge(ctx, kbID, pair)
		if err != nil {
			slog.Error("consolidation merge failed",
				"survivor", pair.Survivor.ID,
				"merged", pair.Merged.ID,
				"error", err)
			continue
		}
		result.Merged++
		result.RelationsFixed += mr.RelationsRedirected
		result.RelationsDeduped += mr.RelationsDeduped
		result.Pairs = append(result.Pairs, pair)
	}

	return result, nil
}

// pickSurvivor chooses which entity to keep. Prefers the entity with more relations,
// then the one with the longer summary, then the older one.
func pickSurvivor(ctx context.Context, store storage.Store, kbID string, a, b *domain.Entity) (*domain.Entity, *domain.Entity) {
	relsA, _ := store.GetRelationsForEntity(ctx, kbID, a.ID)
	relsB, _ := store.GetRelationsForEntity(ctx, kbID, b.ID)

	switch {
	case len(relsA) > len(relsB):
		return a, b
	case len(relsB) > len(relsA):
		return b, a
	case len(a.Summary) > len(b.Summary):
		return a, b
	case len(b.Summary) > len(a.Summary):
		return b, a
	case a.CreatedAt.Before(b.CreatedAt):
		return a, b
	default:
		return b, a
	}
}
