package ingestion_test

import (
	"context"
	"errors"
	"testing"
	"time"

	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/storage"
)

func init() {
	storage.MigrationSQL = memex.MigrationSQL()
}

type fakeEmbedRegistry struct {
	provider embedding.Provider
	err      error
}

func (r fakeEmbedRegistry) NewProvider(domain.EmbedConfig) (embedding.Provider, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.provider, nil
}

type fakeExtractRegistry struct {
	provider extraction.Provider
	err      error
}

func (r fakeExtractRegistry) NewProvider(domain.LLMConfig) (extraction.Provider, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.provider, nil
}

type fakeEmbedder struct {
	batch [][]float32
	err   error
}

func (f *fakeEmbedder) Embed(context.Context, string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.batch) > 0 {
		return f.batch[0], nil
	}
	return []float32{1, 0.5}, nil
}

func (f *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.batch != nil {
		return f.batch, nil
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i + 1), 0.5}
	}
	return out, nil
}

func (f *fakeEmbedder) Dimensions() int {
	if len(f.batch) > 0 {
		return len(f.batch[0])
	}
	return 2
}

type fakeExtractor struct {
	result *extraction.ExtractionResult
	err    error
}

func (f *fakeExtractor) Extract(context.Context, string) (*extraction.ExtractionResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func (f *fakeExtractor) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakeExtractor) ResolveEntity(context.Context, extraction.ExtractedEntity, extraction.ExtractedEntity) (bool, error) {
	return false, nil
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createKB(t *testing.T, store *storage.SQLiteStore, id string) {
	t.Helper()
	err := store.CreateKB(context.Background(), &domain.KnowledgeBase{
		ID:   id,
		Name: id,
		EmbedConfig: domain.EmbedConfig{
			Provider: domain.ProviderOllama,
			Model:    "nomic-embed-text",
		},
		LLMConfig: domain.LLMConfig{
			Provider: domain.ProviderOllama,
			Model:    "llama3.2",
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIngestReturnsErrorWhenExtractorCreationFails(t *testing.T) {
	store := newTestStore(t)
	createKB(t, store, "kb1")

	pipe := ingestion.NewPipeline(
		store,
		fakeEmbedRegistry{provider: &fakeEmbedder{}},
		fakeExtractRegistry{err: errors.New("llm unavailable")},
	)

	result, err := pipe.Ingest(context.Background(), "kb1", "met Alice", ingestion.IngestOptions{Source: "test"})
	if err == nil {
		t.Fatal("expected error from failed extractor creation")
	}

	// Episode should still be created
	eps, err := store.ListEpisodes(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 {
		t.Fatalf("got %d episodes, want 1", len(eps))
	}

	// No entities should be created
	entities, err := store.ListEntities(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %d entities, want 0", len(entities))
	}

	_ = result // result may be partial
}

func TestIngestDeduplicatesCurrentBatchAndNormalizesRelationEndpoints(t *testing.T) {
	store := newTestStore(t)
	createKB(t, store, "kb1")

	pipe := ingestion.NewPipeline(
		store,
		fakeEmbedRegistry{provider: &fakeEmbedder{}},
		fakeExtractRegistry{provider: &fakeExtractor{
			result: &extraction.ExtractionResult{
				Entities: []extraction.ExtractedEntity{
					{Name: "Alice", Type: "person", Summary: "Engineer"},
					{Name: " alice ", Type: "person", Summary: "Senior engineer leading the launch"},
					{Name: "Project Atlas", Type: "project", Summary: "Internal platform"},
				},
				Relations: []extraction.ExtractedRelation{
					{Source: " alice ", Target: "Project   Atlas", Type: "works_on", Summary: "Owns the rollout", Weight: 0.9},
					{Source: "Alice", Target: "Project Atlas", Type: "works_on", Summary: "Owns the rollout", Weight: 0.9},
				},
			},
		}},
	)

	result, err := pipe.Ingest(context.Background(), "kb1", "Alice owns Project Atlas", ingestion.IngestOptions{Source: "test"})
	if err != nil {
		t.Fatalf("ingest returned error: %v", err)
	}
	if result.EntitiesCreated != 2 {
		t.Fatalf("got %d entities created, want 2", result.EntitiesCreated)
	}
	if result.RelationsCreated != 1 {
		t.Fatalf("got %d relations created, want 1", result.RelationsCreated)
	}

	entities, err := store.ListEntities(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2", len(entities))
	}

	foundAlice := false
	for _, entity := range entities {
		if entity.Name == "Alice" {
			foundAlice = true
			if entity.Summary != "Senior engineer leading the launch" {
				t.Fatalf("got summary %q, want merged richer summary", entity.Summary)
			}
		}
	}
	if !foundAlice {
		t.Fatal("expected Alice entity")
	}

	relations, err := store.ListRelations(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 1 {
		t.Fatalf("got %d relations, want 1", len(relations))
	}
}

func TestIngestReturnsErrorWhenEmbeddingBatchIsInvalid(t *testing.T) {
	store := newTestStore(t)
	createKB(t, store, "kb1")

	pipe := ingestion.NewPipeline(
		store,
		fakeEmbedRegistry{provider: &fakeEmbedder{
			batch: [][]float32{
				{1, 0.5},
			},
		}},
		fakeExtractRegistry{provider: &fakeExtractor{
			result: &extraction.ExtractionResult{
				Entities: []extraction.ExtractedEntity{
					{Name: "Alice", Type: "person", Summary: "Engineer"},
					{Name: "Project Atlas", Type: "project", Summary: "Internal platform"},
				},
			},
		}},
	)

	_, err := pipe.Ingest(context.Background(), "kb1", "Alice works on Atlas", ingestion.IngestOptions{Source: "test"})
	if err == nil {
		t.Fatal("expected error from invalid embedding batch")
	}

	// No entities should be persisted when embedding fails
	entities, err := store.ListEntities(context.Background(), "kb1", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %d entities, want 0", len(entities))
	}
}
