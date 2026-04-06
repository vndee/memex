package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	memex "github.com/vndee/memex"
	"github.com/vndee/memex/internal/config"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/embedding"
	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/graph"
	"github.com/vndee/memex/internal/hooks"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/lifecycle"
	"github.com/vndee/memex/internal/notify"
	"github.com/vndee/memex/internal/tui"
	"github.com/vndee/memex/internal/search"
	"github.com/vndee/memex/internal/server"
	"github.com/vndee/memex/internal/setup"
	"github.com/vndee/memex/internal/storage"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	storage.MigrationSQL = memex.MigrationSQL()

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version":
		fmt.Printf("memex %s\n", version)
	case "stats":
		cmdStats(args)
	case "kb":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: memex kb <create|list|delete>")
			os.Exit(1)
		}
		switch args[0] {
		case "create":
			cmdKBCreate(args[1:])
		case "list":
			cmdKBList(args[1:])
		case "delete":
			cmdKBDelete(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "unknown kb subcommand: %s\n", args[0])
			os.Exit(1)
		}
	case "init":
		cmdInit(args)
	case "hook":
		cmdHook(args)
	case "store":
		cmdStore(args)
	case "search":
		cmdSearch(args)
	case "graph":
		cmdGraph(args)
	case "jobs":
		cmdJobs(args)
	case "serve":
		cmdServe(args)
	case "mcp":
		cmdMCP(args)
	case "tui":
		cmdTUI(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `memex — temporal knowledge graph memory for AI agents

Usage:
  memex version                              Print version
  memex init [--editor <name>] [--dry-run]   Auto-configure AI editors (Claude Code, Cursor, etc.)
  memex hook <post|compact|prompt> [flags]   Process hook events from AI editors
  memex kb create <id> [flags]               Create a knowledge base
  memex kb list [flags]                      List knowledge bases
  memex kb delete <id> [flags]               Delete a knowledge base
  memex store <text> --kb <id> [flags]       Store a memory
  memex search <query> --kb <id> [flags]     Hybrid search (BM25 + vector + graph)
  memex graph <entity_id> --kb <id> [flags] Traverse graph from an entity
  memex jobs [--kb <id>] [--status <s>]      List ingestion jobs
  memex jobs <id>                            Show job details
  memex jobs retry <id>                      Retry a failed job
  memex jobs export [--kb <id>] [flags]      Export job history
  memex stats [--kb <id>] [flags]            Show statistics
  memex serve [flags]                        Start HTTP API server
  memex mcp [flags]                          Start MCP server (stdio)
  memex tui [flags]                          Launch interactive terminal UI`)
}

// --- Shared helpers ---

func loadConfig(dbFlag string) (*config.Config, *storage.SQLiteStore) {
	cfg := config.DefaultConfig()
	cfg.LoadFromEnv()
	if dbFlag != "" {
		cfg.DBPath = dbFlag
	}
	store, err := storage.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	return cfg, store
}

func buildNotifiers(cfg *config.Config) []notify.Notifier {
	notifiers := []notify.Notifier{notify.LogNotifier{}}
	if cfg.NotifyWebhookURL != "" {
		wh, err := notify.NewWebhookNotifier(cfg.NotifyWebhookURL, 0)
		if err != nil {
			slog.Error("invalid webhook URL, skipping", "url", cfg.NotifyWebhookURL, "error", err)
		} else {
			notifiers = append(notifiers, wh)
		}
	}
	if cfg.NotifyFilePath != "" {
		fn, err := notify.NewFileNotifier(cfg.NotifyFilePath)
		if err != nil {
			slog.Error("invalid notify file path, skipping", "path", cfg.NotifyFilePath, "error", err)
		} else {
			notifiers = append(notifiers, fn)
		}
	}
	return notifiers
}

