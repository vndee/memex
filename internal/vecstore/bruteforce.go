package vecstore

import (
	"container/heap"
	"sync"
)

// BruteForce is a linear scan index. Efficient for small collections (<10K vectors).
// Supports optional int8 quantization for faster distance computation.
type BruteForce struct {
	dim     int
	vectors map[string][]float32 // id -> float32 vector
	quant   map[string]QuantizedVector
	useQ    bool // use quantized vectors for search
	mu      sync.RWMutex
}

// NewBruteForce creates a brute-force index for the given dimension.
// If quantize is true, vectors are also stored in int8 form for faster search.
func NewBruteForce(dim int, quantize bool) *BruteForce {
	bf := &BruteForce{
		dim:     dim,
		vectors: make(map[string][]float32),
		useQ:    quantize,
	}
	if quantize {
		bf.quant = make(map[string]QuantizedVector)
	}
	return bf
}

func (bf *BruteForce) Add(id string, vec []float32) {
	if len(vec) != bf.dim {
		panic("vecstore: vector dimension mismatch")
	}
	bf.mu.Lock()
	defer bf.mu.Unlock()

	bf.vectors[id] = vec
	if bf.useQ {
		bf.quant[id] = Quantize(vec)
	}
}

func (bf *BruteForce) Remove(id string) {
	bf.mu.Lock()
	defer bf.mu.Unlock()

	delete(bf.vectors, id)
	if bf.useQ {
		delete(bf.quant, id)
	}
}

func (bf *BruteForce) Search(query []float32, k int) []SearchHit {
	if len(query) != bf.dim {
		panic("vecstore: query dimension mismatch")
	}
	bf.mu.RLock()
	defer bf.mu.RUnlock()

	if k <= 0 || len(bf.vectors) == 0 {
		return nil
	}

	// Max-heap of size k (we keep worst at top, pop it when we find better).
	h := &maxDistHeap{}
	heap.Init(h)

	if bf.useQ {
		qNorm := VectorNorm(query)
		for id, qv := range bf.quant {
			dist := QuantizedCosineDistance(qv, query, qNorm)
			if h.Len() < k {
				heap.Push(h, SearchHit{ID: id, Distance: dist})
			} else if dist < (*h)[0].Distance {
				(*h)[0] = SearchHit{ID: id, Distance: dist}
				heap.Fix(h, 0)
			}
		}
	} else {
		for id, vec := range bf.vectors {
			dist := CosineDistance(query, vec)
			if h.Len() < k {
				heap.Push(h, SearchHit{ID: id, Distance: dist})
			} else if dist < (*h)[0].Distance {
				(*h)[0] = SearchHit{ID: id, Distance: dist}
				heap.Fix(h, 0)
			}
		}
	}

	// Extract results in ascending distance order.
	results := make([]SearchHit, h.Len())
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(SearchHit)
	}
	return results
}

func (bf *BruteForce) Len() int {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	return len(bf.vectors)
}

func (bf *BruteForce) Dim() int {
	return bf.dim
}

func (bf *BruteForce) Has(id string) bool {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	_, ok := bf.vectors[id]
	return ok
}

// Get returns the raw float32 vector for the given ID, or nil if not found.
func (bf *BruteForce) Get(id string) []float32 {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	return bf.vectors[id]
}

// ForEach calls fn for each stored vector. Holds read lock for the duration.
func (bf *BruteForce) ForEach(fn func(id string, vec []float32)) {
	bf.mu.RLock()
	defer bf.mu.RUnlock()
	for id, vec := range bf.vectors {
		fn(id, vec)
	}
}

// SearchHit is a single result from a vector search.
type SearchHit struct {
	ID       string
	Distance float32 // lower is more similar (cosine distance)
}

// maxDistHeap is a max-heap on Distance for top-k selection.
type maxDistHeap []SearchHit

func (h maxDistHeap) Len() int            { return len(h) }
func (h maxDistHeap) Less(i, j int) bool   { return h[i].Distance > h[j].Distance } // max-heap
func (h maxDistHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *maxDistHeap) Push(x any)          { *h = append(*h, x.(SearchHit)) }
func (h *maxDistHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// minDistHeap is a min-heap on Distance for HNSW candidate selection.
type minDistHeap []SearchHit

func (h minDistHeap) Len() int            { return len(h) }
func (h minDistHeap) Less(i, j int) bool   { return h[i].Distance < h[j].Distance } // min-heap
func (h minDistHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *minDistHeap) Push(x any)          { *h = append(*h, x.(SearchHit)) }
func (h *minDistHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
