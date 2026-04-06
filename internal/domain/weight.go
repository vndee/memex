package domain

// CombineWeights merges two confidence weights using probability union.
// Result is bounded to [0, 1] and monotonically increasing with each observation.
// Examples: (0.5, 0.5) → 0.75, (0.8, 0.5) → 0.90, (1.0, x) → 1.0, (0, 0) → 0.
func CombineWeights(a, b float64) float64 {
	return 1 - (1-clampWeight(a))*(1-clampWeight(b))
}

// CombineWeightsMulti merges multiple weights via repeated probability union.
func CombineWeightsMulti(weights []float64) float64 {
	if len(weights) == 0 {
		return 0
	}
	result := clampWeight(weights[0])
	for _, w := range weights[1:] {
		result = CombineWeights(result, w)
	}
	return result
}

// BetterSummary returns the longer of two summaries. Used as a heuristic:
// a longer summary typically contains more information.
func BetterSummary(a, b string) string {
	if len(b) > len(a) {
		return b
	}
	return a
}

func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}
