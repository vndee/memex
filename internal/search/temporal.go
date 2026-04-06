package search

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

const defaultDecayStrength = 0.8 // for items with no access history

// applyTemporalDecay multiplies each result's RRF score by a decay factor
// based on access recency. Items accessed more recently and more frequently
// get higher multipliers. Items never accessed get a default penalty.
//
// Decay formula: multiplier = baseStrength * exp(-ln(2)/halfLife * hoursSinceAccess)
// where baseStrength increases with access count.
func applyTemporalDecay(
	ctx context.Context,
	store storage.Store,
	results []*domain.SearchResult,
	halfLifeHours float64,
) {
	if halfLifeHours <= 0 {
		return
	}
	lambda := math.Ln2 / halfLifeHours

	now := time.Now()
	for _, r := range results {
		ds, err := store.GetDecayState(ctx, r.KBID, r.Type, r.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				r.Score *= defaultDecayStrength
			}
			// On error, leave score unchanged.
			continue
		}

		hoursSince := now.Sub(ds.LastAccess).Hours()
		if hoursSince < 0 {
			hoursSince = 0
		}

		// Access count boosts stability: more accesses = slower decay.
		stability := 1.0 + math.Log1p(float64(ds.AccessCount))
		multiplier := ds.Strength * math.Exp(-lambda*hoursSince/stability)

		// Clamp to [0.1, 1.0] so decay never completely zeroes a result.
		if multiplier < 0.1 {
			multiplier = 0.1
		}
		if multiplier > 1.0 {
			multiplier = 1.0
		}
		r.Score *= multiplier
	}
}
