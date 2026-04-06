package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/lifecycle"
	"github.com/vndee/memex/internal/search"
	"github.com/vndee/memex/internal/storage"
	"github.com/vndee/memex/internal/vecstore"
)

const (
	defaultListLimit = 50
	maxListLimit     = 1000
	maxContentBytes  = 1 << 20 // 1 MiB
)

// MCPServer wraps all dependencies needed by MCP tool handlers.
type MCPServer struct {
	store        storage.Store
	sched        *ingestion.Scheduler
	searcher     *search.Searcher
	lcManager    *lifecycle.Manager
	consolidator *lifecycle.Consolidator
	server       *mcp.Server
}

// NewMCPServer creates an MCP server with all memex tools registered.
func NewMCPServer(
	store storage.Store,
	sched *ingestion.Scheduler,
	searcher *search.Searcher,
	lcManager *lifecycle.Manager,
	consolidator *lifecycle.Consolidator,
	version string,
) *MCPServer {
	s := &MCPServer{
		store:        store,
		sched:        sched,
		searcher:     searcher,
		lcManager:    lcManager,
		consolidator: consolidator,
	}

	s.server = mcp.NewServer(&mcp.Implementation{
		Name:    "memex",
		Version: version,
		Title:   "Memex — Temporal Knowledge Graph Memory",
	}, nil)

	s.registerTools()
	return s
}

// Run starts the MCP server on stdio transport. Blocks until the client disconnects.
func (s *MCPServer) Run(ctx context.Context) error {
	slog.Info("starting MCP server on stdio")
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

func (s *MCPServer) registerTools() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_kb_create",
		Description: "Create a new knowledge base with embedding and LLM provider configuration",
	}, s.handleKBCreate)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_kb_list",
		Description: "List all knowledge bases and their configurations",
	}, s.handleKBList)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_store",
		Description: "Store a memory (text) in a knowledge base. Triggers entity/relation extraction and embedding.",
	}, s.handleStore)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_search",
		Description: "Hybrid search across a knowledge base using BM25 + vector similarity + graph traversal with RRF fusion",
	}, s.handleSearch)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_entities",
		Description: "List or search entities in a knowledge base",
	}, s.handleEntities)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_relations",
		Description: "Get relations for an entity or list all relations in a knowledge base",
	}, s.handleRelations)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_delete",
		Description: "Delete an entity, relation, or episode by ID from a knowledge base",
	}, s.handleDelete)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_stats",
		Description: "Get statistics for a knowledge base or global system stats",
	}, s.handleStats)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_lifecycle_decay",
		Description: "Run memory decay across a knowledge base, reducing strength of unaccessed memories",
	}, s.handleLifecycleDecay)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_lifecycle_prune",
		Description: "Remove weak memories below a strength threshold from a knowledge base",
	}, s.handleLifecyclePrune)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_lifecycle_consolidate",
		Description: "Find and merge duplicate entities based on embedding similarity",
	}, s.handleLifecycleConsolidate)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_job_list",
		Description: "List ingestion jobs with optional filters by knowledge base and status",
	}, s.handleJobList)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_job_get",
		Description: "Get details of a specific ingestion job by ID",
	}, s.handleJobGet)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_job_retry",
		Description: "Retry a failed ingestion job",
	}, s.handleJobRetry)

	// --- Feedback tools ---

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_feedback_record",
		Description: "Record feedback or a correction on a memory. Used for closed-loop learning when the AI gets something wrong.",
	}, s.handleFeedbackRecord)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_feedback_search",
		Description: "Search past feedback and corrections to avoid repeating mistakes",
	}, s.handleFeedbackSearch)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_feedback_stats",
		Description: "Get aggregate feedback statistics for a knowledge base",
	}, s.handleFeedbackStats)

	// --- Parity tools (previously HTTP-only) ---

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_kb_get",
		Description: "Get a specific knowledge base by ID",
	}, s.handleKBGet)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_kb_delete",
		Description: "Delete a knowledge base and all its data",
	}, s.handleKBDelete)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_episode_list",
		Description: "List episodes (stored memories) in a knowledge base",
	}, s.handleEpisodeList)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_episode_get",
		Description: "Get a specific episode by ID from a knowledge base",
	}, s.handleEpisodeGet)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_entity_get",
		Description: "Get a specific entity by ID from a knowledge base",
	}, s.handleEntityGet)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_relation_get",
		Description: "Get a specific relation by ID from a knowledge base",
	}, s.handleRelationGet)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "memex_community_list",
		Description: "List communities (entity clusters) in a knowledge base",
	}, s.handleCommunityList)
}

