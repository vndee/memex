package extraction

import (
	"context"
	"testing"

	"github.com/vndee/memex/internal/domain"
)

type stubResolverLLM struct {
	same  bool
	calls int
	a     ExtractedEntity
	b     ExtractedEntity
}

func (s *stubResolverLLM) Extract(context.Context, string) (*ExtractionResult, error) {
	return nil, nil
}

func (s *stubResolverLLM) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (s *stubResolverLLM) ResolveEntity(_ context.Context, a, b ExtractedEntity) (bool, error) {
	s.calls++
	s.a = a
	s.b = b
	return s.same, nil
}

func TestResolveExactMatch(t *testing.T) {
	r := NewResolver(nil)
	existing := []*domain.Entity{
		{ID: "1", Name: "Alice Johnson", Type: "person", Summary: "Engineer"},
		{ID: "2", Name: "Project Alpha", Type: "project", Summary: "Main project"},
	}

	// Exact match (case-insensitive)
	match, found := r.Resolve(context.Background(), ExtractedEntity{Name: "alice johnson", Type: "person"}, existing)
	if !found {
		t.Fatal("expected exact match")
	}
	if match.ID != "1" {
		t.Fatalf("expected ID 1, got %s", match.ID)
	}

	// Exact match with extra whitespace
	match, found = r.Resolve(context.Background(), ExtractedEntity{Name: "  Alice   Johnson  ", Type: "person"}, existing)
	if !found {
		t.Fatal("expected exact match with whitespace normalization")
	}
	if match.ID != "1" {
		t.Fatalf("expected ID 1, got %s", match.ID)
	}
}

func TestResolveFuzzyMatch(t *testing.T) {
	r := NewResolver(nil)
	existing := []*domain.Entity{
		{ID: "1", Name: "Alice Johnson", Type: "person", Summary: "Engineer"},
	}

	// Fuzzy match (slight typo)
	match, found := r.Resolve(context.Background(), ExtractedEntity{Name: "Alice Johnsn", Type: "person"}, existing)
	if !found {
		t.Fatal("expected fuzzy match for slight typo")
	}
	if match.ID != "1" {
		t.Fatalf("expected ID 1, got %s", match.ID)
	}
}

func TestResolveNoMatch(t *testing.T) {
	r := NewResolver(nil)
	existing := []*domain.Entity{
		{ID: "1", Name: "Alice Johnson", Type: "person", Summary: "Engineer"},
	}

	// No match
	_, found := r.Resolve(context.Background(), ExtractedEntity{Name: "Bob Smith", Type: "person"}, existing)
	if found {
		t.Fatal("expected no match for completely different name")
	}
}

func TestResolveEmptyExisting(t *testing.T) {
	r := NewResolver(nil)
	_, found := r.Resolve(context.Background(), ExtractedEntity{Name: "Alice"}, nil)
	if found {
		t.Fatal("expected no match with empty existing list")
	}
}

func TestResolveTypeConflictUsesLLMContext(t *testing.T) {
	llm := &stubResolverLLM{same: false}
	r := NewResolver(llm)
	existing := []*domain.Entity{
		{ID: "1", Name: "Phoenix", Type: "project", Summary: "Migration tooling"},
	}

	_, found := r.Resolve(context.Background(), ExtractedEntity{
		Name:    "Phoenix",
		Type:    "person",
		Summary: "Staff engineer",
	}, existing)
	if found {
		t.Fatal("expected type conflict to avoid automatic merge")
	}
	if llm.calls != 1 {
		t.Fatalf("expected LLM fallback once, got %d", llm.calls)
	}
	if llm.a.Summary != "Staff engineer" {
		t.Fatalf("expected candidate summary to be passed to LLM, got %q", llm.a.Summary)
	}
	if llm.b.Summary != "Migration tooling" {
		t.Fatalf("expected existing summary to be passed to LLM, got %q", llm.b.Summary)
	}
}

func TestJaroWinkler(t *testing.T) {
	tests := []struct {
		a, b string
		min  float64
	}{
		{"", "", 1.0},
		{"abc", "abc", 1.0},
		{"alice", "alicee", 0.9},
		{"completely", "different", 0.0},
	}

	for _, tt := range tests {
		score := jaroWinkler(tt.a, tt.b)
		if score < tt.min {
			t.Errorf("jaroWinkler(%q, %q) = %.3f, want >= %.3f", tt.a, tt.b, score, tt.min)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"  Hello  World  ", "hello world"},
		{"UPPER", "upper"},
		{"already normal", "already normal"},
		{"  ", ""},
	}

	for _, tt := range tests {
		got := NormalizeName(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
