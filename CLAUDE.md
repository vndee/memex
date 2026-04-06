# Memex — Development Guide

Temporal knowledge graph memory layer for AI agents. Single Go binary + SQLite.

## Build & Test

```bash
go build -o memex ./cmd/memex/    # build
go test ./...                      # run all tests (uses :memory: SQLite)
go vet ./...                       # lint
```

Tests use in-memory SQLite — no external services needed. Real LLM/embedding providers are only needed for manual E2E testing.

## Architecture Overview

```
cmd/memex/main.go          CLI entrypoint + subcommand dispatch
internal/
  config/                  Configuration (env vars, flags, defaults)
  domain/models.go         Core types: KnowledgeBase, Episode, Entity, Relation, Community
  storage/                 SQLite storage layer (CRUD, FTS5, access log, migrations)
  vecstore/                Custom pure-Go vector search engine (brute-force + HNSW + quantization)
  embedding/               Embedding providers: Ollama, OpenAI, Gemini, Vertex, Azure, Groq
  extraction/              LLM extraction + 3-tier entity resolution (exact → fuzzy → LLM)
  ingestion/               Ingestion pipeline + async scheduler with job queue
  search/                  Hybrid retrieval: BM25 + vector + graph, fused via RRF
  graph/                   In-memory adjacency graph + BFS traversal + community detection
  lifecycle/               Memory decay, pruning, entity consolidation
  notify/                  Pluggable notifications (log, webhook, file)
  server/
    mcp.go                 MCP server (stdio, 11 tools)
    http.go                Chi HTTP server (20+ REST endpoints)
    helpers.go             Shared KB creation helper
  tui/                     Bubble Tea terminal UI (3-pane layout + graph explorer)
  cloudauth/               Google API key resolution (explicit → GOOGLE_API_KEY → GEMINI_API_KEY)
  httpclient/              Shared HTTP client for provider API calls
migrations/                Embedded SQL migrations (applied at startup)
pkg/memex/                 Public Go library API (embeddable)
```

## Key Design Decisions

### Storage
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO). WAL mode for concurrent reads.
- All data tables scoped by `kb_id`. Each KB is an isolated memory space.
- ULIDs for all IDs (lexicographically sortable by time).
- Migrations embedded via `go:embed` in root `migrations.go`, applied at startup.

### Custom Vector Engine (`internal/vecstore/`)
Pure Go vector search — no chromem-go, no sqlite-vec. Implements:
- **Brute-force** for <10K vectors with loop-unrolled cosine similarity
- **HNSW** index (M=16, efConstruction=200) for larger collections
- **Int8 scalar quantization** for 4x memory reduction
- Persistence to SQLite BLOB
- SQL interface: `CREATE VIRTUAL TABLE ... USING memex_vec(dim=768)`

### Hybrid Search (No LLM at Query Time)
```
Query → parallel:
  [1] BM25 via FTS5 (full-text keyword search)
  [2] Vector via vecstore (semantic similarity)
  [3] Graph BFS (entity name seed → 2-hop expansion)
→ Reciprocal Rank Fusion (k=60)
→ Temporal decay multiplier
→ Top-K results
```

### Per-KB Provider Isolation
Each KB stores its own embed config and LLM config. Different KBs can use different providers (e.g., Ollama for one, Gemini for another). API keys stored in SQLite but excluded from JSON serialization (`json:"-"` tags). Provider instances cached per KB with cache keys that include `apiKey` to prevent cross-KB auth contamination.

### Bitemporal Relations
Relations track two time dimensions:
- `valid_at`: when the fact became true in the real world
- `invalid_at`: when it stopped being true (nil = still valid)
- `created_at`: transaction time (when memex learned about it)

### Ingestion Pipeline
```
Text → Episode → LLM Extract (entities + relations) → Entity Resolution → Embed → Store
```
- Async scheduler with goroutine pool (default 4 workers)
- Per-KB mutex for entity resolution correctness
- Jobs persisted in `ingestion_jobs` table with retry (max 3 attempts)
- Crash recovery: "running" jobs reset to "queued" on startup

