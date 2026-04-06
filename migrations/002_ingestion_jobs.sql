-- Migration 002: Replace ingestion_tasks with ingestion_jobs
-- Adds async job tracking with full lifecycle state

CREATE TABLE IF NOT EXISTS ingestion_jobs (
    id           TEXT PRIMARY KEY,
    kb_id        TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'queued',
    content      TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'api',
    metadata     TEXT NOT NULL DEFAULT '{}',
    episode_id   TEXT DEFAULT NULL,
    result       TEXT DEFAULT NULL,
    error        TEXT NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at   TEXT DEFAULT NULL,
    completed_at TEXT DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_kb_status ON ingestion_jobs(kb_id, status);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON ingestion_jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_kb_created ON ingestion_jobs(kb_id, created_at DESC);

-- Migrate existing ingestion_tasks data if table exists
INSERT OR IGNORE INTO ingestion_jobs (id, kb_id, status, content, source, episode_id, error, attempts, created_at)
    SELECT
        'task-' || CAST(id AS TEXT),
        kb_id,
        'failed',
        '',
        COALESCE(json_extract(details, '$.source'), 'cli'),
        episode_id,
        error,
        attempts,
        created_at
    FROM ingestion_tasks;

DROP TABLE IF EXISTS ingestion_tasks;
