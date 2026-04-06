package graph

import (
	"math"
	"sync"
	"time"
)

// Edge represents a directed edge in the knowledge graph.
type Edge struct {
	TargetID  string
	RelID     string     // relation ID for metadata lookup
	Type      string     // relation type ("works_on", "knows", etc.)
	Weight    float64    // confidence weight from extraction
	ValidAt   time.Time  // when the fact became true in the real world
	InvalidAt *time.Time // when it stopped being true (nil = still valid)
}

// Graph is a per-KB in-memory directed graph using adjacency lists.
// Supports BFS neighborhood expansion for hybrid search.
type Graph struct {
	forward map[string][]Edge // entityID -> outgoing edges
	reverse map[string][]Edge // entityID -> incoming edges
	relIdx  map[string]relRef // relID -> source/target for removal
	mu      sync.RWMutex
}

type relRef struct {
	sourceID, targetID string
}

// New creates an empty graph.
func New() *Graph {
	return &Graph{
		forward: make(map[string][]Edge),
		reverse: make(map[string][]Edge),
		relIdx:  make(map[string]relRef),
	}
}

// AddEdge adds a directed edge from sourceID to targetID. Thread-safe.
func (g *Graph) AddEdge(sourceID, targetID, relID, relType string, weight float64, validAt time.Time, invalidAt *time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addEdge(sourceID, targetID, relID, relType, weight, validAt, invalidAt)
}

// addEdge is the lock-free inner implementation. Caller must hold g.mu.
func (g *Graph) addEdge(sourceID, targetID, relID, relType string, weight float64, validAt time.Time, invalidAt *time.Time) {
	if _, exists := g.relIdx[relID]; exists {
		return
	}

	edge := Edge{
		TargetID:  targetID,
		RelID:     relID,
		Type:      relType,
		Weight:    weight,
		ValidAt:   validAt,
		InvalidAt: invalidAt,
	}
	g.forward[sourceID] = append(g.forward[sourceID], edge)

	rev := Edge{
		TargetID:  sourceID,
		RelID:     relID,
		Type:      relType,
		Weight:    weight,
		ValidAt:   validAt,
		InvalidAt: invalidAt,
	}
	g.reverse[targetID] = append(g.reverse[targetID], rev)

	g.relIdx[relID] = relRef{sourceID: sourceID, targetID: targetID}
}

// RemoveEdge removes an edge by relation ID.
func (g *Graph) RemoveEdge(relID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ref, ok := g.relIdx[relID]
	if !ok {
		return
	}
	delete(g.relIdx, relID)

	g.forward[ref.sourceID] = removeEdge(g.forward[ref.sourceID], relID)
	g.reverse[ref.targetID] = removeEdge(g.reverse[ref.targetID], relID)
}

// Neighbors returns all entity IDs reachable within maxHops from the seeds,
// traversing edges in both directions. Returns entityID -> minimum hop distance.
// Seeds themselves are included at distance 0.
func (g *Graph) Neighbors(seeds []string, maxHops int) map[string]int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	dist := make(map[string]int, len(seeds))
	queue := make([]string, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := dist[id]; !seen {
			dist[id] = 0
			queue = append(queue, id)
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hop := dist[cur]
		if hop >= maxHops {
			continue
		}

		// Traverse outgoing and incoming edges.
		for _, edges := range [][]Edge{g.forward[cur], g.reverse[cur]} {
			for _, e := range edges {
				if _, seen := dist[e.TargetID]; !seen {
					dist[e.TargetID] = hop + 1
					queue = append(queue, e.TargetID)
				}
			}
		}
	}

	return dist
}

// HasEntity returns whether the entity has any edges.
func (g *Graph) HasEntity(entityID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.forward[entityID]) > 0 || len(g.reverse[entityID]) > 0
}

// EntityCount returns the number of unique entities (nodes with at least one edge).
func (g *Graph) EntityCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	nodes := make(map[string]struct{})
	for id := range g.forward {
		nodes[id] = struct{}{}
	}
	for id := range g.reverse {
		nodes[id] = struct{}{}
	}
	return len(nodes)
}

// EdgeCount returns the number of edges in the graph.
func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.relIdx)
}

func removeEdge(edges []Edge, relID string) []Edge {
	for i, e := range edges {
		if e.RelID == relID {
			return append(edges[:i], edges[i+1:]...)
		}
	}
	return edges
}

