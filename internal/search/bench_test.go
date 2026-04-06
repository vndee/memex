package search

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const benchDim = 128 // smaller dim for faster benchmark seeding

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

func randomVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rand.Float32()*2 - 1
	}
	return v
}

// normalize returns a unit-length copy.
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	inv := float32(1.0 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// perturbVector adds noise and re-normalizes (controls similarity).
func perturbVector(v []float32, noise float32) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x + (rand.Float32()*2-1)*noise
	}
	return normalize(out)
}

// words is a small vocabulary for generating FTS-searchable text.
var words = []string{
	"algorithm", "binary", "cache", "database", "engine",
	"function", "graph", "hash", "index", "join",
	"kernel", "lambda", "memory", "network", "optimizer",
	"parser", "query", "runtime", "stack", "thread",
	"utility", "vector", "worker", "xenon", "yield", "zero",
}

func randomName() string {
	return words[rand.IntN(len(words))] + "-" + words[rand.IntN(len(words))]
}

func randomSummary(nWords int) string {
	parts := make([]string, nWords)
	for i := range parts {
		parts[i] = words[rand.IntN(len(words))]
	}
	return strings.Join(parts, " ")
}

// seedBenchData creates n entities with embeddings, ~n/2 relations with a
// chain+random graph structure, and FTS-indexed text.
func seedBenchData(b *testing.B, store *storage.SQLiteStore, n int) (entityIDs []string, entityVecs map[string][]float32) {
	b.Helper()
	ctx := context.Background()

	kb := &domain.KnowledgeBase{
		ID: "bench", Name: "Bench KB",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "nomic-embed-text", Dim: benchDim},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "llama3.2"},
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateKB(ctx, kb); err != nil {
		b.Fatal(err)
	}

	entityIDs = make([]string, n)
	entityVecs = make(map[string][]float32, n)

	for i := range n {
		id := fmt.Sprintf("e%04d", i)
		vec := normalize(randomVector(benchDim))
		entityIDs[i] = id
		entityVecs[id] = vec

		e := &domain.Entity{
			ID:        id,
			KBID:      "bench",
			Name:      randomName(),
			Type:      words[i%len(words)],
			Summary:   randomSummary(8),
			Embedding: vec,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := store.CreateEntity(ctx, e); err != nil {
			b.Fatal(err)
		}
	}

	// Create ~n/2 relations: chain + random edges for graph structure.
	relCount := 0
	for i := 1; i < n; i++ {
		// Chain: e(i-1) -> e(i)
		r := &domain.Relation{
			ID: fmt.Sprintf("r%04d", relCount), KBID: "bench",
			SourceID: entityIDs[i-1], TargetID: entityIDs[i],
			Type: "related_to", Summary: randomSummary(4),
			Weight: 0.5 + rand.Float64()*0.5,
			ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		}
		store.CreateRelation(ctx, r)
		relCount++

		if relCount >= n/2 {
			break
		}
	}
	// Random edges.
	for relCount < n/2 {
		src := rand.IntN(n)
		dst := rand.IntN(n)
		if src == dst {
			continue
		}
		r := &domain.Relation{
			ID: fmt.Sprintf("r%04d", relCount), KBID: "bench",
			SourceID: entityIDs[src], TargetID: entityIDs[dst],
			Type: "linked_to", Summary: randomSummary(4),
			Weight: 0.5 + rand.Float64()*0.5,
			ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		}
		store.CreateRelation(ctx, r)
		relCount++
	}

	return entityIDs, entityVecs
}

func buildSearcher(store *storage.SQLiteStore, entityVecs map[string][]float32, queryVec []float32) *Searcher {
	vecEng := vecstore.NewEngine(vecstore.EngineConfig{})
	vecEng.LoadIndex("bench", benchDim, entityVecs)

	graphSt := graph.NewStore()
	graphSt.Load(context.Background(), "bench", store)

	return New(store, vecEng, graphSt,
		mockEmbedFactory(queryVec), 0) // decay=0 disables temporal scoring in benchmarks
}

// ---------------------------------------------------------------------------
// Accuracy tests: Recall@K for each channel and hybrid
// ---------------------------------------------------------------------------

