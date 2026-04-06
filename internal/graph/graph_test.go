package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func addEdge(g *Graph, src, tgt, rel, typ string, w float64) {
	g.AddEdge(src, tgt, rel, typ, w, t0, nil)
}

func TestNeighbors_SingleHop(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "c", "r2", "works_on", 0.8)

	dist := g.Neighbors([]string{"a"}, 1)

	if dist["a"] != 0 {
		t.Errorf("seed distance: want 0, got %d", dist["a"])
	}
	if dist["b"] != 1 {
		t.Errorf("b distance: want 1, got %d", dist["b"])
	}
	if dist["c"] != 1 {
		t.Errorf("c distance: want 1, got %d", dist["c"])
	}
	if len(dist) != 3 {
		t.Errorf("want 3 nodes, got %d", len(dist))
	}
}

func TestNeighbors_TwoHops(t *testing.T) {
	// a -> b -> c -> d
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "b", "c", "r2", "knows", 1.0)
	addEdge(g, "c", "d", "r3", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 2)

	if dist["a"] != 0 || dist["b"] != 1 || dist["c"] != 2 {
		t.Errorf("unexpected distances: %v", dist)
	}
	if _, ok := dist["d"]; ok {
		t.Error("d should not be reachable in 2 hops from a")
	}
}

func TestNeighbors_ReverseTraversal(t *testing.T) {
	// a -> b (directed), but BFS traverses both directions
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)

	// Starting from b should reach a via reverse edge.
	dist := g.Neighbors([]string{"b"}, 1)

	if dist["a"] != 1 {
		t.Errorf("reverse traversal: want a at distance 1, got %d", dist["a"])
	}
}

func TestNeighbors_MultipleSeeds(t *testing.T) {
	//   a - b
	//   c - d
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "c", "d", "r2", "knows", 1.0)

	dist := g.Neighbors([]string{"a", "c"}, 1)

	if len(dist) != 4 {
		t.Errorf("want 4 nodes, got %d: %v", len(dist), dist)
	}
}

func TestNeighbors_DisconnectedComponents(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "x", "y", "r2", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 10)

	if _, ok := dist["x"]; ok {
		t.Error("disconnected component should not be reachable")
	}
	if _, ok := dist["y"]; ok {
		t.Error("disconnected component should not be reachable")
	}
}

func TestNeighbors_ZeroHops(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)

	dist := g.Neighbors([]string{"a"}, 0)

	if len(dist) != 1 || dist["a"] != 0 {
		t.Errorf("zero hops: want only seed, got %v", dist)
	}
}

func TestNeighbors_EmptyGraph(t *testing.T) {
	g := New()
	dist := g.Neighbors([]string{"a"}, 2)

	if len(dist) != 1 || dist["a"] != 0 {
		t.Errorf("empty graph: want seed only, got %v", dist)
	}
}

func TestAddEdge_Dedup(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "b", "r1", "knows", 1.0) // duplicate relID

	if g.EdgeCount() != 1 {
		t.Errorf("dedup: want 1 edge, got %d", g.EdgeCount())
	}
}

func TestRemoveEdge(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "c", "r2", "uses", 0.5)

	g.RemoveEdge("r1")

	if g.EdgeCount() != 1 {
		t.Errorf("after remove: want 1 edge, got %d", g.EdgeCount())
	}

	dist := g.Neighbors([]string{"a"}, 1)
	if _, ok := dist["b"]; ok {
		t.Error("b should not be reachable after edge removal")
	}
	if dist["c"] != 1 {
		t.Error("c should still be reachable")
	}
}

func TestEntityCount(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "b", "c", "r2", "uses", 1.0)

	if g.EntityCount() != 3 {
		t.Errorf("want 3 entities, got %d", g.EntityCount())
	}
}

func TestHasEntity(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)

	if !g.HasEntity("a") {
		t.Error("a should exist")
	}
	if !g.HasEntity("b") {
		t.Error("b should exist")
	}
	if g.HasEntity("z") {
		t.Error("z should not exist")
	}
}

// --- Subgraph tests ---