// --- Subgraph extraction ---

// SubgraphEdge represents a collected edge within a subgraph result.
type SubgraphEdge struct {
	SourceID string
	TargetID string
	RelID    string
	Type     string
	Weight   float64
}

// SubgraphResult holds the BFS neighborhood with both nodes and edges.
type SubgraphResult struct {
	Nodes map[string]int  // entityID -> hop distance from seed
	Edges []SubgraphEdge  // edges connecting nodes within the subgraph
}

// Subgraph returns the N-hop ego-graph around seeds with full edge data.
// If allowTypes is non-empty, only edges with matching Type are traversed.
func (g *Graph) Subgraph(seeds []string, maxHops int, allowTypes []string) SubgraphResult {
	g.mu.RLock()
	defer g.mu.RUnlock()

	typeSet := buildTypeSet(allowTypes)

	dist := make(map[string]int, len(seeds))
	queue := make([]string, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := dist[id]; !seen {
			dist[id] = 0
			queue = append(queue, id)
		}
	}

	// Pass 1: BFS to discover all nodes within maxHops.
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hop := dist[cur]
		if hop >= maxHops {
			continue
		}

		for _, edgeList := range [][]Edge{g.forward[cur], g.reverse[cur]} {
			for _, e := range edgeList {
				if !typeAllowed(typeSet, e.Type) {
					continue
				}
				if _, seen := dist[e.TargetID]; !seen {
					dist[e.TargetID] = hop + 1
					queue = append(queue, e.TargetID)
				}
			}
		}
	}

	// Pass 2: Collect edges where both endpoints are in the discovered subgraph.
	seenRels := make(map[string]struct{})
	var edges []SubgraphEdge
	for id := range dist {
		for _, e := range g.forward[id] {
			if _, seen := seenRels[e.RelID]; seen {
				continue
			}
			if !typeAllowed(typeSet, e.Type) {
				continue
			}
			if _, ok := dist[e.TargetID]; ok {
				seenRels[e.RelID] = struct{}{}
				ref := g.relIdx[e.RelID]
				edges = append(edges, SubgraphEdge{
					SourceID: ref.sourceID,
					TargetID: ref.targetID,
					RelID:    e.RelID,
					Type:     e.Type,
					Weight:   e.Weight,
				})
			}
		}
	}

	return SubgraphResult{Nodes: dist, Edges: edges}
}

// --- Edge-type filtered BFS ---