// --- Input/Output types ---

type kbCreateInput struct {
	ID            string `json:"id" jsonschema:"unique knowledge base identifier"`
	Name          string `json:"name,omitempty" jsonschema:"display name (defaults to ID)"`
	Description   string `json:"description,omitempty" jsonschema:"knowledge base description"`
	EmbedProvider string `json:"embed_provider,omitempty" jsonschema:"embedding provider: ollama, openai, gemini. Default: ollama"`
	EmbedModel    string `json:"embed_model,omitempty" jsonschema:"embedding model name. Default: nomic-embed-text"`
	LLMProvider   string `json:"llm_provider,omitempty" jsonschema:"LLM provider for extraction: ollama, openai, gemini. Default: ollama"`
	LLMModel      string `json:"llm_model,omitempty" jsonschema:"LLM model name. Default: llama3.2"`
}

type kbCreateOutput struct {
	KB *domain.KnowledgeBase `json:"kb"`
}

type kbListInput struct{}

type kbListOutput struct {
	KnowledgeBases []*domain.KnowledgeBase `json:"knowledge_bases"`
}

type storeInput struct {
	KB       string            `json:"kb" jsonschema:"knowledge base ID"`
	Content  string            `json:"content" jsonschema:"text content to store"`
	Source   string            `json:"source,omitempty" jsonschema:"source identifier (e.g. mcp, api, cli). Default: mcp"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"optional metadata key-value pairs"`
}

type storeOutput struct {
	Job *domain.IngestionJob `json:"job"`
}

type searchInput struct {
	KB    string `json:"kb" jsonschema:"knowledge base ID"`
	Query string `json:"query" jsonschema:"search query text"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"max results to return. Default: 10"`
	Mode  string `json:"mode,omitempty" jsonschema:"search mode: hybrid, bm25, or vector. Default: hybrid"`
}

type searchOutput struct {
	Results []*domain.SearchResult `json:"results"`
}

type entitiesInput struct {
	KB     string `json:"kb" jsonschema:"knowledge base ID"`
	Name   string `json:"name,omitempty" jsonschema:"filter by entity name (fuzzy match)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results. Default: 50"`
	Offset int    `json:"offset,omitempty" jsonschema:"pagination offset. Default: 0"`
}

type entitiesOutput struct {
	Entities []*domain.Entity `json:"entities"`
}

type relationsInput struct {
	KB       string `json:"kb" jsonschema:"knowledge base ID"`
	EntityID string `json:"entity_id,omitempty" jsonschema:"get relations for this entity ID"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max results. Default: 50"`
	Offset   int    `json:"offset,omitempty" jsonschema:"pagination offset. Default: 0"`
}

type relationsOutput struct {
	Relations []*domain.Relation `json:"relations"`
}

type deleteInput struct {
	KB   string `json:"kb" jsonschema:"knowledge base ID"`
	ID   string `json:"id" jsonschema:"entity, relation, or episode ID to delete"`
	Type string `json:"type" jsonschema:"item type: entity, relation, or episode"`
}

type deleteOutput struct {
	Deleted bool   `json:"deleted"`
	Message string `json:"message"`
}

type statsInput struct {
	KB string `json:"kb,omitempty" jsonschema:"knowledge base ID (empty for global stats)"`
}

type statsOutput struct {
	Stats *domain.MemoryStats `json:"stats"`
}

type lifecycleDecayInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
}

type lifecycleDecayOutput struct {
	Updated int64  `json:"updated"`
	Message string `json:"message"`
}