func TestSubgraph_ReturnsNodesAndEdges(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "b", "c", "r2", "uses", 0.8)
	addEdge(g, "c", "d", "r3", "works_on", 0.5)

	sg := g.Subgraph([]string{"a"}, 2, nil)

	if len(sg.Nodes) != 3 {
		t.Errorf("want 3 nodes (a,b,c), got %d: %v", len(sg.Nodes), sg.Nodes)
	}
	if sg.Nodes["a"] != 0 || sg.Nodes["b"] != 1 || sg.Nodes["c"] != 2 {
		t.Errorf("unexpected distances: %v", sg.Nodes)
	}
	if _, ok := sg.Nodes["d"]; ok {
		t.Error("d should not be in 2-hop subgraph from a")
	}

	if len(sg.Edges) < 2 {
		t.Errorf("want at least 2 edges (r1, r2), got %d", len(sg.Edges))
	}

	edgeIDs := make(map[string]bool)
	for _, e := range sg.Edges {
		edgeIDs[e.RelID] = true
	}
	if !edgeIDs["r1"] || !edgeIDs["r2"] {
		t.Errorf("expected edges r1 and r2, got %v", edgeIDs)
	}
}

func TestSubgraph_EdgeTypeFilter(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "c", "r2", "works_on", 0.8)

	sg := g.Subgraph([]string{"a"}, 2, []string{"knows"})

	if _, ok := sg.Nodes["c"]; ok {
		t.Error("c should not be reachable when only 'knows' edges are allowed")
	}
	if _, ok := sg.Nodes["b"]; !ok {
		t.Error("b should be reachable via 'knows' edge")
	}
}

// --- NeighborsFiltered tests ---

func TestNeighborsFiltered_AllowTypes(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "c", "r2", "uses", 0.8)
	addEdge(g, "b", "d", "r3", "knows", 1.0)

	dist := g.NeighborsFiltered([]string{"a"}, 2, []string{"knows"})

	if _, ok := dist["c"]; ok {
		t.Error("c should not be reachable via 'uses' when filtering for 'knows'")
	}
	if dist["b"] != 1 {
		t.Errorf("b should be at distance 1, got %d", dist["b"])
	}
	if dist["d"] != 2 {
		t.Errorf("d should be at distance 2, got %d", dist["d"])
	}
}

func TestNeighborsFiltered_NilAllowAll(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "a", "c", "r2", "uses", 0.8)

	dist := g.NeighborsFiltered([]string{"a"}, 1, nil)

	if len(dist) != 3 {
		t.Errorf("nil allowTypes should traverse all: want 3, got %d", len(dist))
	}
}

// --- Temporal BFS tests ---

func TestNeighborsAt_FiltersExpiredEdges(t *testing.T) {
	g := New()
	expired := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	g.AddEdge("a", "b", "r1", "knows", 1.0, t0, &expired)
	addEdge(g, "a", "c", "r2", "uses", 0.8)

	queryTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	dist := g.NeighborsAt([]string{"a"}, 2, queryTime)

	if _, ok := dist["b"]; ok {
		t.Error("b should not be reachable via expired edge")
	}
	if _, ok := dist["c"]; !ok {
		t.Error("c should be reachable via active edge")
	}
}

func TestNeighborsAt_IncludesActiveEdges(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0) // validAt=t0, invalidAt=nil (always active)

	queryTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	dist := g.NeighborsAt([]string{"a"}, 1, queryTime)

	if dist["b"] != 1 {
		t.Errorf("b should be at distance 1, got %d", dist["b"])
	}
}

func TestNeighborsAt_ExcludesFutureEdges(t *testing.T) {
	g := New()
	future := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g.AddEdge("a", "b", "r1", "knows", 1.0, future, nil)

	queryTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	dist := g.NeighborsAt([]string{"a"}, 1, queryTime)

	if _, ok := dist["b"]; ok {
		t.Error("b should not be reachable via future edge")
	}
}

// --- WeightedNeighbors tests ---