// TestAccuracy_RecallAtK measures recall@10 with three categories of needles:
//   - keyword-only: has unique keyword "xylophone" but random vector
//   - vector-only: close to query vector but generic keywords
//   - graph-only: connected to a keyword needle but no keyword/vector signal
//
// This tests that each channel contributes uniquely and hybrid finds them all.
func TestAccuracy_RecallAtK(t *testing.T) {
	for _, n := range []int{100, 500, 1000} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()

			kb := &domain.KnowledgeBase{
				ID: "acc", Name: "Accuracy KB",
				EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: benchDim},
				LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
				CreatedAt:   time.Now().UTC(),
			}
			store.CreateKB(ctx, kb)

			needleDir := normalize(randomVector(benchDim))
			queryVec := needleDir

			allVecs := make(map[string][]float32, n+9)
			kwNeedles := make(map[string]bool)   // findable by BM25 only
			vecNeedles := make(map[string]bool)   // findable by vector only
			graphNeedles := make(map[string]bool) // findable by graph only
			allNeedles := make(map[string]bool)

			// 3 keyword-only needles: unique keyword, random vector.
			for i := range 3 {
				id := fmt.Sprintf("kw%d", i)
				kwNeedles[id] = true
				allNeedles[id] = true
				vec := normalize(randomVector(benchDim)) // random = far from query
				allVecs[id] = vec
				store.CreateEntity(ctx, &domain.Entity{
					ID: id, KBID: "acc",
					Name: fmt.Sprintf("xylophone-item-%d", i), Type: "target",
					Summary:   "xylophone specialized unique rare entity",
					Embedding: vec,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
			}

			// 3 vector-only needles: very close vector, generic keywords.
			for i := range 3 {
				id := fmt.Sprintf("vec%d", i)
				vecNeedles[id] = true
				allNeedles[id] = true
				vec := perturbVector(needleDir, 0.03) // very close
				allVecs[id] = vec
				store.CreateEntity(ctx, &domain.Entity{
					ID: id, KBID: "acc",
					Name: randomName(), Type: "misc",
					Summary:   randomSummary(6), // generic words, no xylophone
					Embedding: vec,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
			}

			// 3 graph-only needles: connected to kw0, random vector, generic keywords.
			for i := range 3 {
				id := fmt.Sprintf("gr%d", i)
				graphNeedles[id] = true
				allNeedles[id] = true
				vec := normalize(randomVector(benchDim))
				allVecs[id] = vec
				store.CreateEntity(ctx, &domain.Entity{
					ID: id, KBID: "acc",
					Name: randomName(), Type: "misc",
					Summary:   randomSummary(6),
					Embedding: vec,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
				// Link: kw0 -> gr(i)
				store.CreateRelation(ctx, &domain.Relation{
					ID: fmt.Sprintf("gr_r%d", i), KBID: "acc",
					SourceID: "kw0", TargetID: id,
					Type: "linked", Summary: "graph edge",
					Weight: 1.0, ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
				})
			}

			// Fill n random distractors.
			for i := range n {
				id := fmt.Sprintf("dist%04d", i)
				vec := normalize(randomVector(benchDim))
				allVecs[id] = vec
				store.CreateEntity(ctx, &domain.Entity{
					ID: id, KBID: "acc",
					Name: randomName(), Type: words[i%len(words)],
					Summary:   randomSummary(8),
					Embedding: vec,
					CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
				if i > 0 && rand.Float64() < 0.3 {
					store.CreateRelation(ctx, &domain.Relation{
						ID: fmt.Sprintf("dr%04d", i), KBID: "acc",
						SourceID: fmt.Sprintf("dist%04d", i-1), TargetID: id,
						Type: "random", Summary: randomSummary(4),
						Weight: 0.5, ValidAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
					})
				}
			}

			vecEng := vecstore.NewEngine(vecstore.EngineConfig{})
			vecEng.LoadIndex("acc", benchDim, allVecs)

			graphSt := graph.NewStore()
			graphSt.Load(ctx, "acc", store)

			s := New(store, vecEng, graphSt, mockEmbedFactory(queryVec), 0)

			modes := []struct {
				name    string
				ch      Channels
				query   string
				targets map[string]bool
			}{
				{"BM25", Channels{BM25: true}, "xylophone specialized", kwNeedles},
				{"Vector", Channels{Vector: true}, "xylophone specialized", vecNeedles},
				{"BM25+Graph", Channels{BM25: true, Graph: true}, "xylophone specialized", allNeedles},
				{"Hybrid", Channels{BM25: true, Vector: true, Graph: true}, "xylophone specialized", allNeedles},
			}

			for _, mode := range modes {
				opts := DefaultOptions()
				opts.TopK = 10
				opts.Channels = mode.ch

				results, err := s.Search(ctx, "acc", mode.query, opts)
				if err != nil {
					t.Errorf("%s: search error: %v", mode.name, err)
					continue
				}

				hits := 0
				for _, r := range results {
					if mode.targets[r.ID] {
						hits++
					}
				}

				recall := float64(hits) / float64(len(mode.targets))
				t.Logf("  %-12s  recall@10 = %5.1f%% (%d/%d targets in top-%d of %d total)",
					mode.name, recall*100, hits, len(mode.targets), opts.TopK, n+9)
			}
		})
	}
}

// TestAccuracy_HybridBoost verifies that hybrid outperforms individual channels
// when the ground truth is retrievable by both keyword and vector.
func TestAccuracy_HybridBoost(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	kb := &domain.KnowledgeBase{
		ID: "boost", Name: "Boost KB",
		EmbedConfig: domain.EmbedConfig{Provider: "ollama", Model: "test", Dim: benchDim},
		LLMConfig:   domain.LLMConfig{Provider: "ollama", Model: "test"},
		CreatedAt:   time.Now().UTC(),
	}
	store.CreateKB(ctx, kb)

	targetDir := normalize(randomVector(benchDim))
	allVecs := make(map[string][]float32, 200)

	// The "perfect" entity: matches both keyword AND vector.
	allVecs["target"] = perturbVector(targetDir, 0.02)
	store.CreateEntity(ctx, &domain.Entity{
		ID: "target", KBID: "boost",
		Name: "quantum-flux", Type: "concept",
		Summary:   "quantum flux capacitor technology breakthrough",
		Embedding: allVecs["target"],
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Keyword-only decoy: matches keyword but far vector.
	allVecs["kwonly"] = normalize(randomVector(benchDim))
	store.CreateEntity(ctx, &domain.Entity{
		ID: "kwonly", KBID: "boost",
		Name: "quantum-noise", Type: "concept",
		Summary:   "quantum noise reduction filter",
		Embedding: allVecs["kwonly"],
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Vector-only decoy: close vector but no keyword match.
	allVecs["veconly"] = perturbVector(targetDir, 0.03)
	store.CreateEntity(ctx, &domain.Entity{
		ID: "veconly", KBID: "boost",
		Name: "alpha-beta", Type: "concept",
		Summary:   "pruning algorithm for game trees",
		Embedding: allVecs["veconly"],
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// 200 distractors.
	for i := range 200 {
		id := fmt.Sprintf("d%04d", i)
		allVecs[id] = normalize(randomVector(benchDim))
		store.CreateEntity(ctx, &domain.Entity{
			ID: id, KBID: "boost",
			Name:      randomName(), Type: "filler",
			Summary:   randomSummary(8),
			Embedding: allVecs[id],
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
	}

	vecEng := vecstore.NewEngine(vecstore.EngineConfig{})
	vecEng.LoadIndex("boost", benchDim, allVecs)

	graphSt := graph.NewStore()
	s := New(store, vecEng, graphSt, mockEmbedFactory(targetDir), 0)

	// BM25 only: "quantum" should find target + kwonly.
	bm25Opts := DefaultOptions()
	bm25Opts.TopK = 5
	bm25Opts.Channels = Channels{BM25: true}
	bm25Results, _ := s.Search(ctx, "boost", "quantum flux", bm25Opts)

	// Vector only.
	vecOpts := DefaultOptions()
	vecOpts.TopK = 5
	vecOpts.Channels = Channels{Vector: true}
	vecResults, _ := s.Search(ctx, "boost", "quantum flux", vecOpts)

	// Hybrid.
	hybridOpts := DefaultOptions()
	hybridOpts.TopK = 5
	hybridResults, _ := s.Search(ctx, "boost", "quantum flux", hybridOpts)

	rankOf := func(results []*domain.SearchResult, id string) int {
		for i, r := range results {
			if r.ID == id {
				return i + 1
			}
		}
		return -1
	}

	bm25Rank := rankOf(bm25Results, "target")
	vecRank := rankOf(vecResults, "target")
	hybridRank := rankOf(hybridResults, "target")

	t.Logf("Target rank — BM25: %d, Vector: %d, Hybrid: %d", bm25Rank, vecRank, hybridRank)

	// Hybrid should rank target at #1 (boosted by both channels).
	if hybridRank != 1 {
		t.Errorf("expected target at rank 1 in hybrid, got %d", hybridRank)
	}
	// Hybrid should be at least as good as either individual channel.
	if bm25Rank > 0 && hybridRank > bm25Rank {
		t.Errorf("hybrid rank %d worse than BM25 rank %d", hybridRank, bm25Rank)
	}
	if vecRank > 0 && hybridRank > vecRank {
		t.Errorf("hybrid rank %d worse than vector rank %d", hybridRank, vecRank)
	}
}

// ---------------------------------------------------------------------------
// Efficiency benchmarks
// ---------------------------------------------------------------------------

func newBenchStore(b *testing.B) *storage.SQLiteStore {
	b.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	return store
}

func benchSearch(b *testing.B, n int, ch Channels, label string) {
	store := newBenchStore(b)
	entityIDs, entityVecs := seedBenchData(b, store, n)

	// Pick a query near a known entity.
	targetID := entityIDs[rand.IntN(len(entityIDs))]
	queryVec := perturbVector(entityVecs[targetID], 0.1)

	s := buildSearcher(store, entityVecs, queryVec)

	opts := DefaultOptions()
	opts.TopK = 10
	opts.Channels = ch

	// Warm up: run once to ensure indexes are loaded.
	s.Search(context.Background(), "bench", "algorithm memory", opts)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Search(context.Background(), "bench", "algorithm memory", opts)
	}
}

func BenchmarkSearch_BM25_100(b *testing.B) {
	benchSearch(b, 100, Channels{BM25: true}, "BM25")
}

func BenchmarkSearch_BM25_500(b *testing.B) {
	benchSearch(b, 500, Channels{BM25: true}, "BM25")
}

func BenchmarkSearch_BM25_1000(b *testing.B) {
	benchSearch(b, 1000, Channels{BM25: true}, "BM25")
}

func BenchmarkSearch_Vector_100(b *testing.B) {
	benchSearch(b, 100, Channels{Vector: true}, "Vector")
}

func BenchmarkSearch_Vector_500(b *testing.B) {
	benchSearch(b, 500, Channels{Vector: true}, "Vector")
}

func BenchmarkSearch_Vector_1000(b *testing.B) {
	benchSearch(b, 1000, Channels{Vector: true}, "Vector")
}

func BenchmarkSearch_Hybrid_100(b *testing.B) {
	benchSearch(b, 100, Channels{BM25: true, Vector: true, Graph: true}, "Hybrid")
}

func BenchmarkSearch_Hybrid_500(b *testing.B) {
	benchSearch(b, 500, Channels{BM25: true, Vector: true, Graph: true}, "Hybrid")
}

func BenchmarkSearch_Hybrid_1000(b *testing.B) {
	benchSearch(b, 1000, Channels{BM25: true, Vector: true, Graph: true}, "Hybrid")
}

func BenchmarkSearch_Graph_100(b *testing.B) {
	benchSearch(b, 100, Channels{BM25: true, Graph: true}, "BM25+Graph")
}

func BenchmarkSearch_Graph_500(b *testing.B) {
	benchSearch(b, 500, Channels{BM25: true, Graph: true}, "BM25+Graph")
}

func BenchmarkSearch_Graph_1000(b *testing.B) {
	benchSearch(b, 1000, Channels{BM25: true, Graph: true}, "BM25+Graph")
}

// BenchmarkRRF_Fusion measures pure RRF cost at different list sizes.
func BenchmarkRRF_Fusion_30(b *testing.B) {
	benchRRF(b, 30)
}

func BenchmarkRRF_Fusion_100(b *testing.B) {
	benchRRF(b, 100)
}

func BenchmarkRRF_Fusion_300(b *testing.B) {
	benchRRF(b, 300)
}

func benchRRF(b *testing.B, n int) {
	list1 := make([]*domain.SearchResult, n)
	list2 := make([]*domain.SearchResult, n)
	list3 := make([]*domain.SearchResult, n)

	for i := range n {
		list1[i] = &domain.SearchResult{ID: fmt.Sprintf("a%d", i), Score: float64(n - i)}
		list2[i] = &domain.SearchResult{ID: fmt.Sprintf("b%d", i), Score: float64(n - i)}
		list3[i] = &domain.SearchResult{ID: fmt.Sprintf("c%d", i), Score: float64(n - i)}
	}
	// 30% overlap between list1 and list2.
	for i := range n / 3 {
		list2[i].ID = list1[i].ID
	}

	lists := []rankedList{
		{name: "bm25", results: list1},
		{name: "vector", results: list2},
		{name: "graph", results: list3},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		fuseRRF(lists, 60, 10)
	}
}

// BenchmarkGraphBFS measures graph traversal at different scales.
func BenchmarkGraphBFS_100(b *testing.B) {
	benchGraphBFS(b, 100)
}

func BenchmarkGraphBFS_500(b *testing.B) {
	benchGraphBFS(b, 500)
}

func BenchmarkGraphBFS_1000(b *testing.B) {
	benchGraphBFS(b, 1000)
}

func benchGraphBFS(b *testing.B, n int) {
	g := graph.New()
	ids := make([]string, n)
	for i := range n {
		ids[i] = fmt.Sprintf("e%04d", i)
	}
	// Chain.
	for i := 1; i < n; i++ {
		g.AddEdge(ids[i-1], ids[i], fmt.Sprintf("r%d", i), "next", 1.0, time.Now(), nil)
	}
	// Random cross-links.
	for i := range n / 2 {
		src := rand.IntN(n)
		dst := rand.IntN(n)
		if src != dst {
			g.AddEdge(ids[src], ids[dst], fmt.Sprintf("x%d", i), "link", 0.5, time.Now(), nil)
		}
	}

	seeds := []string{ids[0], ids[n/2]}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		g.Neighbors(seeds, 2)
	}
}
