package vecstore

import (
	"container/heap"
	"math"
	"math/rand/v2"
	"sync"
)

// HNSW parameters.
const (
	DefaultM              = 16  // max edges per node per layer
	DefaultEfConstruction = 200 // beam width during construction
	DefaultEfSearch       = 50  // beam width during search (tunable at query time)
)

// hnswNode is a single node in the HNSW graph.
type hnswNode struct {
	id      string
	vec     []float32
	level   int       // max level this node appears on
	friends [][]string // friends[layer] = list of neighbor IDs
}

// HNSW implements the Hierarchical Navigable Small World graph.
// Reference: Malkov & Yashunin, 2016/2018.
type HNSW struct {
	dim            int
	m              int     // max connections per layer
	mMax0          int     // max connections at layer 0 (2 * m)
	efConstruction int     // beam width during insert
	ml             float64 // level generation factor: 1 / ln(m)

	nodes    map[string]*hnswNode
	entryID  string // entry point node ID
	maxLevel int    // highest layer in the graph

	mu sync.RWMutex
}

// NewHNSW creates an HNSW index with the given parameters.
func NewHNSW(dim, m, efConstruction int) *HNSW {
	if m <= 0 {
		m = DefaultM
	}
	if efConstruction <= 0 {
		efConstruction = DefaultEfConstruction
	}
	return &HNSW{
		dim:            dim,
		m:              m,
		mMax0:          2 * m,
		efConstruction: efConstruction,
		ml:             1.0 / math.Log(float64(m)),
		nodes:          make(map[string]*hnswNode),
	}
}

// Add inserts a vector into the index. Thread-safe.
//
// If a node with the same ID already exists, only the stored vector is updated.
// Graph edges are NOT re-linked. For correct results after a vector change,
// callers should Remove then Add to rebuild edges.
func (h *HNSW) Add(id string, vec []float32) {
	if len(vec) != h.dim {
		panic("vecstore: vector dimension mismatch")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Already exists — update vector only (no re-linking).
	if existing, ok := h.nodes[id]; ok {
		existing.vec = vec
		return
	}

	level := h.randomLevel()

	node := &hnswNode{
		id:      id,
		vec:     vec,
		level:   level,
		friends: make([][]string, level+1),
	}

	// First node.
	if len(h.nodes) == 0 {
		h.nodes[id] = node
		h.entryID = id
		h.maxLevel = level
		return
	}

	h.nodes[id] = node

	ep := h.entryID

	// Phase 1: greedily descend from top to node's level + 1
	for lc := h.maxLevel; lc > level; lc-- {
		ep = h.greedyClosest(vec, ep, lc)
	}

	// Phase 2: insert at each layer from level down to 0
	for lc := min(level, h.maxLevel); lc >= 0; lc-- {
		candidates := h.searchLayer(vec, ep, h.efConstruction, lc)
		mMax := h.m
		if lc == 0 {
			mMax = h.mMax0
		}
		neighbors := h.selectNeighbors(candidates, mMax)

		// Connect node -> neighbors
		friendIDs := make([]string, len(neighbors))
		for i, hit := range neighbors {
			friendIDs[i] = hit.ID
		}
		node.friends[lc] = friendIDs

		// Connect neighbors -> node (bidirectional)
		for _, hit := range neighbors {
			nn := h.nodes[hit.ID]
			nn.friends[lc] = append(nn.friends[lc], id)
			if len(nn.friends[lc]) > mMax {
				// Prune: rebuild hits for the neighbor's friend list.
				pruneHits := make([]SearchHit, len(nn.friends[lc]))
				for j, fid := range nn.friends[lc] {
					fn := h.nodes[fid]
					if fn == nil {
						pruneHits[j] = SearchHit{ID: fid, Distance: 2} // max distance
					} else {
						pruneHits[j] = SearchHit{ID: fid, Distance: CosineDistance(nn.vec, fn.vec)}
					}
				}
				nn.friends[lc] = selectNeighborIDs(pruneHits, mMax)
			}
		}

		if len(candidates) > 0 {
			ep = candidates[0].ID // closest candidate becomes ep for next layer
		}
	}

	if level > h.maxLevel {
		h.entryID = id
		h.maxLevel = level
	}
}

// Remove deletes a node from the index. Repairs neighbor connections.
func (h *HNSW) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	node, ok := h.nodes[id]
	if !ok {
		return
	}

	// Remove from all neighbors' friend lists.
	for lc := 0; lc <= node.level; lc++ {
		for _, fid := range node.friends[lc] {
			fn := h.nodes[fid]
			if fn == nil {
				continue
			}
			fn.friends[lc] = removeFromSlice(fn.friends[lc], id)
		}
	}

	delete(h.nodes, id)

	// If we removed the entry point, pick a new one.
	if h.entryID == id {
		h.entryID = ""
		h.maxLevel = 0
		for nid, n := range h.nodes {
			if n.level >= h.maxLevel {
				h.entryID = nid
				h.maxLevel = n.level
			}
		}
	}
}

