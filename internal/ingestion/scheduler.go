package ingestion

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/notify"
	"github.com/vndee/memex/internal/storage"
)

const (
	defaultPoolSize      = 4
	defaultMaxAttempts   = 3
	defaultFeedInterval  = 2 * time.Second
	maxErrorLength       = 2048
)

// SchedulerConfig configures async ingestion behavior.
type SchedulerConfig struct {
	PoolSize     int           // worker goroutines (default 4)
	MaxAttempts  int           // per-job retry limit (default 3)
	FeedInterval time.Duration // how often the feeder polls for queued jobs (default 2s)
	Async        bool          // when false, Submit blocks until completion
}

// Scheduler manages async ingestion jobs with a bounded goroutine pool.
// It enforces per-KB serialization to avoid entity resolution conflicts.
type Scheduler struct {
	pipeline  *Pipeline
	store     storage.Store
	config    SchedulerConfig
	notifiers notify.Multi

	kbLocks map[string]*sync.Mutex
	mu      sync.Mutex // protects kbLocks map
	wg      sync.WaitGroup
	jobs    chan *domain.IngestionJob
	done    chan struct{}
}

// NewScheduler creates a scheduler wrapping the given pipeline.
func NewScheduler(pipeline *Pipeline, store storage.Store, cfg SchedulerConfig, notifiers ...notify.Notifier) *Scheduler {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = defaultPoolSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.FeedInterval <= 0 {
		cfg.FeedInterval = defaultFeedInterval
	}

	return &Scheduler{
		pipeline:  pipeline,
		store:     store,
		config:    cfg,
		notifiers: notify.Multi(notifiers),
		kbLocks:   make(map[string]*sync.Mutex),
		jobs:      make(chan *domain.IngestionJob, cfg.PoolSize*4),
		done:      make(chan struct{}),
	}
}

// Start launches worker goroutines and recovers stale jobs from a previous crash.
func (s *Scheduler) Start(ctx context.Context) error {
	recovered, err := s.store.RecoverStaleJobs(ctx)
	if err != nil {
		slog.Warn("failed to recover stale jobs", "error", err)
	} else if recovered > 0 {
		slog.Info("recovered stale jobs", "count", recovered)
	}

	for i := 0; i < s.config.PoolSize; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	// Feed queued jobs from DB into the channel
	s.wg.Add(1)
	go s.feeder(ctx)

	return nil
}

// Shutdown stops accepting new jobs and waits for in-flight work to complete.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	close(s.done)

	finished := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Submit creates a new ingestion job. In async mode it returns immediately
// after persisting the job. In sync mode it blocks until completion.
func (s *Scheduler) Submit(ctx context.Context, kbID, text string, opts IngestOptions) (*domain.IngestionJob, error) {
	job := &domain.IngestionJob{
		ID:          ulid.Make().String(),
		KBID:        kbID,
		Status:      domain.JobStatusQueued,
		Content:     text,
		Source:      opts.Source,
		Metadata:    opts.Metadata,
		MaxAttempts: s.config.MaxAttempts,
		CreatedAt:   time.Now().UTC(),
	}

	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, err
	}

	if !s.config.Async {
		s.executeJob(ctx, job)
		updated, err := s.store.GetJob(ctx, job.ID)
		if err != nil {
			return job, nil
		}
		return updated, nil
	}

	// Async: job stays in DB with status=queued; feeder will pick it up.
	// No direct channel push to avoid duplicate dispatch with the feeder.

	return job, nil
}

// RetryJob re-queues a failed job for another attempt.
// In sync mode, it executes the job immediately.
func (s *Scheduler) RetryJob(ctx context.Context, jobID string) (*domain.IngestionJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != domain.JobStatusFailed {
		return job, nil
	}
	if job.Attempts >= job.MaxAttempts {
		job.MaxAttempts = job.Attempts + 1
	}

	err = s.store.UpdateJobStatus(ctx, job.ID, domain.JobStatusQueued, storage.JobUpdate{})
	if err != nil {
		return nil, err
	}
	job.Status = domain.JobStatusQueued

	if !s.config.Async {
		// Sync mode: execute immediately and return final state
		s.executeJob(ctx, job)
		updated, err := s.store.GetJob(ctx, job.ID)
		if err != nil {
			return job, nil
		}
		return updated, nil
	}

	// Async: feeder will pick it up from DB
	return job, nil
}