// NeighborsFiltered returns entity IDs reachable within maxHops, restricted to the
// given edge types. If allowTypes is nil or empty, all edge types are traversed.
func (g *Graph) NeighborsFiltered(seeds []string, maxHops int, allowTypes []string) map[string]int {
	if len(allowTypes) == 0 {
		return g.Neighbors(seeds, maxHops)
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	typeSet := buildTypeSet(allowTypes)

	dist := make(map[string]int, len(seeds))
	queue := make([]string, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := dist[id]; !seen {
			dist[id] = 0
			queue = append(queue, id)
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hop := dist[cur]
		if hop >= maxHops {
			continue
		}
		for _, edges := range [][]Edge{g.forward[cur], g.reverse[cur]} {
			for _, e := range edges {
				if !typeAllowed(typeSet, e.Type) {
					continue
				}
				if _, seen := dist[e.TargetID]; !seen {
					dist[e.TargetID] = hop + 1
					queue = append(queue, e.TargetID)
				}
			}
		}
	}

	return dist
}

// --- Temporal BFS ---

// NeighborsAt returns entity IDs reachable within maxHops, only traversing
// edges that were valid at the given point in time.
func (g *Graph) NeighborsAt(seeds []string, maxHops int, at time.Time) map[string]int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	dist := make(map[string]int, len(seeds))
	queue := make([]string, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := dist[id]; !seen {
			dist[id] = 0
			queue = append(queue, id)
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		hop := dist[cur]
		if hop >= maxHops {
			continue
		}
		for _, edges := range [][]Edge{g.forward[cur], g.reverse[cur]} {
			for _, e := range edges {
				if !edgeActiveAt(e, at) {
					continue
				}
				if _, seen := dist[e.TargetID]; !seen {
					dist[e.TargetID] = hop + 1
					queue = append(queue, e.TargetID)
				}
			}
		}
	}

	return dist
}

// --- Weighted traversal ---

// WeightedNeighbors does BFS up to maxHops, tracking cumulative path weight
// (product of edge weights). Only edges with Weight >= minWeight are traversed.
// Returns entityID -> best cumulative weight along any path from a seed.
func (g *Graph) WeightedNeighbors(seeds []string, maxHops int, minWeight float64) map[string]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	type entry struct {
		id   string
		hop  int
		cumW float64
	}

	best := make(map[string]float64, len(seeds))
	hops := make(map[string]int, len(seeds))
	queue := make([]entry, 0, len(seeds))

	for _, id := range seeds {
		if _, seen := best[id]; !seen {
			best[id] = 1.0
			hops[id] = 0
			queue = append(queue, entry{id, 0, 1.0})
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if cur.hop >= maxHops {
			continue
		}
		for _, edges := range [][]Edge{g.forward[cur.id], g.reverse[cur.id]} {
			for _, e := range edges {
				if e.Weight < minWeight {
					continue
				}
				newW := cur.cumW * e.Weight
				prev, seen := best[e.TargetID]
				if !seen || newW > prev {
					best[e.TargetID] = newW
					hops[e.TargetID] = cur.hop + 1
					queue = append(queue, entry{e.TargetID, cur.hop + 1, newW})
				}
			}
		}
	}

	return best
}

// --- Personalized PageRank ---

// PersonalizedPageRank computes PPR scores seeded from the given entity IDs.
// alpha is the teleport probability (typically 0.15), maxIter limits iterations,
// and epsilon is the convergence threshold.
func (g *Graph) PersonalizedPageRank(seeds []string, alpha float64, maxIter int, epsilon float64) map[string]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(seeds) == 0 {
		return nil
	}

	// Build the personalization vector (uniform over seeds).
	seedWeight := 1.0 / float64(len(seeds))
	personal := make(map[string]float64, len(seeds))
	for _, s := range seeds {
		personal[s] = seedWeight
	}

	// Collect all nodes reachable in the graph (union of forward + reverse keys).
	allNodes := make(map[string]struct{})
	for id := range g.forward {
		allNodes[id] = struct{}{}
	}
	for id := range g.reverse {
		allNodes[id] = struct{}{}
	}

	// Degree = total edges (forward + reverse) for undirected view.
	degree := make(map[string]int, len(allNodes))
	for id := range allNodes {
		degree[id] = len(g.forward[id]) + len(g.reverse[id])
	}

	// Initialize ranks.
	n := float64(len(allNodes))
	rank := make(map[string]float64, len(allNodes))
	for id := range allNodes {
		rank[id] = 1.0 / n
	}

	newRank := make(map[string]float64, len(allNodes))

	for iter := 0; iter < maxIter; iter++ {
		// Reset newRank for this iteration (reuse allocation).
		for id := range newRank {
			delete(newRank, id)
		}

		// Distribute rank from each node to neighbors.
		for id := range allNodes {
			if degree[id] == 0 {
				continue
			}
			share := rank[id] / float64(degree[id])
			for _, e := range g.forward[id] {
				newRank[e.TargetID] += share
			}
			for _, e := range g.reverse[id] {
				newRank[e.TargetID] += share
			}
		}

		// Apply teleport.
		maxDiff := 0.0
		for id := range allNodes {
			nr := alpha*personal[id] + (1-alpha)*newRank[id]
			diff := math.Abs(nr - rank[id])
			if diff > maxDiff {
				maxDiff = diff
			}
			rank[id] = nr
		}

		if maxDiff < epsilon {
			break
		}
	}

	return rank
}

// --- Helpers ---

func buildTypeSet(allowTypes []string) map[string]struct{} {
	if len(allowTypes) == 0 {
		return nil
	}
	s := make(map[string]struct{}, len(allowTypes))
	for _, t := range allowTypes {
		s[t] = struct{}{}
	}
	return s
}

func typeAllowed(typeSet map[string]struct{}, edgeType string) bool {
	if typeSet == nil {
		return true
	}
	_, ok := typeSet[edgeType]
	return ok
}

func edgeActiveAt(e Edge, at time.Time) bool {
	if e.ValidAt.After(at) {
		return false
	}
	if e.InvalidAt != nil && !e.InvalidAt.After(at) {
		return false
	}
	return true
}