// Search finds the k nearest neighbors to query. efSearch controls the beam width
// (higher = more accurate but slower). Pass 0 for the default.
func (h *HNSW) Search(query []float32, k int, efSearch int) []SearchHit {
	if len(query) != h.dim {
		panic("vecstore: query dimension mismatch")
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.nodes) == 0 || k <= 0 {
		return nil
	}

	if efSearch <= 0 {
		efSearch = DefaultEfSearch
	}
	if efSearch < k {
		efSearch = k
	}

	ep := h.entryID

	// Greedy descent from top to layer 1
	for lc := h.maxLevel; lc > 0; lc-- {
		ep = h.greedyClosest(query, ep, lc)
	}

	// Search layer 0 with beam width = efSearch
	candidates := h.searchLayer(query, ep, efSearch, 0)

	// Return top-k (candidates already sorted by distance ascending).
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	return candidates
}

func (h *HNSW) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

func (h *HNSW) Dim() int {
	return h.dim
}

func (h *HNSW) Has(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.nodes[id]
	return ok
}

func (h *HNSW) Get(id string) []float32 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if n, ok := h.nodes[id]; ok {
		return n.vec
	}
	return nil
}

// searchLayer performs a beam search on a single layer, returning up to ef
// closest SearchHits sorted by distance (ascending). Distances are preserved
// so callers don't need to recompute them.
func (h *HNSW) searchLayer(query []float32, entryID string, ef int, layer int) []SearchHit {
	entry := h.nodes[entryID]
	if entry == nil || layer > entry.level {
		return nil
	}

	entryDist := CosineDistance(query, entry.vec)

	// Candidates: min-heap (best first)
	candidates := &minDistHeap{{ID: entryID, Distance: entryDist}}
	heap.Init(candidates)

	// Results: max-heap (worst first, for pruning)
	results := &maxDistHeap{{ID: entryID, Distance: entryDist}}
	heap.Init(results)

	visited := map[string]struct{}{entryID: {}}

	for candidates.Len() > 0 {
		closest := heap.Pop(candidates).(SearchHit)

		// Stop if the closest candidate is farther than the worst result.
		if results.Len() >= ef && closest.Distance > (*results)[0].Distance {
			break
		}

		node := h.nodes[closest.ID]
		if node == nil || layer > node.level {
			continue
		}

		for _, nid := range node.friends[layer] {
			if _, seen := visited[nid]; seen {
				continue
			}
			visited[nid] = struct{}{}

			nn := h.nodes[nid]
			if nn == nil {
				continue
			}
			dist := CosineDistance(query, nn.vec)

			if results.Len() < ef || dist < (*results)[0].Distance {
				heap.Push(candidates, SearchHit{ID: nid, Distance: dist})
				heap.Push(results, SearchHit{ID: nid, Distance: dist})
				if results.Len() > ef {
					heap.Pop(results)
				}
			}
		}
	}

	// Extract results sorted by distance (ascending).
	sorted := make([]SearchHit, results.Len())
	for i := len(sorted) - 1; i >= 0; i-- {
		sorted[i] = heap.Pop(results).(SearchHit)
	}
	return sorted
}

// greedyClosest descends greedily on the given layer to find the closest node to query.
func (h *HNSW) greedyClosest(query []float32, entryID string, layer int) string {
	current := entryID
	currentDist := CosineDistance(query, h.nodes[current].vec)

	for {
		improved := false
		node := h.nodes[current]
		if node == nil || layer > node.level {
			break
		}
		for _, nid := range node.friends[layer] {
			nn := h.nodes[nid]
			if nn == nil || layer > nn.level {
				continue
			}
			dist := CosineDistance(query, nn.vec)
			if dist < currentDist {
				current = nid
				currentDist = dist
				improved = true
			}
		}
		if !improved {
			break
		}
	}
	return current
}

// selectNeighbors picks the closest m neighbors from candidates.
// Candidates are sorted ascending by distance from searchLayer,
// so a simple prefix gives the closest m.
func (h *HNSW) selectNeighbors(candidates []SearchHit, m int) []SearchHit {
	if len(candidates) <= m {
		return candidates
	}
	return candidates[:m]
}

// selectNeighborIDs picks the closest m neighbor IDs from hits (unsorted).
func selectNeighborIDs(hits []SearchHit, m int) []string {
	if len(hits) <= m {
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		return ids
	}
	mh := make(minDistHeap, len(hits))
	copy(mh, hits)
	heap.Init(&mh)

	ids := make([]string, 0, m)
	for mh.Len() > 0 && len(ids) < m {
		ids = append(ids, heap.Pop(&mh).(SearchHit).ID)
	}
	return ids
}

// randomLevel generates a random level using exponential distribution.
func (h *HNSW) randomLevel() int {
	f := rand.Float64()
	// Guard against 0.0 which would make Log return -Inf.
	if f == 0 {
		f = math.SmallestNonzeroFloat64
	}
	return int(-math.Log(f) * h.ml)
}

func removeFromSlice(s []string, target string) []string {
	for i, v := range s {
		if v == target {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
