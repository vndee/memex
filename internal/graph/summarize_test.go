package graph

import (
	"strings"
	"testing"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func TestSummarizeSubgraph_Basic(t *testing.T) {
	nodes := []domain.SubgraphNode{
		{ID: "e1", Name: "Alice", Type: "person", Summary: "Engineer at Acme", Distance: 0},
		{ID: "e2", Name: "Bob", Type: "person", Summary: "PM at Acme", Distance: 1},
	}
	edges := []domain.SubgraphEdge{
		{ID: "r1", SourceID: "e1", TargetID: "e2", Type: "knows", Weight: 0.85,
			ValidAt: time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)},
	}

	text := SummarizeSubgraph(nodes, edges)

	if !strings.Contains(text, "Alice (person, seed)") {
		t.Errorf("missing seed label for Alice in:\n%s", text)
	}
	if !strings.Contains(text, "Bob (person, 1 hop)") {
		t.Errorf("missing 1 hop label for Bob in:\n%s", text)
	}
	if !strings.Contains(text, "knows Bob") {
		t.Errorf("missing edge summary in:\n%s", text)
	}
	if !strings.Contains(text, "weight: 0.85") {
		t.Errorf("missing weight in:\n%s", text)
	}
}

func TestSummarizeSubgraph_Empty(t *testing.T) {
	text := SummarizeSubgraph(nil, nil)
	if text != "No graph context available." {
		t.Errorf("unexpected empty output: %q", text)
	}
}