type lifecyclePruneInput struct {
	KB        string  `json:"kb" jsonschema:"knowledge base ID"`
	Threshold float64 `json:"threshold,omitempty" jsonschema:"strength threshold below which items are pruned. Default: 0.05"`
}

type lifecyclePruneOutput struct {
	Pruned  int    `json:"pruned"`
	Message string `json:"message"`
}

type lifecycleConsolidateInput struct {
	KB        string  `json:"kb" jsonschema:"knowledge base ID"`
	Threshold float64 `json:"threshold,omitempty" jsonschema:"cosine similarity threshold for merging. Default: 0.92"`
}

type lifecycleConsolidateOutput struct {
	Result *lifecycle.ConsolidationResult `json:"result"`
}

type jobListInput struct {
	KB     string `json:"kb,omitempty" jsonschema:"filter by knowledge base ID"`
	Status string `json:"status,omitempty" jsonschema:"filter by status: queued, running, completed, failed"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results (default 50)"`
}

type jobListOutput struct {
	Jobs []*domain.IngestionJob `json:"jobs"`
}

type jobGetInput struct {
	ID string `json:"id" jsonschema:"job ID"`
}

type jobGetOutput struct {
	Job *domain.IngestionJob `json:"job"`
}

type jobRetryInput struct {
	ID string `json:"id" jsonschema:"job ID to retry"`
}

type jobRetryOutput struct {
	Job *domain.IngestionJob `json:"job"`
}

// --- Feedback types ---

type feedbackRecordInput struct {
	KB         string `json:"kb" jsonschema:"knowledge base ID"`
	Topic      string `json:"topic,omitempty" jsonschema:"feedback topic/category"`
	Content    string `json:"content" jsonschema:"what went wrong or what the feedback is about"`
	Correction string `json:"correction,omitempty" jsonschema:"the correct answer or fix"`
}

type feedbackRecordOutput struct {
	Feedback *domain.Feedback `json:"feedback"`
}

type feedbackSearchInput struct {
	KB    string `json:"kb" jsonschema:"knowledge base ID"`
	Query string `json:"query" jsonschema:"search query for feedback"`
	Topic string `json:"topic,omitempty" jsonschema:"filter by topic"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results. Default: 10"`
}

type feedbackSearchOutput struct {
	Feedback []*domain.Feedback `json:"feedback"`
}

type feedbackStatsInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
}

type feedbackStatsOutput struct {
	Stats *domain.FeedbackStats `json:"stats"`
}

// --- Parity types ---

type kbGetInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
}

type kbGetOutput struct {
	KB *domain.KnowledgeBase `json:"kb"`
}

type kbDeleteInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID to delete"`
}

type kbDeleteOutput struct {
	Message string `json:"message"`
}

type episodeListInput struct {
	KB     string `json:"kb" jsonschema:"knowledge base ID"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results. Default: 50"`
	Offset int    `json:"offset,omitempty" jsonschema:"pagination offset. Default: 0"`
}

type episodeListOutput struct {
	Episodes []*domain.Episode `json:"episodes"`
}

type episodeGetInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
	ID string `json:"id" jsonschema:"episode ID"`
}

type episodeGetOutput struct {
	Episode *domain.Episode `json:"episode"`
}

type entityGetInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
	ID string `json:"id" jsonschema:"entity ID"`
}

type entityGetOutput struct {
	Entity *domain.Entity `json:"entity"`
}

type relationGetInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
	ID string `json:"id" jsonschema:"relation ID"`
}

type relationGetOutput struct {
	Relation *domain.Relation `json:"relation"`
}

type communityListInput struct {
	KB string `json:"kb" jsonschema:"knowledge base ID"`
}

type communityListOutput struct {
	Communities []*domain.Community `json:"communities"`
}

// --- Tool handlers ---

