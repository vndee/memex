package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/lifecycle"
	"github.com/vndee/memex/internal/search"
	"github.com/vndee/memex/internal/storage"
)

// HTTPServer serves the memex REST API.
type HTTPServer struct {
	store        storage.Store
	sched        *ingestion.Scheduler
	searcher     *search.Searcher
	lcManager    *lifecycle.Manager
	consolidator *lifecycle.Consolidator
	router       chi.Router
}

// NewHTTPServer creates a new HTTP server with all routes registered.
func NewHTTPServer(
	store storage.Store,
	sched *ingestion.Scheduler,
	searcher *search.Searcher,
	lcManager *lifecycle.Manager,
	consolidator *lifecycle.Consolidator,
) *HTTPServer {
	s := &HTTPServer{
		store:        store,
		sched:        sched,
		searcher:     searcher,
		lcManager:    lcManager,
		consolidator: consolidator,
	}
	s.router = s.buildRouter()
	return s
}

// Handler returns the http.Handler for use with httptest or custom servers.
func (s *HTTPServer) Handler() http.Handler {
	return s.router
}

// Serve starts the HTTP server with graceful shutdown on context cancellation.
func (s *HTTPServer) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting HTTP server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		slog.Info("shutting down HTTP server")
		return srv.Shutdown(shutCtx)
	}
}

func (s *HTTPServer) buildRouter() chi.Router {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(jsonContentType)

	// Health check
	r.Get("/health", s.handleHealth)

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Global stats
		r.Get("/stats", s.handleGlobalStats)

		// Jobs (global, filterable by kb)
		r.Get("/jobs", s.handleJobList)
		r.Get("/jobs/{id}", s.handleJobGet)
		r.Post("/jobs/{id}/retry", s.handleJobRetry)

		// Knowledge bases
		r.Route("/kb", func(r chi.Router) {
			r.Post("/", s.handleKBCreate)
			r.Get("/", s.handleKBList)

			r.Route("/{kb_id}", func(r chi.Router) {
				r.Get("/", s.handleKBGet)
				r.Delete("/", s.handleKBDelete)

				// Episodes
				r.Post("/episodes", s.handleEpisodeCreate)
				r.Get("/episodes", s.handleEpisodeList)
				r.Get("/episodes/{id}", s.handleEpisodeGet)

				// Entities
				r.Get("/entities", s.handleEntityList)
				r.Get("/entities/{id}", s.handleEntityGet)
				r.Delete("/entities/{id}", s.handleEntityDelete)

				// Relations
				r.Get("/relations", s.handleRelationList)
				r.Get("/relations/{id}", s.handleRelationGet)

				// Feedback
				r.Post("/feedback", s.handleFeedbackCreate)
				r.Get("/feedback", s.handleFeedbackList)
				r.Get("/feedback/stats", s.handleFeedbackStatsHTTP)

				// Search
				r.Get("/search", s.handleSearch)

				// Graph traversal
				r.Get("/graph/traverse", s.handleGraphTraverse)

				// Communities
				r.Get("/communities", s.handleCommunityList)

				// Stats
				r.Get("/stats", s.handleKBStats)

				// Lifecycle
				r.Post("/lifecycle/decay", s.handleLifecycleDecay)
				r.Post("/lifecycle/prune", s.handleLifecyclePrune)
				r.Post("/lifecycle/consolidate", s.handleLifecycleConsolidate)
			})
		})
	})

	return r
}

// jsonContentType sets Content-Type: application/json on all responses.
func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// --- Response helpers ---

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode error", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func parseIntParam(r *http.Request, key string, def, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

// --- Handlers ---

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// KB handlers

type kbHTTPCreateRequest struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	EmbedProvider string `json:"embed_provider,omitempty"`
	EmbedModel    string `json:"embed_model,omitempty"`
	LLMProvider   string `json:"llm_provider,omitempty"`
	LLMModel      string `json:"llm_model,omitempty"`
}

func (s *HTTPServer) handleKBCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxContentBytes)
	var req kbHTTPCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or too-large JSON body")
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	kb := buildKB(req.ID, req.Name, req.Description, req.EmbedProvider, req.EmbedModel, req.LLMProvider, req.LLMModel)

	if err := s.store.CreateKB(r.Context(), kb); err != nil {
		slog.Error("create KB failed", "id", req.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create knowledge base")
		return
	}

	sanitizeKB(kb)
	writeJSON(w, http.StatusCreated, kb)
}

func (s *HTTPServer) handleKBList(w http.ResponseWriter, r *http.Request) {
	kbs, err := s.store.ListKBs(r.Context())
	if err != nil {
		slog.Error("list KBs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list knowledge bases")
		return
	}
	for _, kb := range kbs {
		sanitizeKB(kb)
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledge_bases": kbs})
}

func (s *HTTPServer) handleKBGet(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	kb, err := s.store.GetKB(r.Context(), kbID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("KB %q not found", kbID))
		return
	}
	sanitizeKB(kb)
	writeJSON(w, http.StatusOK, kb)
}

