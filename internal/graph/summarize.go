package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vndee/memex/internal/domain"
)

// SummarizeSubgraph converts a subgraph into a compact natural language
// representation suitable for LLM context injection. Nodes are grouped by
// hop distance and their outgoing edges listed underneath.
func SummarizeSubgraph(nodes []domain.SubgraphNode, edges []domain.SubgraphEdge) string {
	if len(nodes) == 0 {
		return "No graph context available."
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Distance != nodes[j].Distance {
			return nodes[i].Distance < nodes[j].Distance
		}
		return nodes[i].Name < nodes[j].Name
	})

	// Index edges by source for O(1) lookup.
	outgoing := make(map[string][]domain.SubgraphEdge)
	for _, e := range edges {
		outgoing[e.SourceID] = append(outgoing[e.SourceID], e)
	}

	// Build a name lookup from node ID to display name.
	nameOf := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nameOf[n.ID] = n.Name
	}

	// Sort each node's edges by weight descending.
	for id := range outgoing {
		sort.Slice(outgoing[id], func(i, j int) bool {
			return outgoing[id][i].Weight > outgoing[id][j].Weight
		})
	}

	var sb strings.Builder
	sb.WriteString("Context from knowledge graph:\n")

	for _, n := range nodes {
		distLabel := "seed"
		if n.Distance > 0 {
			distLabel = fmt.Sprintf("%d hop", n.Distance)
			if n.Distance > 1 {
				distLabel += "s"
			}
		}

		sb.WriteString(fmt.Sprintf("\n%s (%s, %s)", n.Name, n.Type, distLabel))
		if n.Summary != "" {
			sb.WriteString(": ")
			sb.WriteString(n.Summary)
		}
		sb.WriteByte('\n')

		for _, e := range outgoing[n.ID] {
			targetName := nameOf[e.TargetID]
			if targetName == "" {
				targetName = e.TargetID
			}
			sb.WriteString(fmt.Sprintf("  - %s %s [weight: %.2f", e.Type, targetName, e.Weight))
			if !e.ValidAt.IsZero() {
				sb.WriteString(fmt.Sprintf(", since %s", e.ValidAt.Format("2006-01-02")))
			}
			sb.WriteString("]\n")
		}
	}

	return sb.String()
}