func (s *MCPServer) handleKBCreate(ctx context.Context, req *mcp.CallToolRequest, input kbCreateInput) (*mcp.CallToolResult, kbCreateOutput, error) {
	if input.ID == "" {
		return nil, kbCreateOutput{}, fmt.Errorf("id is required")
	}

	kb := buildKB(input.ID, input.Name, input.Description, input.EmbedProvider, input.EmbedModel, input.LLMProvider, input.LLMModel)

	if err := s.store.CreateKB(ctx, kb); err != nil {
		return nil, kbCreateOutput{}, fmt.Errorf("create KB %q: %w", input.ID, err)
	}

	// Sanitize API keys before returning.
	sanitizeKB(kb)

	return textResult(fmt.Sprintf("Created knowledge base: %s (embed: %s/%s, llm: %s/%s)",
		kb.ID, kb.EmbedConfig.Provider, kb.EmbedConfig.Model, kb.LLMConfig.Provider, kb.LLMConfig.Model)), kbCreateOutput{KB: kb}, nil
}

func (s *MCPServer) handleKBList(ctx context.Context, req *mcp.CallToolRequest, _ kbListInput) (*mcp.CallToolResult, kbListOutput, error) {
	kbs, err := s.store.ListKBs(ctx)
	if err != nil {
		return nil, kbListOutput{}, fmt.Errorf("list KBs: %w", err)
	}

	for _, kb := range kbs {
		sanitizeKB(kb)
	}

	if len(kbs) == 0 {
		return textResult("No knowledge bases found. Use memex_kb_create to create one."), kbListOutput{KnowledgeBases: kbs}, nil
	}

	return nil, kbListOutput{KnowledgeBases: kbs}, nil
}

func (s *MCPServer) handleStore(ctx context.Context, req *mcp.CallToolRequest, input storeInput) (*mcp.CallToolResult, storeOutput, error) {
	if input.KB == "" {
		return nil, storeOutput{}, fmt.Errorf("kb is required")
	}
	if input.Content == "" {
		return nil, storeOutput{}, fmt.Errorf("content is required")
	}
	if len(input.Content) > maxContentBytes {
		return nil, storeOutput{}, fmt.Errorf("content exceeds maximum size of %d bytes", maxContentBytes)
	}

	source := input.Source
	if source == "" {
		source = "mcp"
	}

	job, err := s.sched.Submit(ctx, input.KB, input.Content, ingestion.IngestOptions{
		Source:   source,
		Metadata: input.Metadata,
	})
	if err != nil {
		return nil, storeOutput{}, fmt.Errorf("store in KB %q: %w", input.KB, err)
	}

	return textResult(fmt.Sprintf("Stored: job=%s status=%s episode=%s",
		job.ID, job.Status, job.EpisodeID)), storeOutput{Job: job}, nil
}

func (s *MCPServer) handleSearch(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, searchOutput, error) {
	if input.KB == "" {
		return nil, searchOutput{}, fmt.Errorf("kb is required")
	}
	if input.Query == "" {
		return nil, searchOutput{}, fmt.Errorf("query is required")
	}

	opts := search.DefaultOptions()
	if input.TopK > 0 {
		opts.TopK = input.TopK
	}

	channels, err := search.ParseChannels(input.Mode)
	if err != nil {
		return nil, searchOutput{}, err
	}
	opts.Channels = channels

	results, err := s.searcher.Search(ctx, input.KB, input.Query, opts)
	if err != nil {
		return nil, searchOutput{}, fmt.Errorf("search KB %q: %w", input.KB, err)
	}

	if len(results) == 0 {
		return textResult("No results found."), searchOutput{Results: results}, nil
	}

	// Build human-readable text for the LLM.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d results:\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. [%s] %s (score: %.4f)\n", i+1, r.Type, truncateContent(r.Content, 200), r.Score)
	}

	return textResult(sb.String()), searchOutput{Results: results}, nil
}

func (s *MCPServer) handleEntities(ctx context.Context, req *mcp.CallToolRequest, input entitiesInput) (*mcp.CallToolResult, entitiesOutput, error) {
	if input.KB == "" {
		return nil, entitiesOutput{}, fmt.Errorf("kb is required")
	}

	limit := clampLimit(input.Limit)

	var entities []*domain.Entity
	var err error

	if input.Name != "" {
		entities, err = s.store.FindEntitiesByName(ctx, input.KB, input.Name)
		if len(entities) > limit {
			entities = entities[:limit]
		}
	} else {
		entities, err = s.store.ListEntities(ctx, input.KB, limit, input.Offset)
	}
	if err != nil {
		return nil, entitiesOutput{}, fmt.Errorf("list entities in KB %q: %w", input.KB, err)
	}

	// Strip embeddings from response (large, not useful for LLM).
	for _, e := range entities {
		e.Embedding = nil
	}

	return nil, entitiesOutput{Entities: entities}, nil
}

