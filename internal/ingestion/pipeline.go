package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/storage"
)

// EmbedderFactory creates embedding providers from config.
type EmbedderFactory interface {
	NewProvider(cfg domain.EmbedConfig) (embedding.Provider, error)
}

// ExtractorFactory creates extraction providers from config.
type ExtractorFactory interface {
	NewProvider(cfg domain.LLMConfig) (extraction.Provider, error)
}

// Pipeline orchestrates: text -> episode -> LLM extraction -> entity resolution -> embeddings -> store.
type Pipeline struct {
	store       storage.Store
	embedFact   EmbedderFactory
	extractFact ExtractorFactory

	mu           sync.RWMutex
	embedCache   map[cacheKey]embedding.Provider
	extractCache map[cacheKey]extraction.Provider
}

// cacheKey includes provider config so cache invalidates when KB config changes.
type cacheKey struct {
	kbID     string
	provider string
	model    string
	baseURL  string
	apiKey   string // ensures different API keys don't share cached providers
}

type pendingEntity struct {
	entity *domain.Entity
	isNew  bool
}

type pendingRelation struct {
	rel       *domain.Relation
	embedText string
}

// NewPipeline creates an ingestion pipeline with the given registries.
func NewPipeline(store storage.Store, embedFact EmbedderFactory, extractFact ExtractorFactory) *Pipeline {
	return &Pipeline{
		store:        store,
		embedFact:    embedFact,
		extractFact:  extractFact,
		embedCache:   make(map[cacheKey]embedding.Provider),
		extractCache: make(map[cacheKey]extraction.Provider),
	}
}