func buildScheduler(cfg *config.Config, store storage.Store, async bool) *ingestion.Scheduler {
	pipe := ingestion.NewPipeline(store, embedding.NewRegistry(), extraction.NewRegistry())
	return ingestion.NewScheduler(pipe, store, ingestion.SchedulerConfig{
		PoolSize:    cfg.PoolSize,
		MaxAttempts: cfg.MaxAttempts,
		Async:       async,
	}, buildNotifiers(cfg)...)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// --- Commands ---

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	editor := fs.String("editor", "", "configure only this editor (e.g. 'Claude Code', 'Cursor')")
	db := fs.String("db", "", "database path to use in MCP config")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "overwrite existing memex entries")
	fs.Parse(args)

	// Find memex binary path
	memexBin, _ := os.Executable()
	if memexBin == "" {
		memexBin = "memex"
	}

	results := setup.RunInit(memexBin, *db, *editor, *dryRun, *force)

	configured := 0
	for _, r := range results {
		switch r.Status {
		case "configured":
			fmt.Printf("  ✓ %s → %s\n", r.Editor, r.ConfigPath)
			configured++
		case "skipped":
			fmt.Printf("  - %s (already configured, use --force to overwrite)\n", r.Editor)
		case "not_installed":
			fmt.Printf("  · %s (not installed)\n", r.Editor)
		case "error":
			fmt.Printf("  ✗ %s: %s\n", r.Editor, r.Error)
		}
	}

	if configured > 0 {
		fmt.Printf("\nConfigured %d editor(s). Restart them to activate memex MCP.\n", configured)
	} else if !*dryRun {
		fmt.Println("\nNo editors were configured. Install a supported editor or use --editor to specify one.")
	}
}

func cmdHook(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: memex hook <post|compact|prompt> --kb <id>")
		os.Exit(1)
	}

	event := args[0]
	fs := flag.NewFlagSet("hook", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "default", "knowledge base ID")
	throttle := fs.Int64("throttle", 1, "process every Nth call (for post hook)")
	fs.Parse(args[1:])

	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	sched := buildScheduler(cfg, store, false)

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	handler := hooks.NewHandler(sched, searcher)

	var err error
	switch event {
	case "post":
		err = handler.PostToolUse(ctx, *kb, *throttle)
	case "compact":
		err = handler.PreCompact(ctx, *kb)
	case "prompt":
		err = handler.UserPromptSubmit(ctx, *kb)
	default:
		fmt.Fprintf(os.Stderr, "unknown hook event: %s (use post, compact, or prompt)\n", event)
		os.Exit(1)
	}

	if err != nil {
		slog.Error("hook failed", "event", event, "error", err)
		os.Exit(1)
	}
}

func cmdStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID (empty = global)")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	_, store := loadConfig(*db)
	defer store.Close()

	stats, err := store.GetStats(context.Background(), *kb)
	if err != nil {
		slog.Error("failed to get stats", "error", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(stats)
	} else {
		if stats.KBID != "" {
			fmt.Printf("Knowledge Base: %s\n", stats.KBID)
		} else {
			fmt.Println("Global Statistics")
		}
		fmt.Printf("Episodes:    %d\n", stats.TotalEpisodes)
		fmt.Printf("Entities:    %d\n", stats.TotalEntities)
		fmt.Printf("Relations:   %d\n", stats.TotalRelations)
		fmt.Printf("Communities: %d\n", stats.TotalCommunities)
		fmt.Printf("DB Size:     %d bytes\n", stats.DBSizeBytes)
	}
}

func cmdKBCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex kb create <id> [flags]")
		os.Exit(1)
	}

	id := args[0]
	fs := flag.NewFlagSet("kb create", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	embed := fs.String("embed", "ollama/nomic-embed-text", "embedding provider/model")
	llm := fs.String("llm", "ollama/llama3.2", "LLM provider/model")
	name := fs.String("name", "", "display name (defaults to ID)")
	desc := fs.String("desc", "", "description")
	fs.Parse(args[1:])
	embedParts := strings.SplitN(*embed, "/", 2)
	llmParts := strings.SplitN(*llm, "/", 2)

	if len(embedParts) != 2 || len(llmParts) != 2 {
		fmt.Fprintln(os.Stderr, "embed and llm flags must be in provider/model format")
		os.Exit(1)
	}

	displayName := *name
	if displayName == "" {
		displayName = id
	}

	_, store := loadConfig(*db)
	defer store.Close()

	kb := &domain.KnowledgeBase{
		ID:          id,
		Name:        displayName,
		Description: *desc,
		EmbedConfig: domain.EmbedConfig{
			Provider: embedParts[0],
			Model:    embedParts[1],
		},
		LLMConfig: domain.LLMConfig{
			Provider: llmParts[0],
			Model:    llmParts[1],
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := store.CreateKB(context.Background(), kb); err != nil {
		slog.Error("failed to create kb", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Created knowledge base: %s (embed: %s, llm: %s)\n", id, *embed, *llm)
}

func cmdKBList(args []string) {
	fs := flag.NewFlagSet("kb list", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	_, store := loadConfig(*db)
	defer store.Close()

	kbs, err := store.ListKBs(context.Background())
	if err != nil {
		slog.Error("failed to list kbs", "error", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(kbs)
	} else {
		if len(kbs) == 0 {
			fmt.Println("No knowledge bases found. Create one with: memex kb create <id>")
			return
		}
		for _, kb := range kbs {
			fmt.Printf("%-20s embed=%s/%s  llm=%s/%s\n",
				kb.ID, kb.EmbedConfig.Provider, kb.EmbedConfig.Model,
				kb.LLMConfig.Provider, kb.LLMConfig.Model)
		}
	}
}

func cmdKBDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex kb delete <id>")
		os.Exit(1)
	}

	id := args[0]
	fs := flag.NewFlagSet("kb delete", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	fs.Parse(args[1:])

	_, store := loadConfig(*db)
	defer store.Close()

	if err := store.DeleteKB(context.Background(), id); err != nil {
		slog.Error("failed to delete kb", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Deleted knowledge base: %s\n", id)
}

func cmdStore(args []string) {
	fs := flag.NewFlagSet("store", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID (required)")
	source := fs.String("source", "cli", "source identifier")
	async := fs.Bool("async", false, "submit for async processing (returns immediately)")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	if *kb == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex store <text> --kb <id> [--async]")
		os.Exit(1)
	}

	text := strings.Join(fs.Args(), " ")

	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	useAsync := *async || cfg.AsyncIngest
	sched := buildScheduler(cfg, store, useAsync)

	if useAsync {
		if err := sched.Start(ctx); err != nil {
			slog.Error("failed to start scheduler", "error", err)
			os.Exit(1)
		}
	}

	job, err := sched.Submit(ctx, *kb, text, ingestion.IngestOptions{
		Source: *source,
	})

	if useAsync {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		sched.Shutdown(shutCtx)
	}

	if err != nil {
		slog.Error("ingestion failed", "error", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(job)
	} else if useAsync {
		fmt.Printf("Job submitted: %s (status: %s)\n", job.ID, job.Status)
	} else {
		fmt.Printf("Stored: job=%s status=%s episode=%s\n", job.ID, job.Status, job.EpisodeID)
	}
}

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID (required)")
	topK := fs.Int("top-k", 10, "number of results")
	mode := fs.String("mode", "hybrid", "search mode: hybrid|bm25|vector")
	hops := fs.Int("hops", 2, "graph BFS hops for hybrid mode")
	graphScorer := fs.String("graph-scorer", "bfs", "graph scoring: bfs|pagerank|weighted")
	edgeTypes := fs.String("edge-types", "", "comma-separated relation types to traverse")
	minWeight := fs.Float64("min-weight", 0, "minimum edge weight for weighted scorer")
	expandComm := fs.Bool("expand-communities", false, "expand seeds with community members")
	at := fs.String("at", "", "ISO 8601 timestamp for temporal traversal")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	if *kb == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex search <query> --kb <id> [--mode hybrid|bm25|vector]")
		os.Exit(1)
	}

	query := strings.Join(fs.Args(), " ")

	cfg, store := loadConfig(*db)
	defer store.Close()

	opts := search.DefaultOptions()
	opts.TopK = *topK
	opts.MaxHops = *hops
	opts.MinWeight = *minWeight
	opts.ExpandCommunities = *expandComm

	channels, err := search.ParseChannels(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts.Channels = channels

	scorer, err := search.ParseGraphScorer(*graphScorer)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts.GraphScorer = scorer

	if *edgeTypes != "" {
		opts.EdgeTypes = server.SplitCSV(*edgeTypes)
	}

	if *at != "" {
		parsed, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --at timestamp: %v\n", err)
			os.Exit(1)
		}
		opts.TemporalAt = &parsed
	}

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	results, err := searcher.Search(context.Background(), *kb, query, opts)
	if err != nil {
		slog.Error("search failed", "error", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return
	}

	if *jsonOut {
		printJSON(results)
		return
	}

	for i, r := range results {
		fmt.Printf("%d. [%s] %s (score: %.4f)\n", i+1, r.Type, r.Content, r.Score)
	}
}

func cmdGraph(args []string) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID (required)")
	hops := fs.Int("hops", 2, "traversal depth (1-10)")
	edgeTypes := fs.String("edge-types", "", "comma-separated relation types to traverse")
	format := fs.String("format", "json", "output format: json|text")
	fs.Parse(args)

	if *kb == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex graph <entity_id> --kb <id> [--hops 2] [--format json|text]")
		os.Exit(1)
	}

	entityID := fs.Arg(0)
	if *hops < 1 {
		*hops = 1
	}
	if *hops > 10 {
		*hops = 10
	}
	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx := context.Background()

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	if err := searcher.EnsureLoaded(ctx, *kb); err != nil {
		slog.Error("failed to load graph", "error", err)
		os.Exit(1)
	}

	kg := searcher.GraphStore().Get(*kb)
	if kg == nil {
		fmt.Println("No graph data available.")
		return
	}

	var allowTypes []string
	if *edgeTypes != "" {
		allowTypes = server.SplitCSV(*edgeTypes)
	}

	sgResult := kg.Subgraph([]string{entityID}, *hops, allowTypes)

	sub, err := server.HydrateSubgraph(ctx, store, *kb, sgResult)
	if err != nil {
		slog.Error("failed to hydrate subgraph", "error", err)
		os.Exit(1)
	}

	if *format == "text" {
		fmt.Print(graph.SummarizeSubgraph(sub.Nodes, sub.Edges))
		return
	}

	printJSON(sub)
}

func cmdMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	fs.Parse(args)

	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	sched := buildScheduler(cfg, store, false)
	if err := sched.Start(ctx); err != nil {
		slog.Error("failed to start scheduler", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		sched.Shutdown(shutCtx)
	}()

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	lcManager := lifecycle.NewManager(store, lifecycle.ManagerConfig{
		DecayHalfLife:  cfg.DecayHalfLife,
		PruneThreshold: cfg.PruneThreshold,
	})
	consolidator := lifecycle.NewConsolidator(store, 0)

	mcpServer := server.NewMCPServer(store, sched, searcher, lcManager, consolidator, version)
	if err := mcpServer.Run(ctx); err != nil {
		slog.Error("MCP server error", "error", err)
		os.Exit(1)
	}
}

func cmdTUI(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	fs.Parse(args)

	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	sched := buildScheduler(cfg, store, false)
	if err := sched.Start(ctx); err != nil {
		slog.Error("failed to start scheduler", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		sched.Shutdown(shutCtx)
	}()

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	app := tui.New(store, searcher, sched)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		slog.Error("TUI error", "error", err)
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	host := fs.String("host", "127.0.0.1", "listen address (use 0.0.0.0 for all interfaces)")
	port := fs.Int("port", 0, "HTTP port (default from config or 8080)")
	fs.Parse(args)

	cfg, store := loadConfig(*db)
	defer store.Close()

	if *port > 0 {
		cfg.HTTPPort = *port
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	sched := buildScheduler(cfg, store, true)
	if err := sched.Start(ctx); err != nil {
		slog.Error("failed to start scheduler", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		sched.Shutdown(shutCtx)
	}()

	embedReg := embedding.NewRegistry()
	searcher := server.NewSearcher(store, cfg.DecayHalfLife, embedReg.NewProvider)

	lcManager := lifecycle.NewManager(store, lifecycle.ManagerConfig{
		DecayHalfLife:  cfg.DecayHalfLife,
		PruneThreshold: cfg.PruneThreshold,
	})
	consolidator := lifecycle.NewConsolidator(store, 0) // default threshold

	// Start background lifecycle loop.
	go lcManager.Run(ctx)

	httpSrv := server.NewHTTPServer(store, sched, searcher, lcManager, consolidator)
	addr := fmt.Sprintf("%s:%d", *host, cfg.HTTPPort)
	if err := httpSrv.Serve(ctx, addr); err != nil {
		slog.Error("HTTP server error", "error", err)
		os.Exit(1)
	}
}

func cmdJobs(args []string) {
	if len(args) == 0 {
		cmdJobsList(args)
		return
	}

	switch args[0] {
	case "retry":
		cmdJobsRetry(args[1:])
	case "export":
		cmdJobsExport(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			cmdJobsList(args)
		} else {
			cmdJobDetail(args)
		}
	}
}

func cmdJobsList(args []string) {
	fs := flag.NewFlagSet("jobs", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID")
	status := fs.String("status", "", "filter by status (queued|running|completed|failed)")
	limit := fs.Int("limit", 20, "max results")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args)

	_, store := loadConfig(*db)
	defer store.Close()

	jobs, err := store.ListJobs(context.Background(), *kb, *status, *limit)
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(jobs)
		return
	}

	if len(jobs) == 0 {
		fmt.Println("No ingestion jobs found.")
		return
	}

	fmt.Printf("%-28s %-12s %-10s %-8s %-20s %s\n", "ID", "KB", "STATUS", "ATTEMPT", "CREATED", "EPISODE")
	for _, j := range jobs {
		ep := j.EpisodeID
		if ep == "" {
			ep = "-"
		}
		fmt.Printf("%-28s %-12s %-10s %d/%-5d %-20s %s\n",
			j.ID, truncate(j.KBID, 12), j.Status, j.Attempts, j.MaxAttempts,
			j.CreatedAt.Format("2006-01-02 15:04:05"), ep)
	}
}

func cmdJobDetail(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex jobs <id>")
		os.Exit(1)
	}

	jobID := args[0]
	fs := flag.NewFlagSet("jobs detail", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	jsonOut := fs.Bool("json", false, "output as JSON")
	fs.Parse(args[1:])

	_, store := loadConfig(*db)
	defer store.Close()

	job, err := store.GetJob(context.Background(), jobID)
	if err != nil {
		slog.Error("failed to get job", "error", err)
		os.Exit(1)
	}

	if *jsonOut {
		printJSON(job)
		return
	}

	fmt.Printf("Job:       %s\n", job.ID)
	fmt.Printf("KB:        %s\n", job.KBID)
	fmt.Printf("Status:    %s\n", job.Status)
	fmt.Printf("Source:    %s\n", job.Source)
	fmt.Printf("Episode:   %s\n", valueOr(job.EpisodeID, "-"))
	fmt.Printf("Attempts:  %d/%d\n", job.Attempts, job.MaxAttempts)
	fmt.Printf("Created:   %s\n", job.CreatedAt.Format(time.RFC3339))
	if job.StartedAt != nil {
		fmt.Printf("Started:   %s\n", job.StartedAt.Format(time.RFC3339))
	}
	if job.CompletedAt != nil {
		fmt.Printf("Completed: %s\n", job.CompletedAt.Format(time.RFC3339))
		if job.StartedAt != nil {
			fmt.Printf("Duration:  %s\n", job.CompletedAt.Sub(*job.StartedAt).Round(time.Millisecond))
		}
	}
	if job.Error != "" {
		fmt.Printf("Error:     %s\n", job.Error)
	}
	if len(job.Result) > 0 {
		fmt.Printf("Result:    %s\n", string(job.Result))
	}
}

func cmdJobsRetry(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: memex jobs retry <id>")
		os.Exit(1)
	}

	jobID := args[0]
	fs := flag.NewFlagSet("jobs retry", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	fs.Parse(args[1:])

	cfg, store := loadConfig(*db)
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	sched := buildScheduler(cfg, store, false)

	job, err := sched.RetryJob(ctx, jobID)
	if err != nil {
		slog.Error("retry failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Job %s: status=%s\n", job.ID, job.Status)
}

func cmdJobsExport(args []string) {
	fs := flag.NewFlagSet("jobs export", flag.ExitOnError)
	db := fs.String("db", "", "database path")
	kb := fs.String("kb", "", "knowledge base ID")
	format := fs.String("format", "json", "output format (json|csv)")
	output := fs.String("output", "", "output file (default: stdout)")
	limit := fs.Int("limit", 1000, "max jobs to export")
	fs.Parse(args)

	_, store := loadConfig(*db)
	defer store.Close()

	jobs, err := store.ListJobs(context.Background(), *kb, "", *limit)
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		os.Exit(1)
	}

	var w *os.File
	if *output != "" {
		w, err = os.Create(*output)
		if err != nil {
			slog.Error("failed to create output file", "error", err)
			os.Exit(1)
		}
		defer w.Close()
	} else {
		w = os.Stdout
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(jobs)

	case "csv":
		cw := csv.NewWriter(w)
		cw.Write([]string{"id", "kb_id", "status", "source", "episode_id", "error", "attempts", "max_attempts", "created_at", "started_at", "completed_at"})
		for _, j := range jobs {
			cw.Write([]string{
				j.ID, j.KBID, j.Status, j.Source, j.EpisodeID, j.Error,
				strconv.Itoa(j.Attempts), strconv.Itoa(j.MaxAttempts),
				j.CreatedAt.Format(time.RFC3339),
				timeStr(j.StartedAt), timeStr(j.CompletedAt),
			})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			slog.Error("csv write error", "error", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown format: %s (use json or csv)\n", *format)
		os.Exit(1)
	}

	if *output != "" {
		fmt.Fprintf(os.Stderr, "Exported %d jobs to %s\n", len(jobs), *output)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func timeStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