func (s *MCPServer) handleRelations(ctx context.Context, req *mcp.CallToolRequest, input relationsInput) (*mcp.CallToolResult, relationsOutput, error) {
	if input.KB == "" {
		return nil, relationsOutput{}, fmt.Errorf("kb is required")
	}

	limit := clampLimit(input.Limit)

	var relations []*domain.Relation
	var err error

	if input.EntityID != "" {
		relations, err = s.store.GetRelationsForEntity(ctx, input.KB, input.EntityID)
		if len(relations) > limit {
			relations = relations[:limit]
		}
	} else {
		relations, err = s.store.ListRelations(ctx, input.KB, limit, input.Offset)
	}
	if err != nil {
		return nil, relationsOutput{}, fmt.Errorf("list relations in KB %q: %w", input.KB, err)
	}

	// Strip embeddings from response.
	for _, r := range relations {
		r.Embedding = nil
	}

	return nil, relationsOutput{Relations: relations}, nil
}

func (s *MCPServer) handleDelete(ctx context.Context, req *mcp.CallToolRequest, input deleteInput) (*mcp.CallToolResult, deleteOutput, error) {
	if input.KB == "" || input.ID == "" {
		return nil, deleteOutput{}, fmt.Errorf("kb and id are required")
	}

	var err error
	switch input.Type {
	case domain.ItemEntity:
		err = s.store.DeleteEntity(ctx, input.KB, input.ID)
	case domain.ItemRelation:
		err = s.store.InvalidateRelation(ctx, input.KB, input.ID, time.Now().UTC())
	case domain.ItemEpisode:
		err = s.store.DeleteEpisode(ctx, input.KB, input.ID)
	default:
		return nil, deleteOutput{}, fmt.Errorf("unknown type %q (use entity, relation, or episode)", input.Type)
	}

	if err != nil {
		return nil, deleteOutput{}, fmt.Errorf("delete %s %q: %w", input.Type, input.ID, err)
	}

	msg := fmt.Sprintf("Deleted %s %s from KB %s", input.Type, input.ID, input.KB)
	if input.Type == domain.ItemRelation {
		msg = fmt.Sprintf("Invalidated relation %s in KB %s", input.ID, input.KB)
	}

	return textResult(msg), deleteOutput{Deleted: true, Message: msg}, nil
}

func (s *MCPServer) handleStats(ctx context.Context, req *mcp.CallToolRequest, input statsInput) (*mcp.CallToolResult, statsOutput, error) {
	stats, err := s.store.GetStats(ctx, input.KB)
	if err != nil {
		return nil, statsOutput{}, fmt.Errorf("get stats: %w", err)
	}

	b, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return nil, statsOutput{}, fmt.Errorf("marshal stats: %w", err)
	}
	return textResult(string(b)), statsOutput{Stats: stats}, nil
}

func (s *MCPServer) handleLifecycleDecay(ctx context.Context, req *mcp.CallToolRequest, input lifecycleDecayInput) (*mcp.CallToolResult, lifecycleDecayOutput, error) {
	if input.KB == "" {
		return nil, lifecycleDecayOutput{}, fmt.Errorf("kb is required")
	}

	updated, err := s.lcManager.DecayKB(ctx, input.KB)
	if err != nil {
		return nil, lifecycleDecayOutput{}, fmt.Errorf("decay KB %q: %w", input.KB, err)
	}

	msg := fmt.Sprintf("Decay complete for KB %s: %d items updated", input.KB, updated)
	return textResult(msg), lifecycleDecayOutput{Updated: updated, Message: msg}, nil
}

