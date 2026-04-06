package search

import "fmt"

const (
	maxTopK = 1000 // hard cap to prevent integer overflow and unbounded queries
	maxHops = 10   // BFS depth limit to prevent graph explosion

	// perChannelFetchMultiplier over-fetches per search channel so RRF fusion
	// has enough candidates when channels return overlapping results.
	perChannelFetchMultiplier = 3
	// postFusionBuffer retains extra candidates after RRF so temporal decay
	// re-ranking can reorder results before final truncation to TopK.
	postFusionBuffer = 2
)

// Options configures a hybrid search query.
type Options struct {
	TopK     int      // max results (default 10)
	MaxHops  int      // graph BFS depth (default 2)
	RRFk     float64  // RRF constant (default 60)
	Channels Channels // which search channels to enable
}

// Channels controls which search channels are active.
type Channels struct {
	BM25   bool
	Vector bool
	Graph  bool
}

// DefaultOptions returns search options with all channels enabled.
func DefaultOptions() Options {
	return Options{
		TopK:    10,
		MaxHops: 2,
		RRFk:    60,
		Channels: Channels{
			BM25:   true,
			Vector: true,
			Graph:  true,
		},
	}
}

// ParseChannels converts a mode string to Channels config.
// Valid modes: "hybrid" (default), "bm25", "vector".
func ParseChannels(mode string) (Channels, error) {
	switch mode {
	case "", "hybrid":
		return Channels{BM25: true, Vector: true, Graph: true}, nil
	case "bm25":
		return Channels{BM25: true}, nil
	case "vector":
		return Channels{Vector: true}, nil
	default:
		return Channels{}, fmt.Errorf("unknown search mode %q (use hybrid, bm25, or vector)", mode)
	}
}

func (o Options) withDefaults() Options {
	if o.TopK <= 0 {
		o.TopK = 10
	}
	if o.TopK > maxTopK {
		o.TopK = maxTopK
	}
	if o.MaxHops <= 0 {
		o.MaxHops = 2
	}
	if o.MaxHops > maxHops {
		o.MaxHops = maxHops
	}
	if o.RRFk <= 0 {
		o.RRFk = 60
	}
	return o
}