### Memory Lifecycle
- **Decay**: Ebbinghaus-inspired: `strength = e^(-t / (stability * access_count^1.5))`, default half-life 168h
- **Pruning**: Remove items with strength < threshold. Protects entities with relations.
- **Consolidation**: Cosine similarity > 0.92 → merge entities, redirect relations (in transaction)
- Background manager runs decay every 1h, prune every 24h

### FTS5 Query Safety
User queries are sanitized before FTS5 MATCH — each word is double-quoted to prevent special characters (`?`, `.`, `*`) from being interpreted as FTS5 operators. See `sanitizeFTS5()` in `storage/search.go`.

## Conventions

- **Error wrapping**: Always `fmt.Errorf("context: %w", err)`. Never return naked errors.
- **Logging**: `log/slog` only. Generic messages to clients, details to logs. Never log entity names or content (PII).
- **HTTP security**: `http.MaxBytesReader` on POST bodies, generic error messages to clients, `ReadTimeout: 30s`, default bind `127.0.0.1`.
- **No ORM, no DI framework**. Direct SQL with parameterized queries.
- **Test with `:memory:` SQLite**. No mocks for storage — use the real store.
- **Struct tags**: `json:"-"` on sensitive fields (API keys). `json:"omitempty"` for optional fields.

## Provider Support

| Provider | Prefix | Embeddings | LLM | API Key Env |
|----------|--------|-----------|-----|-------------|
| Ollama | `ollama/` | nomic-embed-text | llama3.2 | (none, local) |
| OpenAI | `openai/` | text-embedding-3-small | gpt-4o-mini | `OPENAI_API_KEY` |
| Gemini | `gemini/` | gemini-embedding-001 | gemini-2.5-flash | `GEMINI_API_KEY` |
| Vertex | `vertex/` | textembedding-gecko | gemini-2.5-flash | `GOOGLE_API_KEY` |
| Azure | `azure/` | text-embedding-3-small | gpt-4o-mini | `AZURE_OPENAI_API_KEY` |
| Groq | `groq/` | -- | llama-3.3-70b | `GROQ_API_KEY` |

`gemini/` is an alias for `genai/` internally. Model names are parsed as `provider/model` (e.g., `gemini/gemini-2.5-flash`).

## MCP Tools (11 total)

`memex_store`, `memex_search`, `memex_list_kbs`, `memex_create_kb`, `memex_delete_kb`, `memex_get_entity`, `memex_get_relations`, `memex_get_stats`, `memex_lifecycle_decay`, `memex_lifecycle_prune`, `memex_lifecycle_consolidate`

All data tools require a `kb` parameter. Transport: stdio.

## TUI Architecture

Built with `bubbletea` v1 (Model/Init/Update/View pattern), `bubbles` (table, textinput, viewport), `lipgloss` (styling).

- 3-pane layout: KB browser (left) + content table (center) + inspector (right)
- Graph explorer: focused-node navigator with breadcrumb trail, connection expansion
- Vim-style keys: `h/l` panes, `j/k` navigate, `g/G` top/bottom, `enter` expand, `esc` back
- Entity name cache for resolving ULIDs in relation display
- `SetRows(nil)` before `SetColumns()` to prevent bubbles table column/row count mismatch panic

## Database Schema

Two migrations in `migrations/`:
- `001_initial.sql`: knowledge_bases, episodes, entities, relations, communities, decay_state, memory_access_log, FTS5 virtual tables, vector virtual tables
- `002_ingestion_jobs.sql`: ingestion_jobs table (replaces old ingestion_tasks)

## Research Background

Based on Graphiti-lite architecture. Key influences:
- **Zep/Graphiti** (arXiv:2501.13956): Temporal KG with bitemporal edges
- **Mem0** (arXiv:2504.19413): Production memory patterns
- **LightRAG**: Hybrid graph-vector retrieval
- **HippoRAG** (NeurIPS 2024): PageRank-based importance
- Reciprocal Rank Fusion for multi-signal search merging
- Ebbinghaus forgetting curve for memory decay
- 3-tier entity resolution: exact match → fuzzy → LLM dedup