func (s *MCPServer) handleLifecyclePrune(ctx context.Context, req *mcp.CallToolRequest, input lifecyclePruneInput) (*mcp.CallToolResult, lifecyclePruneOutput, error) {
	if input.KB == "" {
		return nil, lifecyclePruneOutput{}, fmt.Errorf("kb is required")
	}

	pruned, err := s.lcManager.PruneKB(ctx, input.KB, input.Threshold)
	if err != nil {
		return nil, lifecyclePruneOutput{}, fmt.Errorf("prune KB %q: %w", input.KB, err)
	}

	msg := fmt.Sprintf("Prune complete for KB %s: %d items removed", input.KB, pruned)
	return textResult(msg), lifecyclePruneOutput{Pruned: pruned, Message: msg}, nil
}

func (s *MCPServer) handleLifecycleConsolidate(ctx context.Context, req *mcp.CallToolRequest, input lifecycleConsolidateInput) (*mcp.CallToolResult, lifecycleConsolidateOutput, error) {
	if input.KB == "" {
		return nil, lifecycleConsolidateOutput{}, fmt.Errorf("kb is required")
	}

	result, err := s.consolidator.RunConsolidation(ctx, input.KB)
	if err != nil {
		return nil, lifecycleConsolidateOutput{}, fmt.Errorf("consolidate KB %q: %w", input.KB, err)
	}

	msg := fmt.Sprintf("Consolidation complete for KB %s: %d/%d candidates merged, %d relations fixed",
		input.KB, result.Merged, result.Candidates, result.RelationsFixed)
	return textResult(msg), lifecycleConsolidateOutput{Result: result}, nil
}

func (s *MCPServer) handleJobList(ctx context.Context, req *mcp.CallToolRequest, input jobListInput) (*mcp.CallToolResult, jobListOutput, error) {
	limit := clampLimit(input.Limit)

	jobs, err := s.store.ListJobs(ctx, input.KB, input.Status, limit)
	if err != nil {
		return nil, jobListOutput{}, fmt.Errorf("list jobs: %w", err)
	}

	if len(jobs) == 0 {
		return textResult("No jobs found."), jobListOutput{Jobs: jobs}, nil
	}

	counts := make(map[string]int)
	for _, j := range jobs {
		counts[j.Status]++
	}
	summary := fmt.Sprintf("Found %d jobs", len(jobs))
	for status, n := range counts {
		summary += fmt.Sprintf(", %d %s", n, status)
	}
	return textResult(summary), jobListOutput{Jobs: jobs}, nil
}

func (s *MCPServer) handleJobGet(ctx context.Context, req *mcp.CallToolRequest, input jobGetInput) (*mcp.CallToolResult, jobGetOutput, error) {
	if input.ID == "" {
		return nil, jobGetOutput{}, fmt.Errorf("id is required")
	}

	job, err := s.store.GetJob(ctx, input.ID)
	if err != nil {
		return nil, jobGetOutput{}, fmt.Errorf("get job %q: %w", input.ID, err)
	}

	summary := fmt.Sprintf("Job %s: status=%s source=%s attempts=%d/%d",
		job.ID, job.Status, job.Source, job.Attempts, job.MaxAttempts)
	if job.Error != "" {
		summary += fmt.Sprintf(" error=%s", job.Error)
	}
	return textResult(summary), jobGetOutput{Job: job}, nil
}

func (s *MCPServer) handleJobRetry(ctx context.Context, req *mcp.CallToolRequest, input jobRetryInput) (*mcp.CallToolResult, jobRetryOutput, error) {
	if input.ID == "" {
		return nil, jobRetryOutput{}, fmt.Errorf("id is required")
	}

	job, err := s.sched.RetryJob(ctx, input.ID)
	if err != nil {
		return nil, jobRetryOutput{}, fmt.Errorf("retry job %q: %w", input.ID, err)
	}

	msg := fmt.Sprintf("Retried job %s, new status: %s", job.ID, job.Status)
	return textResult(msg), jobRetryOutput{Job: job}, nil
}

// --- Feedback handlers ---

