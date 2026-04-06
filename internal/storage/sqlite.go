package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/vndee/memex/internal/domain"
	_ "modernc.org/sqlite"
)

// MigrationSQL holds the initial schema. Set by the main package at startup
// using the embedded migrations file, or hardcoded for tests.
var MigrationSQL string

// Store provides all database operations scoped by knowledge base.
type Store interface {
	// Knowledge Bases
	CreateKB(ctx context.Context, kb *domain.KnowledgeBase) error
	GetKB(ctx context.Context, id string) (*domain.KnowledgeBase, error)
	ListKBs(ctx context.Context) ([]*domain.KnowledgeBase, error)
	DeleteKB(ctx context.Context, id string) error

	// Episodes
	CreateEpisode(ctx context.Context, ep *domain.Episode) error
	GetEpisode(ctx context.Context, kbID, id string) (*domain.Episode, error)
	ListEpisodes(ctx context.Context, kbID string, limit, offset int) ([]*domain.Episode, error)
	DeleteEpisode(ctx context.Context, kbID, id string) error

	// Entities
	CreateEntity(ctx context.Context, e *domain.Entity) error
	GetEntity(ctx context.Context, kbID, id string) (*domain.Entity, error)
	UpdateEntity(ctx context.Context, e *domain.Entity) error
	DeleteEntity(ctx context.Context, kbID, id string) error
	FindEntitiesByName(ctx context.Context, kbID, name string) ([]*domain.Entity, error)
	ListEntities(ctx context.Context, kbID string, limit, offset int) ([]*domain.Entity, error)
	ListEntityNames(ctx context.Context, kbID string) ([]*domain.Entity, error) // lightweight: id, name, type, summary only

	// Relations
	CreateRelation(ctx context.Context, r *domain.Relation) error
	GetRelation(ctx context.Context, kbID, id string) (*domain.Relation, error)
	InvalidateRelation(ctx context.Context, kbID, id string, invalidAt time.Time) error
	GetRelationsForEntity(ctx context.Context, kbID, entityID string) ([]*domain.Relation, error)
	GetValidRelations(ctx context.Context, kbID string, at time.Time) ([]*domain.Relation, error)
	ListRelations(ctx context.Context, kbID string, limit, offset int) ([]*domain.Relation, error)
	UpsertRelation(ctx context.Context, r *domain.Relation) (bool, error)
	DeduplicateRelationsForKB(ctx context.Context, kbID string) (int64, error)
	DeduplicateRelationsForEntity(ctx context.Context, kbID, entityID string) (int64, error)

	// Communities
	CreateCommunity(ctx context.Context, c *domain.Community) error
	UpdateCommunity(ctx context.Context, c *domain.Community) error
	ListCommunities(ctx context.Context, kbID string) ([]*domain.Community, error)

	// FTS5 search
	SearchFTS(ctx context.Context, kbID, query string, limit int) ([]*domain.SearchResult, error)

	// Vector search — loads embeddings from DB for brute-force similarity search.
	// For high-performance indexed search, use the vecstore.Engine directly.
	SearchVectorEntities(ctx context.Context, kbID string, query []float32, limit int) ([]*domain.SearchResult, error)
	SearchVectorRelations(ctx context.Context, kbID string, query []float32, limit int) ([]*domain.SearchResult, error)

	// Access tracking
	LogAccess(ctx context.Context, kbID, entityType, entityID string) error
	GetDecayState(ctx context.Context, kbID, entityType, entityID string) (*domain.DecayState, error)
	UpdateDecayState(ctx context.Context, ds *domain.DecayState) error
	ListDecayStates(ctx context.Context, kbID string, maxStrength float64) ([]*domain.DecayState, error)

	// Stats
	GetStats(ctx context.Context, kbID string) (*domain.MemoryStats, error)

	// Ingestion jobs
	CreateJob(ctx context.Context, job *domain.IngestionJob) error
	GetJob(ctx context.Context, id string) (*domain.IngestionJob, error)
	ListJobs(ctx context.Context, kbID, status string, limit int) ([]*domain.IngestionJob, error)
	UpdateJobStatus(ctx context.Context, id, status string, updates JobUpdate) error
	RecoverStaleJobs(ctx context.Context) (int64, error)
	DequeueJobs(ctx context.Context, limit int) ([]*domain.IngestionJob, error)

	// Vector loading — used for vecstore hydration at startup or on first search.
	LoadEntityEmbeddings(ctx context.Context, kbID string) (map[string][]float32, error)
	LoadRelationEmbeddings(ctx context.Context, kbID string) (map[string][]float32, error)

	// Episode counts
	CountEpisodesBySourcePrefix(ctx context.Context, kbID, prefix string) (int, error)

	// Feedback
	CreateFeedback(ctx context.Context, fb *domain.Feedback) error
	SearchFeedback(ctx context.Context, kbID, query string, limit int) ([]*domain.Feedback, error)
	ListFeedbackByTopic(ctx context.Context, kbID, topic string, limit int) ([]*domain.Feedback, error)
	GetFeedbackStats(ctx context.Context, kbID string) (*domain.FeedbackStats, error)

	// Consolidation
	RedirectRelations(ctx context.Context, kbID, fromEntityID, toEntityID string) (int64, error)
	DeleteDecayState(ctx context.Context, kbID, entityType, entityID string) error
	BatchUpdateDecayStrength(ctx context.Context, kbID string, halfLifeHours float64) (int64, error)

	// Lifecycle
	Close() error
	DB() *sql.DB
}

// SQLiteStore implements Store backed by SQLite.
type SQLiteStore struct {
	db     *sql.DB
	dbPath string
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
// Pass ":memory:" for an in-memory database (useful for testing).
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath != ":memory:" {
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			return nil, fmt.Errorf("resolve db path: %w", err)
		}
		dbPath = absPath

		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if dbPath == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
	}

	s := &SQLiteStore{db: db, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) migrate() error {
	if MigrationSQL == "" {
		return fmt.Errorf("MigrationSQL not set; call storage.SetMigrationSQL() before creating store")
	}

	_, err := s.db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}
	_, err = s.db.Exec("PRAGMA foreign_keys=ON")
	if err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	_, err = s.db.Exec(MigrationSQL)
	if err != nil {
		return fmt.Errorf("execute migration: %w", err)
	}

	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// DBSize returns the database file size in bytes.
func (s *SQLiteStore) DBSize() (int64, error) {
	if s.dbPath == ":memory:" {
		return 0, nil
	}
	info, err := os.Stat(s.dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func sqliteDSN(dbPath string) string {
	if dbPath == ":memory:" {
		return dbPath
	}

	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")

	return (&url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: q.Encode(),
	}).String()
}
