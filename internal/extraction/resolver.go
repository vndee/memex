package extraction

import (
	"context"
	"log/slog"
	"strings"
	"unicode"

	"github.com/vndee/memex/internal/domain"
)

const (
	defaultSimilarityThreshold = 0.85
	llmFallbackThreshold       = 0.6
	jaroWinklerMaxPrefix       = 4
	jaroWinklerScaling         = 0.1
)

// Resolver performs 3-tier entity resolution: exact match -> fuzzy -> LLM.
type Resolver struct {
	llm              Provider // optional, for tier-3 resolution
	similarityThresh float64  // for tier-2 fuzzy matching
}

// NewResolver creates a Resolver with the given LLM provider (may be nil).
func NewResolver(llm Provider) *Resolver {
	return &Resolver{
		llm:              llm,
		similarityThresh: defaultSimilarityThreshold,
	}
}

// Resolve finds the best matching existing entity for a candidate name.
// Returns the matched entity (or nil if no match), and whether a match was found.
func (r *Resolver) Resolve(ctx context.Context, candidate ExtractedEntity, existing []*domain.Entity) (*domain.Entity, bool) {
	normalized := NormalizeName(candidate.Name)
	normalizedCandidateType := NormalizeName(candidate.Type)

	// Pre-normalize all existing names and types once
	normalizedNames := make([]string, len(existing))
	normalizedTypes := make([]string, len(existing))
	for i, e := range existing {
		normalizedNames[i] = NormalizeName(e.Name)
		normalizedTypes[i] = NormalizeName(e.Type)
	}

	// Tier 1: Exact match (case-insensitive, whitespace-normalized)
	for i, nn := range normalizedNames {
		if nn == normalized && typesMatch(normalizedCandidateType, normalizedTypes[i]) {
			return existing[i], true
		}
	}

	// Tier 2: Fuzzy match (Jaro-Winkler similarity)
	var bestIdx int
	bestScore := 0.0
	for i, nn := range normalizedNames {
		score := jaroWinkler(normalized, nn)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestScore >= r.similarityThresh {
		if typesMatch(normalizedCandidateType, normalizedTypes[bestIdx]) {
			return existing[bestIdx], true
		}
	}

	// Tier 3: LLM resolution (if available and there's a plausible candidate)
	if r.llm != nil && len(existing) > 0 && bestScore >= llmFallbackThreshold {
		bestMatch := existing[bestIdx]
		same, err := r.llm.ResolveEntity(ctx, candidate, descriptorFromEntity(bestMatch))
		if err != nil {
			slog.Warn("LLM entity resolution failed, falling through to no-match",
				"error", err, "candidate", candidate.Name, "existing", bestMatch.Name)
		} else if same {
			return bestMatch, true
		}
	}

	return nil, false
}

// NormalizeName lowercases, trims, and collapses whitespace.
func NormalizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}

func typesCompatible(a, b string) bool {
	return typesMatch(NormalizeName(a), NormalizeName(b))
}

// typesMatch checks compatibility of already-normalized type strings.
func typesMatch(a, b string) bool {
	return a == "" || b == "" || a == b
}

func descriptorFromEntity(e *domain.Entity) ExtractedEntity {
	return ExtractedEntity{
		Name:    e.Name,
		Type:    e.Type,
		Summary: e.Summary,
	}
}

// jaroWinkler computes the Jaro-Winkler similarity between two strings.
// Uses rune-based indexing to correctly handle multibyte UTF-8 characters.
func jaroWinkler(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	r1 := []rune(s1)
	r2 := []rune(s2)
	if len(r1) == 0 || len(r2) == 0 {
		return 0.0
	}

	jaro := jaroSimilarityRunes(r1, r2)

	// Winkler modification: boost for common prefix (up to 4 runes)
	prefixLen := 0
	for i := 0; i < len(r1) && i < len(r2) && i < jaroWinklerMaxPrefix; i++ {
		if r1[i] != r2[i] {
			break
		}
		prefixLen++
	}

	return jaro + float64(prefixLen)*jaroWinklerScaling*(1.0-jaro)
}

func jaroSimilarityRunes(r1, r2 []rune) float64 {
	l1, l2 := len(r1), len(r2)
	if l1 == 0 && l2 == 0 {
		return 1.0
	}

	matchDist := 0
	if l1 > l2 {
		matchDist = l1/2 - 1
	} else {
		matchDist = l2/2 - 1
	}
	if matchDist < 0 {
		matchDist = 0
	}

	s1Matches := make([]bool, l1)
	s2Matches := make([]bool, l2)

	matches := 0
	transpositions := 0

	for i := 0; i < l1; i++ {
		start := i - matchDist
		if start < 0 {
			start = 0
		}
		end := i + matchDist + 1
		if end > l2 {
			end = l2
		}
		for j := start; j < end; j++ {
			if s2Matches[j] || r1[i] != r2[j] {
				continue
			}
			s1Matches[i] = true
			s2Matches[j] = true
			matches++
			break
		}
	}

	if matches == 0 {
		return 0.0
	}

	k := 0
	for i := 0; i < l1; i++ {
		if !s1Matches[i] {
			continue
		}
		for !s2Matches[k] {
			k++
		}
		if r1[i] != r2[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	return (m/float64(l1) + m/float64(l2) + (m-float64(transpositions)/2.0)/m) / 3.0
}
