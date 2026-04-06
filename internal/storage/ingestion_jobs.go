package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/vndee/memex/internal/domain"
)

const jobColumns = `id, kb_id, status, content, source, metadata, episode_id, result, error, attempts, max_attempts, created_at, started_at, completed_at`

func (s *SQLiteStore) CreateJob(ctx context.Context, job *domain.IngestionJob) error {
	if job.Status == "" {
		job.Status = domain.JobStatusQueued
	}
	metaJSON, err := json.Marshal(job.Metadata)
	if err != nil {
		return fmt.Errorf("marshal job metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ingestion_jobs (id, kb_id, status, content, source, metadata, episode_id, result, error, attempts, max_attempts, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.KBID, job.Status, job.Content, job.Source, string(metaJSON),
		nilIfEmpty(job.EpisodeID), nullJSON(job.Result), job.Error,
		job.Attempts, job.MaxAttempts, nowRFC3339())
	if err != nil {
		return fmt.Errorf("create ingestion job: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetJob(ctx context.Context, id string) (*domain.IngestionJob, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM ingestion_jobs WHERE id = ?`, id)
	return scanJob(row)
}

const maxListLimit = 10_000

func (s *SQLiteStore) ListJobs(ctx context.Context, kbID, status string, limit int) ([]*domain.IngestionJob, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	query := `SELECT ` + jobColumns + ` FROM ingestion_jobs`
	args := make([]any, 0, 3)

	switch {
	case kbID != "" && status != "":
		query += ` WHERE kb_id = ? AND status = ?`
		args = append(args, kbID, status)
	case kbID != "":
		query += ` WHERE kb_id = ?`
		args = append(args, kbID)
	case status != "":
		query += ` WHERE status = ?`
		args = append(args, status)
	}

	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ingestion jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.IngestionJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) UpdateJobStatus(ctx context.Context, id, status string, updates JobUpdate) error {
	now := nowRFC3339()

	switch status {
	case domain.JobStatusRunning:
		_, err := s.db.ExecContext(ctx,
			`UPDATE ingestion_jobs SET status = ?, started_at = ?, attempts = attempts + 1 WHERE id = ?`,
			status, now, id)
		if err != nil {
			return fmt.Errorf("mark job %s running: %w", id, err)
		}
		return nil

	case domain.JobStatusCompleted:
		_, err := s.db.ExecContext(ctx,
			`UPDATE ingestion_jobs SET status = ?, completed_at = ?, episode_id = ?, result = ? WHERE id = ?`,
			status, now, nilIfEmpty(updates.EpisodeID), nullJSON(updates.Result), id)
		if err != nil {
			return fmt.Errorf("mark job %s completed: %w", id, err)
		}
		return nil

	case domain.JobStatusFailed:
		_, err := s.db.ExecContext(ctx,
			`UPDATE ingestion_jobs SET status = ?, completed_at = ?, episode_id = ?, error = ? WHERE id = ?`,
			status, now, nilIfEmpty(updates.EpisodeID), updates.Error, id)
		if err != nil {
			return fmt.Errorf("mark job %s failed: %w", id, err)
		}
		return nil

	case domain.JobStatusQueued:
		_, err := s.db.ExecContext(ctx,
			`UPDATE ingestion_jobs SET status = ?, started_at = NULL, completed_at = NULL WHERE id = ?`,
			status, id)
		if err != nil {
			return fmt.Errorf("reset job %s to queued: %w", id, err)
		}
		return nil

	default:
		return fmt.Errorf("unknown job status: %q", status)
	}
}

// RecoverStaleJobs resets jobs stuck in "running" for more than 5 minutes
// (e.g. after crash) back to "queued" for retry.
func (s *SQLiteStore) RecoverStaleJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ingestion_jobs SET status = ?, started_at = NULL
		 WHERE status = ? AND attempts < max_attempts
		   AND started_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-5 minutes')`,
		domain.JobStatusQueued, domain.JobStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("recover stale jobs: %w", err)
	}
	return res.RowsAffected()
}

// DequeueJobs atomically claims up to limit "queued" jobs by setting them to "running".
// All data is read in a single transaction to avoid N+1 re-reads.
func (s *SQLiteStore) DequeueJobs(ctx context.Context, limit int) ([]*domain.IngestionJob, error) {
	if limit <= 0 {
		limit = 10
	}

	now := nowRFC3339()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dequeue jobs: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read full rows in the transaction (avoids N+1 re-reads after commit)
	rows, err := tx.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM ingestion_jobs WHERE status = ? ORDER BY created_at ASC LIMIT ?`,
		domain.JobStatusQueued, limit)
	if err != nil {
		return nil, fmt.Errorf("dequeue jobs: query: %w", err)
	}

	var jobs []*domain.IngestionJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dequeue jobs: rows: %w", err)
	}
	if len(jobs) == 0 {
		return nil, tx.Commit()
	}

	// Claim all in one UPDATE with IN clause
	ids := make([]any, len(jobs))
	placeholders := ""
	for i, j := range jobs {
		ids[i] = j.ID
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}
	args := make([]any, 0, 2+len(ids))
	args = append(args, domain.JobStatusRunning, now)
	args = append(args, ids...)

	_, err = tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE ingestion_jobs SET status = ?, started_at = ?, attempts = attempts + 1 WHERE id IN (%s)`, placeholders),
		args...)
	if err != nil {
		return nil, fmt.Errorf("dequeue jobs: claim: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("dequeue jobs: commit: %w", err)
	}

	// Update in-memory status to match what was persisted
	for _, j := range jobs {
		j.Status = domain.JobStatusRunning
		j.Attempts++
	}
	return jobs, nil
}

// JobUpdate holds optional fields for status transitions.
type JobUpdate struct {
	EpisodeID string
	Result    json.RawMessage
	Error     string
}

func scanJob(row scanner) (*domain.IngestionJob, error) {
	var job domain.IngestionJob
	var metaJSON string
	var episodeID, resultJSON, startedAt, completedAt sql.NullString
	var createdAt string

	err := row.Scan(
		&job.ID, &job.KBID, &job.Status, &job.Content, &job.Source,
		&metaJSON, &episodeID, &resultJSON, &job.Error,
		&job.Attempts, &job.MaxAttempts, &createdAt, &startedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("scan ingestion job: %w", err)
	}

	if metaJSON != "" && metaJSON != "{}" {
		_ = json.Unmarshal([]byte(metaJSON), &job.Metadata)
	}
	if episodeID.Valid {
		job.EpisodeID = episodeID.String
	}
	if resultJSON.Valid {
		job.Result = json.RawMessage(resultJSON.String)
	}
	job.CreatedAt = parseTime(createdAt)
	job.StartedAt = parseTimePtr(startedAt)
	job.CompletedAt = parseTimePtr(completedAt)
	return &job, nil
}

func nullJSON(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}