func (s *Scheduler) worker(ctx context.Context, id int) {
	defer s.wg.Done()
	slog.Debug("scheduler worker started", "worker_id", id)

	for {
		select {
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case job, ok := <-s.jobs:
			if !ok {
				return
			}
			s.executeJob(ctx, job)
		}
	}
}

func (s *Scheduler) executeJob(ctx context.Context, job *domain.IngestionJob) {
	kbMu := s.getKBLock(job.KBID)
	kbMu.Lock()
	defer kbMu.Unlock()

	// Mark running (no-op if already claimed by DequeueJobs)
	if job.Status != domain.JobStatusRunning {
		if err := s.store.UpdateJobStatus(ctx, job.ID, domain.JobStatusRunning, storage.JobUpdate{}); err != nil {
			slog.Error("failed to mark job running", "job_id", job.ID, "error", err)
			return
		}
	}

	result, err := s.pipeline.Ingest(ctx, job.KBID, job.Content, IngestOptions{
		Source:   job.Source,
		Metadata: job.Metadata,
	})

	if err != nil {
		errMsg := truncateError(err.Error())
		if dbErr := s.store.UpdateJobStatus(ctx, job.ID, domain.JobStatusFailed, storage.JobUpdate{
			EpisodeID: resultEpisodeID(result),
			Error:     errMsg,
		}); dbErr != nil {
			slog.Error("failed to persist job failure", "job_id", job.ID, "error", dbErr)
		}
		job.Status = domain.JobStatusFailed
		job.Error = errMsg
		s.sendNotification(ctx, notify.EventJobFailed, job)
		return
	}

	resultJSON, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		slog.Error("failed to marshal ingest result", "job_id", job.ID, "error", marshalErr)
		resultJSON = nil
	}
	if dbErr := s.store.UpdateJobStatus(ctx, job.ID, domain.JobStatusCompleted, storage.JobUpdate{
		EpisodeID: result.EpisodeID,
		Result:    resultJSON,
	}); dbErr != nil {
		slog.Error("failed to persist job completion", "job_id", job.ID, "error", dbErr)
	}
	job.Status = domain.JobStatusCompleted
	job.EpisodeID = result.EpisodeID
	job.Result = resultJSON
	s.sendNotification(ctx, notify.EventJobCompleted, job)
}

func (s *Scheduler) feeder(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.FeedInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.feedQueuedJobs(ctx)
		}
	}
}

func (s *Scheduler) feedQueuedJobs(ctx context.Context) {
	// Only dequeue what the channel can actually receive to avoid
	// claiming jobs that can't be dispatched.
	available := cap(s.jobs) - len(s.jobs)
	if available <= 0 {
		return
	}

	jobs, err := s.store.DequeueJobs(ctx, available)
	if err != nil {
		slog.Debug("feeder: dequeue error", "error", err)
		return
	}
	for _, job := range jobs {
		s.jobs <- job
	}
}

func (s *Scheduler) getKBLock(kbID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mu, ok := s.kbLocks[kbID]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	s.kbLocks[kbID] = mu
	return mu
}

func (s *Scheduler) sendNotification(ctx context.Context, eventType string, job *domain.IngestionJob) {
	event := notify.Event{Type: eventType, Job: *job}
	if err := s.notifiers.Notify(ctx, event); err != nil {
		slog.Warn("notification failed", "event", eventType, "job_id", job.ID, "error", err)
	}
}

func resultEpisodeID(r *IngestResult) string {
	if r == nil {
		return ""
	}
	return r.EpisodeID
}

func truncateError(s string) string {
	if len(s) <= maxErrorLength {
		return s
	}
	return s[:maxErrorLength] + "... (truncated)"
}