func getOrCreate[V any](mu *sync.RWMutex, cache map[cacheKey]V, key cacheKey, create func() (V, error)) (V, error) {
	mu.RLock()
	if v, ok := cache[key]; ok {
		mu.RUnlock()
		return v, nil
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if v, ok := cache[key]; ok {
		return v, nil
	}
	v, err := create()
	if err != nil {
		var zero V
		return zero, err
	}
	cache[key] = v
	return v, nil
}

// IngestOptions configures an ingestion call.
type IngestOptions struct {
	Source   string
	Metadata map[string]string
}

// Ingest processes text into the knowledge graph for the given KB.
func (p *Pipeline) Ingest(ctx context.Context, kbID, text string, opts IngestOptions) (*IngestResult, error) {
	kb, err := p.store.GetKB(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get kb %q: %w", kbID, err)
	}

	now := time.Now().UTC()
	episode := &domain.Episode{
		ID:        ulid.Make().String(),
		KBID:      kbID,
		Content:   text,
		Source:    opts.Source,
		Metadata:  opts.Metadata,
		CreatedAt: now,
	}
	if err := p.store.CreateEpisode(ctx, episode); err != nil {
		return nil, fmt.Errorf("create episode: %w", err)
	}

	result := &IngestResult{EpisodeID: episode.ID}
	defer func() {
		if err := p.store.LogAccess(ctx, kbID, domain.ItemEpisode, episode.ID); err != nil {
			slog.Warn("log access failed", "error", err)
		}
	}()

	extractKey := cacheKey{kbID: kbID, provider: kb.LLMConfig.Provider, model: kb.LLMConfig.Model, baseURL: kb.LLMConfig.BaseURL, apiKey: kb.LLMConfig.APIKey}
	extractor, err := getOrCreate(&p.mu, p.extractCache, extractKey, func() (extraction.Provider, error) {
		return p.extractFact.NewProvider(kb.LLMConfig)
	})
	if err != nil {
		return result, fmt.Errorf("create extractor: %w", err)
	}

	extracted, err := extractor.Extract(ctx, text)
	if err != nil {
		return result, fmt.Errorf("extract: %w", err)
	}

	allEntities, err := p.store.ListEntityNames(ctx, kbID)
	if err != nil {
		slog.Warn("failed to load existing entities, will resolve only within current ingest", "error", err)
		allEntities = nil
	}

	resolver := extraction.NewResolver(extractor)
	knownEntities := append([]*domain.Entity(nil), allEntities...)
	entityMap := make(map[string]string)
	pendingEntities := make([]*pendingEntity, 0, len(extracted.Entities))
	pendingByID := make(map[string]*pendingEntity)

	for _, raw := range extracted.Entities {
		ext := normalizeExtractedEntity(raw)
		if ext.Name == "" {
			continue
		}

		matched, found := resolver.Resolve(ctx, ext, knownEntities)
		if found {
			entityMap[extraction.NormalizeName(ext.Name)] = matched.ID

			if pending, ok := pendingByID[matched.ID]; ok {
				mergeEntity(pending.entity, ext, now)
				continue
			}

			updated := cloneEntity(matched)
			if mergeEntity(updated, ext, now) {
				pending := &pendingEntity{entity: updated, isNew: false}
				pendingEntities = append(pendingEntities, pending)
				pendingByID[updated.ID] = pending
			}
			continue
		}

		entity := &domain.Entity{
			ID:        ulid.Make().String(),
			KBID:      kbID,
			Name:      ext.Name,
			Type:      defaultEntityType(ext.Type),
			Summary:   ext.Summary,
			CreatedAt: now,
			UpdatedAt: now,
		}
		pending := &pendingEntity{entity: entity, isNew: true}
		pendingEntities = append(pendingEntities, pending)
		pendingByID[entity.ID] = pending
		knownEntities = append(knownEntities, entity)
		entityMap[extraction.NormalizeName(ext.Name)] = entity.ID
	}

	pendingRels := make([]*pendingRelation, 0, len(extracted.Relations))
	seenRelations := make(map[string]int) // sig → index in pendingRels
	for _, raw := range extracted.Relations {
		rel := normalizeExtractedRelation(raw)
		if rel.Source == "" || rel.Target == "" || rel.Type == "" {
			continue
		}

		sourceID, ok := resolveEntityID(ctx, resolver, rel.Source, knownEntities, entityMap)
		if !ok {
			continue
		}
		targetID, ok := resolveEntityID(ctx, resolver, rel.Target, knownEntities, entityMap)
		if !ok {
			continue
		}

		sig := relationSignature(sourceID, rel.Type, targetID)
		if idx, exists := seenRelations[sig]; exists {
			// Within-batch duplicate: merge weight and keep better summary.
			existing := pendingRels[idx]
			existing.rel.Weight = domain.CombineWeights(existing.rel.Weight, rel.Weight)
			better := domain.BetterSummary(existing.rel.Summary, rel.Summary)
			if better != existing.rel.Summary {
				existing.rel.Summary = better
				existing.embedText = buildRelationEmbeddingText(rel)
			}
			continue
		}
		seenRelations[sig] = len(pendingRels)

		pendingRels = append(pendingRels, &pendingRelation{
			rel: &domain.Relation{
				ID:        ulid.Make().String(),
				KBID:      kbID,
				SourceID:  sourceID,
				TargetID:  targetID,
				Type:      rel.Type,
				Summary:   rel.Summary,
				Weight:    rel.Weight,
				EpisodeID: episode.ID,
				ValidAt:   now,
				CreatedAt: now,
			},
			embedText: buildRelationEmbeddingText(rel),
		})
	}

	if len(pendingEntities) > 0 || len(pendingRels) > 0 {
		embedKey := cacheKey{kbID: kbID, provider: kb.EmbedConfig.Provider, model: kb.EmbedConfig.Model, baseURL: kb.EmbedConfig.BaseURL, apiKey: kb.EmbedConfig.APIKey}
		embedder, err := getOrCreate(&p.mu, p.embedCache, embedKey, func() (embedding.Provider, error) {
			return p.embedFact.NewProvider(kb.EmbedConfig)
		})
		if err != nil {
			return result, fmt.Errorf("create embedder: %w", err)
		}

		if err := assignEntityEmbeddings(ctx, embedder, pendingEntities); err != nil {
			return result, fmt.Errorf("embed entities: %w", err)
		}
		if err := assignRelationEmbeddings(ctx, embedder, pendingRels); err != nil {
			return result, fmt.Errorf("embed relations: %w", err)
		}
	}

	for _, pending := range pendingEntities {
		if pending.isNew {
			if err := p.store.CreateEntity(ctx, pending.entity); err != nil {
				return result, fmt.Errorf("create entity %q: %w", pending.entity.Name, err)
			}
			result.EntitiesCreated++
			continue
		}

		if err := p.store.UpdateEntity(ctx, pending.entity); err != nil {
			return result, fmt.Errorf("update entity %q: %w", pending.entity.ID, err)
		}
		result.EntitiesUpdated++
	}

	for _, pending := range pendingRels {
		created, err := p.store.UpsertRelation(ctx, pending.rel)
		if err != nil {
			return result, fmt.Errorf("upsert relation %q: %w", pending.rel.ID, err)
		}
		if created {
			result.RelationsCreated++
		} else {
			result.RelationsStrengthened++
		}
	}

	return result, nil
}

func assignEntityEmbeddings(ctx context.Context, embedder embedding.Provider, pending []*pendingEntity) error {
	if len(pending) == 0 {
		return nil
	}

	texts := make([]string, len(pending))
	for i, item := range pending {
		texts[i] = buildEntityEmbeddingText(item.entity)
	}

	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}
	if len(embeddings) != len(pending) {
		return fmt.Errorf("entity embeddings count mismatch: got %d want %d", len(embeddings), len(pending))
	}
	for i, emb := range embeddings {
		pending[i].entity.Embedding = emb
	}
	return nil
}

