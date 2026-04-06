package embedding

import "fmt"

func validateBatchResponse(provider string, expectedCount, configuredDim int, vectors [][]float32) (int, error) {
	if len(vectors) != expectedCount {
		return 0, fmt.Errorf("%s: expected %d embeddings, got %d", provider, expectedCount, len(vectors))
	}

	expectedDim := configuredDim
	for i, vec := range vectors {
		if len(vec) == 0 {
			return 0, fmt.Errorf("%s: empty embedding at index %d", provider, i)
		}
		if expectedDim == 0 {
			expectedDim = len(vec)
			continue
		}
		if len(vec) != expectedDim {
			return 0, fmt.Errorf("%s: embedding dimension mismatch at index %d: got %d want %d", provider, i, len(vec), expectedDim)
		}
	}

	return expectedDim, nil
}
