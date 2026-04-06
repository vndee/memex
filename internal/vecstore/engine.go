package vecstore

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

const (
	// DefaultHNSWThreshold is the vector count at which we switch from brute-force to HNSW.
	DefaultHNSWThreshold = 10_000
)

// EngineConfig configures the vector search engine.
type EngineConfig struct {
	HNSWThreshold  int
	M              int
	EfConstruction int
	EfSearch       int
	Quantize       bool
}

func (c EngineConfig) withDefaults() EngineConfig {
	if c.HNSWThreshold <= 0 {
		c.HNSWThreshold = DefaultHNSWThreshold
	}
	if c.M <= 0 {
		c.M = DefaultM
	}
	if c.EfConstruction <= 0 {
		c.EfConstruction = DefaultEfConstruction
	}
	if c.EfSearch <= 0 {
		c.EfSearch = DefaultEfSearch
	}
	return c
}

// Engine manages per-KB vector indexes with adaptive strategy switching.
type Engine struct {
	config  EngineConfig
	indexes map[string]*kbIndex // kbID -> index
	mu      sync.RWMutex       // protects the indexes map only
}

// kbIndex holds the vector index for a single knowledge base.
// The inner Index has its own fine-grained locking for Add/Remove/Search.
type kbIndex struct {
	dim        int
	index      Index
	upgrading  atomic.Bool // prevents concurrent upgrades
	upgradeMu  sync.Mutex  // serializes the actual upgrade
}

func (ki *kbIndex) isHNSW() bool {
	_, ok := ki.index.(*hnswSearchAdapter)
	return ok
}

// NewEngine creates a vector search engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		config:  cfg.withDefaults(),
		indexes: make(map[string]*kbIndex),
	}
}

// EnsureIndex creates a per-KB index if it doesn't exist. Thread-safe.
func (e *Engine) EnsureIndex(kbID string, dim int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.indexes[kbID]; ok {
		return
	}

	e.indexes[kbID] = &kbIndex{
		dim:   dim,
		index: NewBruteForce(dim, e.config.Quantize),
	}
}

// Add inserts or updates a vector in the KB's index.
// Returns an error on dimension mismatch instead of panicking.
func (e *Engine) Add(kbID, id string, vec []float32) error {
	idx := e.getIndex(kbID)
	if idx == nil {
		return fmt.Errorf("vecstore: no index for kb %q (call EnsureIndex first)", kbID)
	}
	if len(vec) != idx.dim {
		return fmt.Errorf("vecstore: dimension mismatch: got %d, want %d", len(vec), idx.dim)
	}

	// The inner index (BruteForce/HNSW) has its own lock.
	idx.index.Add(id, vec)

	// Check upgrade threshold. The atomic bool prevents concurrent upgrades.
	if !idx.isHNSW() && idx.index.Len() >= e.config.HNSWThreshold {
		e.maybeUpgrade(kbID, idx)
	}

	return nil
}

// Remove deletes a vector from the KB's index.
func (e *Engine) Remove(kbID, id string) error {
	idx := e.getIndex(kbID)
	if idx == nil {
		return fmt.Errorf("vecstore: no index for kb %q (call EnsureIndex first)", kbID)
	}
	idx.index.Remove(id)
	return nil
}

// Search finds the k nearest neighbors in the given KB.
// Returns an error on dimension mismatch instead of panicking.
func (e *Engine) Search(kbID string, query []float32, k int) ([]SearchHit, error) {
	idx := e.getIndex(kbID)
	if idx == nil {
		return nil, fmt.Errorf("vecstore: no index for kb %q (call EnsureIndex first)", kbID)
	}
	if len(query) != idx.dim {
		return nil, fmt.Errorf("vecstore: query dimension mismatch: got %d, want %d", len(query), idx.dim)
	}
	return idx.index.Search(query, k), nil
}

// Len returns the number of vectors in a KB's index.
func (e *Engine) Len(kbID string) int {
	idx := e.getIndex(kbID)
	if idx == nil {
		return 0
	}
	return idx.index.Len()
}

// HasIndex returns true if a KB's index is loaded.
func (e *Engine) HasIndex(kbID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.indexes[kbID]
	return ok
}

// DropIndex removes and frees a KB's index.
func (e *Engine) DropIndex(kbID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.indexes, kbID)
}

// getIndex looks up a KB's index under read lock.
func (e *Engine) getIndex(kbID string) *kbIndex {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.indexes[kbID]
}

// maybeUpgrade atomically upgrades a brute-force index to HNSW.
// Uses per-kbIndex mutex to prevent concurrent upgrades while not blocking
// searches on other KBs.
func (e *Engine) maybeUpgrade(kbID string, idx *kbIndex) {
	// Fast path: another goroutine is already upgrading.
	if !idx.upgrading.CompareAndSwap(false, true) {
		return
	}
	defer idx.upgrading.Store(false)

	idx.upgradeMu.Lock()
	defer idx.upgradeMu.Unlock()

	// Double-check after acquiring the mutex.
	if idx.isHNSW() {
		return
	}

	bf, ok := idx.index.(*BruteForce)
	if !ok {
		return
	}

	slog.Info("vecstore: upgrading to HNSW", "kb_id", kbID, "vectors", bf.Len())

	// Snapshot vectors under the BruteForce read lock (brief hold),
	// then build HNSW without holding any lock.
	type vecEntry struct {
		id  string
		vec []float32
	}
	snap := make([]vecEntry, 0, bf.Len())
	bf.ForEach(func(id string, vec []float32) {
		snap = append(snap, vecEntry{id, vec})
	})

	hnsw := NewHNSW(idx.dim, e.config.M, e.config.EfConstruction)
	for _, s := range snap {
		hnsw.Add(s.id, s.vec)
	}

	// Swap atomically. New searches immediately use HNSW.
	idx.index = &hnswSearchAdapter{HNSW: hnsw, efSearch: e.config.EfSearch}

	slog.Info("vecstore: HNSW upgrade complete", "kb_id", kbID, "vectors", hnsw.Len())
}

// LoadIndex populates a KB's index from pre-existing vectors.
// Typically called at startup to hydrate the index from DB.
func (e *Engine) LoadIndex(kbID string, dim int, vectors map[string][]float32) {
	e.EnsureIndex(kbID, dim)

	idx := e.getIndex(kbID)
	if idx == nil {
		slog.Error("vecstore: LoadIndex: index missing after EnsureIndex", "kb_id", kbID)
		return
	}

	for id, vec := range vectors {
		if len(vec) != idx.dim {
			continue
		}
		idx.index.Add(id, vec)
	}

	if !idx.isHNSW() && idx.index.Len() >= e.config.HNSWThreshold {
		e.maybeUpgrade(kbID, idx)
	}
}