func assignRelationEmbeddings(ctx context.Context, embedder embedding.Provider, pending []*pendingRelation) error {
	if len(pending) == 0 {
		return nil
	}

	texts := make([]string, len(pending))
	for i, item := range pending {
		texts[i] = item.embedText
	}

	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}
	if len(embeddings) != len(pending) {
		return fmt.Errorf("relation embeddings count mismatch: got %d want %d", len(embeddings), len(pending))
	}
	for i, emb := range embeddings {
		pending[i].rel.Embedding = emb
	}
	return nil
}

func normalizeExtractedEntity(ext extraction.ExtractedEntity) extraction.ExtractedEntity {
	ext.Name = collapseWhitespace(ext.Name)
	ext.Type = strings.TrimSpace(ext.Type)
	ext.Summary = collapseWhitespace(ext.Summary)
	return ext
}

func normalizeExtractedRelation(rel extraction.ExtractedRelation) extraction.ExtractedRelation {
	rel.Source = collapseWhitespace(rel.Source)
	rel.Target = collapseWhitespace(rel.Target)
	rel.Type = strings.TrimSpace(rel.Type)
	rel.Summary = collapseWhitespace(rel.Summary)
	return rel
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func cloneEntity(e *domain.Entity) *domain.Entity {
	cloned := *e
	if e.Embedding != nil {
		cloned.Embedding = append([]float32(nil), e.Embedding...)
	}
	return &cloned
}

func mergeEntity(dst *domain.Entity, ext extraction.ExtractedEntity, now time.Time) bool {
	changed := false

	if shouldReplaceEntityType(dst.Type, ext.Type) {
		dst.Type = ext.Type
		changed = true
	}

	if shouldReplaceSummary(dst.Summary, ext.Summary) {
		dst.Summary = ext.Summary
		changed = true
	}

	if changed {
		dst.UpdatedAt = now
	}
	return changed
}

func shouldReplaceEntityType(current, next string) bool {
	next = strings.TrimSpace(next)
	if next == "" {
		return false
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return true
	}
	return strings.EqualFold(current, "concept") && !strings.EqualFold(next, current)
}

func shouldReplaceSummary(current, next string) bool {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" || next == current {
		return false
	}
	return len(next) > len(current)
}

func defaultEntityType(typ string) string {
	if strings.TrimSpace(typ) == "" {
		return "concept"
	}
	return strings.TrimSpace(typ)
}

func buildEntityEmbeddingText(entity *domain.Entity) string {
	if entity.Summary == "" {
		return entity.Name
	}
	return entity.Name + ": " + entity.Summary
}

func buildRelationEmbeddingText(rel extraction.ExtractedRelation) string {
	text := rel.Source + " " + rel.Type + " " + rel.Target
	if rel.Summary == "" {
		return text
	}
	return text + ": " + rel.Summary
}

func resolveEntityID(ctx context.Context, resolver *extraction.Resolver, name string, known []*domain.Entity, entityMap map[string]string) (string, bool) {
	key := extraction.NormalizeName(name)
	if id, ok := entityMap[key]; ok {
		return id, true
	}

	matched, found := resolver.Resolve(ctx, extraction.ExtractedEntity{Name: name}, known)
	if !found {
		return "", false
	}
	entityMap[key] = matched.ID
	return matched.ID, true
}

func relationSignature(sourceID, relType, targetID string) string {
	return strings.Join([]string{sourceID, relType, targetID}, "|")
}

// IngestResult summarizes what happened during ingestion.
type IngestResult struct {
	EpisodeID              string `json:"episode_id"`
	EntitiesCreated        int    `json:"entities_created"`
	EntitiesUpdated        int    `json:"entities_updated"`
	RelationsCreated       int    `json:"relations_created"`
	RelationsStrengthened  int    `json:"relations_strengthened"`
}

func (r *IngestResult) String() string {
	return fmt.Sprintf("episode=%s entities_created=%d entities_updated=%d relations_created=%d relations_strengthened=%d",
		r.EpisodeID, r.EntitiesCreated, r.EntitiesUpdated, r.RelationsCreated, r.RelationsStrengthened)
}
