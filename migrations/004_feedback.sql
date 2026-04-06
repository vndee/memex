-- Feedback/correction tracking for closed-loop learning.
CREATE TABLE IF NOT EXISTS feedback (
    id TEXT PRIMARY KEY,
    kb_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    topic TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    correction TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'mcp',
    metadata TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_feedback_kb ON feedback(kb_id);
CREATE INDEX IF NOT EXISTS idx_feedback_kb_topic ON feedback(kb_id, topic);

-- FTS5 for searching feedback content and corrections.
CREATE VIRTUAL TABLE IF NOT EXISTS feedback_fts USING fts5(
    topic,
    content,
    correction,
    content='feedback',
    content_rowid='rowid'
);

-- Triggers to keep FTS in sync.
CREATE TRIGGER IF NOT EXISTS feedback_ai AFTER INSERT ON feedback BEGIN
    INSERT INTO feedback_fts(rowid, topic, content, correction)
    VALUES (new.rowid, new.topic, new.content, new.correction);
END;

CREATE TRIGGER IF NOT EXISTS feedback_ad AFTER DELETE ON feedback BEGIN
    INSERT INTO feedback_fts(feedback_fts, rowid, topic, content, correction)
    VALUES ('delete', old.rowid, old.topic, old.content, old.correction);
END;

CREATE TRIGGER IF NOT EXISTS feedback_au AFTER UPDATE ON feedback BEGIN
    INSERT INTO feedback_fts(feedback_fts, rowid, topic, content, correction)
    VALUES ('delete', old.rowid, old.topic, old.content, old.correction);
    INSERT INTO feedback_fts(rowid, topic, content, correction)
    VALUES (new.rowid, new.topic, new.content, new.correction);
END;
