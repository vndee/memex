package search

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

// EmbedderFactory creates embedding providers from KB config.
// Matches the signature of embedding.Registry.NewProvider.
type EmbedderFactory func(cfg domain.EmbedConfig) (embedding.Provider, error)

// Searcher provides hybrid search over a knowledge base by combining
// BM25 keyword search, vector similarity search, and graph BFS traversal
// using Reciprocal Rank Fusion with temporal decay scoring.
type Searcher struct {
	store    storage.Store
	vecEng   *vecstore.Engine
	graphSt  *graph.Store
	embedFn  EmbedderFactory
	halfLife float64 // decay half-life in hours
	loadSF   singleflight.Group
}

// New creates a Searcher.
func New(
	store storage.Store,
	vecEng *vecstore.Engine,
	graphSt *graph.Store,
	embedFn EmbedderFactory,
	halfLifeHours float64,
) *Searcher {
	return &Searcher{
		store:    store,
		vecEng:   vecEng,
		graphSt:  graphSt,
		embedFn:  embedFn,
		halfLife: halfLifeHours,
	}
}

// GraphStore returns the underlying graph store for direct traversal access.
func (s *Searcher) GraphStore() *graph.Store {
	return s.graphSt
}

// Search executes hybrid search: BM25 + vector + graph BFS, fused via RRF,
// with temporal decay applied.
func (s *Searcher) Search(ctx context.Context, kbID, query string, opts Options) ([]*domain.SearchResult, error) {
	opts = opts.withDefaults()
	fetchLimit := opts.TopK * perChannelFetchMultiplier

	// Embed the query for vector search.
	var queryVec []float32
	if opts.Channels.Vector || opts.Channels.Graph {
		vec, err := s.embedQuery(ctx, kbID, query)
		if err != nil {
			slog.Warn("query embedding failed, disabling vector channel",
				"kb_id", kbID, "error", err)
			opts.Channels.Vector = false
		} else {
			queryVec = vec
		}
	}

	// Ensure vecstore and graph are hydrated for this KB.
	if err := s.EnsureLoaded(ctx, kbID); err != nil {
		slog.Warn("index hydration failed", "kb_id", kbID, "error", err)
	}

	// Phase 1: Run BM25 and vector search in parallel.
	var bm25Results, vecResults []*domain.SearchResult

	g, gctx := errgroup.WithContext(ctx)

	if opts.Channels.BM25 {
		g.Go(func() error {
			var err error
			bm25Results, err = searchBM25(gctx, s.store, kbID, query, fetchLimit)
			return err
		})
	}

	if opts.Channels.Vector && queryVec != nil {
		g.Go(func() error {
			var err error
			vecResults, err = searchVector(gctx, s.store, s.vecEng, kbID, queryVec, fetchLimit)
			return err
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// Phase 2: Graph expansion using entity seeds from phase 1.
	var graphResults []*domain.SearchResult
	if opts.Channels.Graph {
		seeds := extractEntityIDs(bm25Results, vecResults)
		if opts.ExpandCommunities && len(seeds) > 0 {
			seeds = expandWithCommunities(ctx, s.store, kbID, seeds)
		}
		if len(seeds) > 0 {
			if kg := s.graphSt.Get(kbID); kg != nil {
				var err error
				graphResults, err = searchGraph(ctx, s.store, kg, kbID, seeds, opts, fetchLimit)
				if err != nil {
					slog.Warn("graph search failed", "kb_id", kbID, "error", err)
				}
			}
		}
	}

	// Phase 3: Fuse via RRF.
	var lists []rankedList
	if len(bm25Results) > 0 {
		lists = append(lists, rankedList{name: "bm25", results: bm25Results})
	}
	if len(vecResults) > 0 {
		lists = append(lists, rankedList{name: "vector", results: vecResults})
	}
	if len(graphResults) > 0 {
		lists = append(lists, rankedList{name: "graph", results: graphResults})
	}

	if len(lists) == 0 {
		return nil, nil
	}

	fused := fuseRRF(lists, opts.RRFk, opts.TopK*postFusionBuffer)

	// Phase 4: Apply temporal decay.
	if s.halfLife > 0 {
		applyTemporalDecay(ctx, s.store, fused, s.halfLife)
	}

	// Re-sort by decayed score and truncate.
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Score > fused[j].Score
	})
	if len(fused) > opts.TopK {
		fused = fused[:opts.TopK]
	}

	// Phase 5: Log access for decay tracking (fire and forget).
	// Copy slice to avoid capturing the caller's variable.
	go func(results []*domain.SearchResult) {
		for _, r := range results {
			if err := s.store.LogAccess(context.Background(), r.KBID, r.Type, r.ID); err != nil {
				slog.Debug("access log failed", "id", r.ID, "error", err)
			}
		}
	}(fused)

	return fused, nil
}

// EnsureLoaded hydrates the vecstore index and graph for a KB if not already loaded.
// Uses singleflight to prevent duplicate concurrent hydration.
func (s *Searcher) EnsureLoaded(ctx context.Context, kbID string) error {
	_, err, _ := s.loadSF.Do(kbID, func() (any, error) {
		if !s.vecEng.HasIndex(kbID) {
			if err := s.hydrateVecstore(ctx, kbID); err != nil {
				return nil, fmt.Errorf("vecstore hydration: %w", err)
			}
		}
		if !s.graphSt.Has(kbID) {
			if err := s.graphSt.Load(ctx, kbID, s.store); err != nil {
				return nil, fmt.Errorf("graph hydration: %w", err)
			}
		}
		return nil, nil
	})
	return err
}

func (s *Searcher) hydrateVecstore(ctx context.Context, kbID string) error {
	entityVecs, err := s.store.LoadEntityEmbeddings(ctx, kbID)
	if err != nil {
		return fmt.Errorf("load entity embeddings: %w", err)
	}
	relationVecs, err := s.store.LoadRelationEmbeddings(ctx, kbID)
	if err != nil {
		return fmt.Errorf("load relation embeddings: %w", err)
	}

	// Determine dimension from the first vector found.
	dim := 0
	for _, v := range entityVecs {
		dim = len(v)
		break
	}
	if dim == 0 {
		for _, v := range relationVecs {
			dim = len(v)
			break
		}
	}
	if dim == 0 {
		return nil // no vectors to load
	}

	// Insert vectors directly without intermediate merged map.
	nEntities := len(entityVecs)
	nRelations := len(relationVecs)

	s.vecEng.EnsureIndex(kbID, dim)
	for id, v := range entityVecs {
		s.vecEng.Add(kbID, id, v)
	}
	entityVecs = nil // release for GC
	for id, v := range relationVecs {
		s.vecEng.Add(kbID, id, v)
	}

	slog.Info("vecstore hydrated",
		"kb_id", kbID,
		"entities", nEntities,
		"relations", nRelations,
		"dim", dim)
	return nil
}

func (s *Searcher) embedQuery(ctx context.Context, kbID, query string) ([]float32, error) {
	kb, err := s.store.GetKB(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get KB config: %w", err)
	}

	provider, err := s.embedFn(kb.EmbedConfig)
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	vec, err := provider.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	return vec, nil
}