func (s *HTTPServer) handleKBDelete(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	if err := s.store.DeleteKB(r.Context(), kbID); err != nil {
		slog.Error("delete KB failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete knowledge base")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("deleted KB %s", kbID)})
}

// Episode handlers

type episodeHTTPCreateRequest struct {
	Content  string            `json:"content"`
	Source   string            `json:"source,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (s *HTTPServer) handleEpisodeCreate(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	r.Body = http.MaxBytesReader(w, r.Body, maxContentBytes)
	var req episodeHTTPCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or too-large JSON body")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	source := req.Source
	if source == "" {
		source = "api"
	}

	job, err := s.sched.Submit(r.Context(), kbID, req.Content, ingestion.IngestOptions{
		Source:   source,
		Metadata: req.Metadata,
	})
	if err != nil {
		slog.Error("store episode failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store episode")
		return
	}

	writeJSON(w, http.StatusAccepted, job)
}

func (s *HTTPServer) handleEpisodeList(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	limit := parseIntParam(r, "limit", defaultListLimit, maxListLimit)
	offset := parseIntParam(r, "offset", 0, 0)

	episodes, err := s.store.ListEpisodes(r.Context(), kbID, limit, offset)
	if err != nil {
		slog.Error("list episodes failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list episodes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": episodes})
}

func (s *HTTPServer) handleEpisodeGet(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	id := chi.URLParam(r, "id")

	ep, err := s.store.GetEpisode(r.Context(), kbID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("episode %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

// Entity handlers

func (s *HTTPServer) handleEntityList(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	limit := parseIntParam(r, "limit", defaultListLimit, maxListLimit)
	offset := parseIntParam(r, "offset", 0, 0)
	name := r.URL.Query().Get("name")

	var entities []*domain.Entity
	var err error

	if name != "" {
		entities, err = s.store.FindEntitiesByName(r.Context(), kbID, name)
		if len(entities) > limit {
			entities = entities[:limit]
		}
	} else {
		entities, err = s.store.ListEntities(r.Context(), kbID, limit, offset)
	}
	if err != nil {
		slog.Error("list entities failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list entities")
		return
	}

	for _, e := range entities {
		e.Embedding = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

func (s *HTTPServer) handleEntityGet(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	id := chi.URLParam(r, "id")

	entity, err := s.store.GetEntity(r.Context(), kbID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("entity %q not found", id))
		return
	}
	entity.Embedding = nil
	writeJSON(w, http.StatusOK, entity)
}

func (s *HTTPServer) handleEntityDelete(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	id := chi.URLParam(r, "id")

	if err := s.store.DeleteEntity(r.Context(), kbID, id); err != nil {
		slog.Error("delete entity failed", "kb_id", kbID, "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete entity")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("deleted entity %s", id)})
}

// Relation handlers

func (s *HTTPServer) handleRelationList(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	limit := parseIntParam(r, "limit", defaultListLimit, maxListLimit)
	offset := parseIntParam(r, "offset", 0, 0)
	entityID := r.URL.Query().Get("entity_id")

	var relations []*domain.Relation
	var err error

	if entityID != "" {
		relations, err = s.store.GetRelationsForEntity(r.Context(), kbID, entityID)
		if len(relations) > limit {
			relations = relations[:limit]
		}
	} else {
		relations, err = s.store.ListRelations(r.Context(), kbID, limit, offset)
	}
	if err != nil {
		slog.Error("list relations failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list relations")
		return
	}

	for _, rel := range relations {
		rel.Embedding = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": relations})
}

func (s *HTTPServer) handleRelationGet(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	id := chi.URLParam(r, "id")

	rel, err := s.store.GetRelation(r.Context(), kbID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("relation %q not found", id))
		return
	}
	rel.Embedding = nil
	writeJSON(w, http.StatusOK, rel)
}

// Search handler

func (s *HTTPServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "q query parameter is required")
		return
	}

	opts := search.DefaultOptions()
	opts.TopK = parseIntParam(r, "top_k", 10, 1000)
	opts.MaxHops = parseIntParam(r, "max_hops", 2, 10)

	mode := r.URL.Query().Get("mode")
	channels, err := search.ParseChannels(mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.Channels = channels

	if gs := r.URL.Query().Get("graph_scorer"); gs != "" {
		scorer, err := search.ParseGraphScorer(gs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		opts.GraphScorer = scorer
	}

	if et := r.URL.Query().Get("edge_types"); et != "" {
		opts.EdgeTypes = SplitCSV(et)
	}

	if mw := r.URL.Query().Get("min_weight"); mw != "" {
		v, err := strconv.ParseFloat(mw, 64)
		if err != nil || v < 0 || v > 1 {
			writeError(w, http.StatusBadRequest, "min_weight must be a number between 0 and 1")
			return
		}
		opts.MinWeight = v
	}

	if r.URL.Query().Get("expand_communities") == "true" {
		opts.ExpandCommunities = true
	}

	if at := r.URL.Query().Get("at"); at != "" {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			writeError(w, http.StatusBadRequest, "at must be a valid RFC3339 timestamp")
			return
		}
		opts.TemporalAt = &t
	}

	results, err := s.searcher.Search(r.Context(), kbID, query, opts)
	if err != nil {
		slog.Error("search failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// Graph traverse handler

func (s *HTTPServer) handleGraphTraverse(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	entityID := r.URL.Query().Get("entity_id")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id query parameter is required")
		return
	}

	hops := parseIntParam(r, "hops", 2, 10)

	if err := s.searcher.EnsureLoaded(r.Context(), kbID); err != nil {
		slog.Error("graph load failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load graph")
		return
	}

	kg := s.searcher.GraphStore().Get(kbID)
	if kg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": []any{}, "edges": []any{}})
		return
	}

	var allowTypes []string
	if et := r.URL.Query().Get("edge_types"); et != "" {
		allowTypes = SplitCSV(et)
	}

	sgResult := kg.Subgraph([]string{entityID}, hops, allowTypes)

	sub, err := HydrateSubgraph(r.Context(), s.store, kbID, sgResult)
	if err != nil {
		slog.Error("hydrate subgraph failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to hydrate subgraph")
		return
	}

	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(graph.SummarizeSubgraph(sub.Nodes, sub.Edges)))
		return
	}

	writeJSON(w, http.StatusOK, sub)
}

// Community handler

func (s *HTTPServer) handleCommunityList(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	communities, err := s.store.ListCommunities(r.Context(), kbID)
	if err != nil {
		slog.Error("list communities failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list communities")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"communities": communities})
}

// Stats handlers

func (s *HTTPServer) handleGlobalStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats(r.Context(), "")
	if err != nil {
		slog.Error("get global stats failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *HTTPServer) handleKBStats(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	stats, err := s.store.GetStats(r.Context(), kbID)
	if err != nil {
		slog.Error("get KB stats failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// Lifecycle handlers

func (s *HTTPServer) handleLifecycleDecay(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	updated, err := s.lcManager.DecayKB(r.Context(), kbID)
	if err != nil {
		slog.Error("lifecycle decay failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to run decay")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("decay run complete: %d items updated", updated),
		"updated": updated,
	})
}

func (s *HTTPServer) handleLifecyclePrune(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	threshold := 0.0 // use manager default

	if v := r.URL.Query().Get("threshold"); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil || t <= 0 || t >= 1 {
			writeError(w, http.StatusBadRequest, "threshold must be a number between 0 and 1")
			return
		}
		threshold = t
	}

	pruned, err := s.lcManager.PruneKB(r.Context(), kbID, threshold)
	if err != nil {
		slog.Error("lifecycle prune failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to run prune")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   fmt.Sprintf("prune complete: %d items removed", pruned),
		"pruned":    pruned,
		"threshold": threshold,
	})
}

func (s *HTTPServer) handleLifecycleConsolidate(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	result, err := s.consolidator.RunConsolidation(r.Context(), kbID)
	if err != nil {
		slog.Error("lifecycle consolidate failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to run consolidation")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// Feedback handlers

type feedbackHTTPCreateRequest struct {
	Topic      string `json:"topic"`
	Content    string `json:"content"`
	Correction string `json:"correction,omitempty"`
}

func (s *HTTPServer) handleFeedbackCreate(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	r.Body = http.MaxBytesReader(w, r.Body, maxContentBytes)
	var req feedbackHTTPCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or too-large JSON body")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	fb := domain.NewFeedback(kbID, req.Topic, req.Content, req.Correction, "api")

	if err := s.store.CreateFeedback(r.Context(), fb); err != nil {
		slog.Error("create feedback failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create feedback")
		return
	}

	writeJSON(w, http.StatusCreated, fb)
}

func (s *HTTPServer) handleFeedbackList(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")
	query := r.URL.Query().Get("q")
	topic := r.URL.Query().Get("topic")
	limit := parseIntParam(r, "limit", 50, maxListLimit)

	var feedback []*domain.Feedback
	var err error

	if query != "" {
		feedback, err = s.store.SearchFeedback(r.Context(), kbID, query, limit)
	} else {
		feedback, err = s.store.ListFeedbackByTopic(r.Context(), kbID, topic, limit)
	}
	if err != nil {
		slog.Error("list feedback failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list feedback")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"feedback": feedback})
}

func (s *HTTPServer) handleFeedbackStatsHTTP(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "kb_id")

	stats, err := s.store.GetFeedbackStats(r.Context(), kbID)
	if err != nil {
		slog.Error("feedback stats failed", "kb_id", kbID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get feedback stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// Job handlers

func (s *HTTPServer) handleJobList(w http.ResponseWriter, r *http.Request) {
	kb := r.URL.Query().Get("kb")
	status := r.URL.Query().Get("status")
	limit := parseIntParam(r, "limit", defaultListLimit, maxListLimit)

	jobs, err := s.store.ListJobs(r.Context(), kb, status, limit)
	if err != nil {
		slog.Error("list jobs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *HTTPServer) handleJobGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *HTTPServer) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	job, err := s.sched.RetryJob(r.Context(), id)
	if err != nil {
		slog.Error("retry job failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry job")
		return
	}
	writeJSON(w, http.StatusOK, job)
}