func (s *MCPServer) handleFeedbackRecord(ctx context.Context, req *mcp.CallToolRequest, input feedbackRecordInput) (*mcp.CallToolResult, feedbackRecordOutput, error) {
	if input.KB == "" {
		return nil, feedbackRecordOutput{}, fmt.Errorf("kb is required")
	}
	if input.Content == "" {
		return nil, feedbackRecordOutput{}, fmt.Errorf("content is required")
	}

	fb := domain.NewFeedback(input.KB, input.Topic, input.Content, input.Correction, "mcp")

	if err := s.store.CreateFeedback(ctx, fb); err != nil {
		return nil, feedbackRecordOutput{}, fmt.Errorf("record feedback: %w", err)
	}

	msg := fmt.Sprintf("Recorded feedback %s in KB %s", fb.ID, input.KB)
	if input.Topic != "" {
		msg += fmt.Sprintf(" (topic: %s)", input.Topic)
	}
	return textResult(msg), feedbackRecordOutput{Feedback: fb}, nil
}

func (s *MCPServer) handleFeedbackSearch(ctx context.Context, req *mcp.CallToolRequest, input feedbackSearchInput) (*mcp.CallToolResult, feedbackSearchOutput, error) {
	if input.KB == "" {
		return nil, feedbackSearchOutput{}, fmt.Errorf("kb is required")
	}

	limit := clampLimit(input.Limit)

	var feedback []*domain.Feedback
	var err error

	if input.Query != "" {
		feedback, err = s.store.SearchFeedback(ctx, input.KB, input.Query, limit)
	} else {
		feedback, err = s.store.ListFeedbackByTopic(ctx, input.KB, input.Topic, limit)
	}
	if err != nil {
		return nil, feedbackSearchOutput{}, fmt.Errorf("search feedback: %w", err)
	}

	if len(feedback) == 0 {
		return textResult("No feedback found."), feedbackSearchOutput{Feedback: feedback}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d feedback entries:\n", len(feedback))
	for i, fb := range feedback {
		fmt.Fprintf(&sb, "%d. [%s] %s", i+1, fb.Topic, fb.Content)
		if fb.Correction != "" {
			fmt.Fprintf(&sb, " → %s", fb.Correction)
		}
		sb.WriteByte('\n')
	}
	return textResult(sb.String()), feedbackSearchOutput{Feedback: feedback}, nil
}

func (s *MCPServer) handleFeedbackStats(ctx context.Context, req *mcp.CallToolRequest, input feedbackStatsInput) (*mcp.CallToolResult, feedbackStatsOutput, error) {
	if input.KB == "" {
		return nil, feedbackStatsOutput{}, fmt.Errorf("kb is required")
	}

	stats, err := s.store.GetFeedbackStats(ctx, input.KB)
	if err != nil {
		return nil, feedbackStatsOutput{}, fmt.Errorf("feedback stats: %w", err)
	}

	b, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return nil, feedbackStatsOutput{}, fmt.Errorf("marshal feedback stats: %w", err)
	}
	return textResult(string(b)), feedbackStatsOutput{Stats: stats}, nil
}

// --- Parity handlers ---

func (s *MCPServer) handleKBGet(ctx context.Context, req *mcp.CallToolRequest, input kbGetInput) (*mcp.CallToolResult, kbGetOutput, error) {
	if input.KB == "" {
		return nil, kbGetOutput{}, fmt.Errorf("kb is required")
	}

	kb, err := s.store.GetKB(ctx, input.KB)
	if err != nil {
		return nil, kbGetOutput{}, fmt.Errorf("get KB %q: %w", input.KB, err)
	}

	sanitizeKB(kb)
	return nil, kbGetOutput{KB: kb}, nil
}

func (s *MCPServer) handleKBDelete(ctx context.Context, req *mcp.CallToolRequest, input kbDeleteInput) (*mcp.CallToolResult, kbDeleteOutput, error) {
	if input.KB == "" {
		return nil, kbDeleteOutput{}, fmt.Errorf("kb is required")
	}

	if err := s.store.DeleteKB(ctx, input.KB); err != nil {
		return nil, kbDeleteOutput{}, fmt.Errorf("delete KB %q: %w", input.KB, err)
	}

	msg := fmt.Sprintf("Deleted knowledge base %s", input.KB)
	return textResult(msg), kbDeleteOutput{Message: msg}, nil
}