func TestWeightedNeighbors_PrefersHighWeight(t *testing.T) {
	// a -> b (0.9), a -> c (0.3), b -> d (0.8)
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 0.9)
	addEdge(g, "a", "c", "r2", "knows", 0.3)
	addEdge(g, "b", "d", "r3", "knows", 0.8)

	weights := g.WeightedNeighbors([]string{"a"}, 2, 0)

	// b should have weight 0.9, d should have 0.9*0.8=0.72
	if w := weights["b"]; w < 0.89 || w > 0.91 {
		t.Errorf("b weight: want ~0.9, got %.4f", w)
	}
	if w := weights["d"]; w < 0.71 || w > 0.73 {
		t.Errorf("d weight: want ~0.72, got %.4f", w)
	}
}

func TestWeightedNeighbors_MinWeightFilter(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 0.9)
	addEdge(g, "a", "c", "r2", "knows", 0.3)

	weights := g.WeightedNeighbors([]string{"a"}, 1, 0.5)

	if _, ok := weights["c"]; ok {
		t.Error("c should be filtered out by minWeight=0.5")
	}
	if _, ok := weights["b"]; !ok {
		t.Error("b should be reachable (weight 0.9 >= 0.5)")
	}
}

// --- PersonalizedPageRank tests ---

func TestPersonalizedPageRank_ConvergesOnSeeds(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "b", "c", "r2", "knows", 1.0)

	ranks, err := g.PersonalizedPageRank(context.Background(), []string{"a"}, 2, 0.15, 50, 1e-8)
	if err != nil {
		t.Fatal(err)
	}

	if ranks["a"] <= ranks["c"] {
		t.Errorf("seed 'a' should rank higher than distant 'c': a=%.6f c=%.6f", ranks["a"], ranks["c"])
	}
}

func TestPersonalizedPageRank_HubsRankHigher(t *testing.T) {
	// Hub topology: h connects to a,b,c,d; leaf e only connects to d
	g := New()
	addEdge(g, "h", "a", "r1", "knows", 1.0)
	addEdge(g, "h", "b", "r2", "knows", 1.0)
	addEdge(g, "h", "c", "r3", "knows", 1.0)
	addEdge(g, "h", "d", "r4", "knows", 1.0)
	addEdge(g, "e", "d", "r5", "knows", 1.0)

	ranks, err := g.PersonalizedPageRank(context.Background(), []string{"a"}, 3, 0.15, 50, 1e-8)
	if err != nil {
		t.Fatal(err)
	}

	if ranks["h"] <= ranks["e"] {
		t.Errorf("hub 'h' should rank higher than leaf 'e': h=%.6f e=%.6f", ranks["h"], ranks["e"])
	}
}

func TestPersonalizedPageRank_EmptySeeds(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)

	ranks, err := g.PersonalizedPageRank(context.Background(), nil, 2, 0.15, 20, 1e-6)
	if err != nil {
		t.Fatal(err)
	}
	if ranks != nil {
		t.Errorf("empty seeds should return nil, got %v", ranks)
	}
}

func TestPersonalizedPageRank_LocalNeighborhoodExcludesFarNodes(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)
	addEdge(g, "b", "c", "r2", "knows", 1.0)
	addEdge(g, "x", "y", "r3", "knows", 1.0)
	addEdge(g, "y", "z", "r4", "knows", 1.0)

	ranks, err := g.PersonalizedPageRank(context.Background(), []string{"a"}, 1, 0.15, 50, 1e-8)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := ranks["c"]; ok {
		t.Fatalf("expected 2-hop node c to be excluded, got ranks %v", ranks)
	}
	if _, ok := ranks["x"]; ok {
		t.Fatalf("expected disconnected node x to be excluded, got ranks %v", ranks)
	}
	if _, ok := ranks["b"]; !ok {
		t.Fatalf("expected 1-hop node b in local neighborhood, got ranks %v", ranks)
	}
}

func TestPersonalizedPageRank_RespectsCanceledContext(t *testing.T) {
	g := New()
	addEdge(g, "a", "b", "r1", "knows", 1.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := g.PersonalizedPageRank(ctx, []string{"a"}, 2, 0.15, 20, 1e-6)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