func (s *MCPServer) handleEpisodeList(ctx context.Context, req *mcp.CallToolRequest, input episodeListInput) (*mcp.CallToolResult, episodeListOutput, error) {
	if input.KB == "" {
		return nil, episodeListOutput{}, fmt.Errorf("kb is required")
	}

	limit := clampLimit(input.Limit)
	episodes, err := s.store.ListEpisodes(ctx, input.KB, limit, input.Offset)
	if err != nil {
		return nil, episodeListOutput{}, fmt.Errorf("list episodes in KB %q: %w", input.KB, err)
	}

	return nil, episodeListOutput{Episodes: episodes}, nil
}

func (s *MCPServer) handleEpisodeGet(ctx context.Context, req *mcp.CallToolRequest, input episodeGetInput) (*mcp.CallToolResult, episodeGetOutput, error) {
	if input.KB == "" || input.ID == "" {
		return nil, episodeGetOutput{}, fmt.Errorf("kb and id are required")
	}

	ep, err := s.store.GetEpisode(ctx, input.KB, input.ID)
	if err != nil {
		return nil, episodeGetOutput{}, fmt.Errorf("get episode %q: %w", input.ID, err)
	}

	return nil, episodeGetOutput{Episode: ep}, nil
}

func (s *MCPServer) handleEntityGet(ctx context.Context, req *mcp.CallToolRequest, input entityGetInput) (*mcp.CallToolResult, entityGetOutput, error) {
	if input.KB == "" || input.ID == "" {
		return nil, entityGetOutput{}, fmt.Errorf("kb and id are required")
	}

	entity, err := s.store.GetEntity(ctx, input.KB, input.ID)
	if err != nil {
		return nil, entityGetOutput{}, fmt.Errorf("get entity %q: %w", input.ID, err)
	}

	entity.Embedding = nil
	return nil, entityGetOutput{Entity: entity}, nil
}

func (s *MCPServer) handleRelationGet(ctx context.Context, req *mcp.CallToolRequest, input relationGetInput) (*mcp.CallToolResult, relationGetOutput, error) {
	if input.KB == "" || input.ID == "" {
		return nil, relationGetOutput{}, fmt.Errorf("kb and id are required")
	}

	rel, err := s.store.GetRelation(ctx, input.KB, input.ID)
	if err != nil {
		return nil, relationGetOutput{}, fmt.Errorf("get relation %q: %w", input.ID, err)
	}

	rel.Embedding = nil
	return nil, relationGetOutput{Relation: rel}, nil
}

func (s *MCPServer) handleCommunityList(ctx context.Context, req *mcp.CallToolRequest, input communityListInput) (*mcp.CallToolResult, communityListOutput, error) {
	if input.KB == "" {
		return nil, communityListOutput{}, fmt.Errorf("kb is required")
	}

	communities, err := s.store.ListCommunities(ctx, input.KB)
	if err != nil {
		return nil, communityListOutput{}, fmt.Errorf("list communities in KB %q: %w", input.KB, err)
	}

	return nil, communityListOutput{Communities: communities}, nil
}

// --- Helpers ---

// textResult creates a CallToolResult with a single text content.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// sanitizeKB removes sensitive fields (API keys) from a KB before returning to clients.
func sanitizeKB(kb *domain.KnowledgeBase) {
	kb.EmbedConfig.APIKey = ""
	kb.LLMConfig.APIKey = ""
}

// truncateContent truncates text for human-readable MCP text responses.
func truncateContent(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// clampLimit applies default and max bounds to a user-provided limit.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// NewSearcher is a convenience to build a Searcher from components
// (used by both CLI and MCP server).
func NewSearcher(
	store storage.Store,
	decayHalfLife float64,
	embedFn search.EmbedderFactory,
) *search.Searcher {
	vecEng := vecstore.NewEngine(vecstore.EngineConfig{})
	graphSt := graph.NewStore()
	return search.New(store, vecEng, graphSt, embedFn, decayHalfLife)
}
