-- Memex: Temporal Knowledge Graph Memory Layer
-- Schema v1: Multi-KB with bitemporal relations and FTS5

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Knowledge Bases
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    embed_config TEXT NOT NULL, -- JSON: {provider, model, base_url, api_key, dim}
    llm_config   TEXT NOT NULL, -- JSON: {provider, model, base_url, api_key}
    settings    TEXT NOT NULL DEFAULT '{}', -- JSON: per-KB overrides
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Episodes: raw interaction data
CREATE TABLE IF NOT EXISTS episodes (
    id          TEXT PRIMARY KEY,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    content     TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'api',
    metadata    TEXT NOT NULL DEFAULT '{}', -- JSON
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (kb_id, id)
);
CREATE INDEX IF NOT EXISTS idx_episodes_kb ON episodes(kb_id);
CREATE INDEX IF NOT EXISTS idx_episodes_created ON episodes(kb_id, created_at);

-- Entities: extracted knowledge graph nodes
CREATE TABLE IF NOT EXISTS entities (
    id          TEXT PRIMARY KEY,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT 'concept',
    summary     TEXT NOT NULL DEFAULT '',
    embedding   BLOB,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (kb_id, id)
);
CREATE INDEX IF NOT EXISTS idx_entities_kb ON entities(kb_id);
CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(kb_id, name);
CREATE INDEX IF NOT EXISTS idx_entities_type ON entities(kb_id, type);

-- Relations: bitemporal edges
CREATE TABLE IF NOT EXISTS relations (
    id          TEXT PRIMARY KEY,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    source_id   TEXT NOT NULL,
    target_id   TEXT NOT NULL,
    type        TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    weight      REAL NOT NULL DEFAULT 1.0,
    embedding   BLOB,
    episode_id  TEXT REFERENCES episodes(id) ON DELETE SET NULL,
    valid_at    TEXT NOT NULL,
    invalid_at  TEXT,            -- NULL = still valid
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY (kb_id, source_id) REFERENCES entities(kb_id, id) ON DELETE CASCADE,
    FOREIGN KEY (kb_id, target_id) REFERENCES entities(kb_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_relations_kb ON relations(kb_id);
CREATE INDEX IF NOT EXISTS idx_relations_source ON relations(kb_id, source_id);
CREATE INDEX IF NOT EXISTS idx_relations_target ON relations(kb_id, target_id);
CREATE INDEX IF NOT EXISTS idx_relations_type ON relations(kb_id, type);
CREATE INDEX IF NOT EXISTS idx_relations_valid ON relations(kb_id, valid_at);

-- Communities: cluster summaries
CREATE TABLE IF NOT EXISTS communities (
    id          TEXT PRIMARY KEY,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    member_ids  TEXT NOT NULL DEFAULT '[]', -- JSON array of entity IDs
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_communities_kb ON communities(kb_id);

-- Memory access log for decay scoring
CREATE TABLE IF NOT EXISTS memory_access_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL, -- 'entity', 'relation', 'episode'
    entity_id   TEXT NOT NULL,
    accessed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_access_entity ON memory_access_log(kb_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_access_time ON memory_access_log(accessed_at);

-- Decay state: computed summary per item
CREATE TABLE IF NOT EXISTS decay_state (
    kb_id        TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    strength     REAL NOT NULL DEFAULT 1.0,
    access_count INTEGER NOT NULL DEFAULT 1,
    last_access  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (kb_id, entity_type, entity_id)
);

-- Deferred ingestion work after degraded episode-only storage
CREATE TABLE IF NOT EXISTS ingestion_tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kb_id       TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    episode_id  TEXT NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    stage       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    details     TEXT NOT NULL DEFAULT '{}', -- JSON
    error       TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (episode_id, stage)
);
CREATE INDEX IF NOT EXISTS idx_ingestion_tasks_kb_status ON ingestion_tasks(kb_id, status, updated_at);

-- FTS5 virtual tables for BM25 keyword search
CREATE VIRTUAL TABLE IF NOT EXISTS entities_fts USING fts5(
    name, summary, type,
    content='entities',
    content_rowid='rowid'
);

CREATE VIRTUAL TABLE IF NOT EXISTS relations_fts USING fts5(
    type, summary,
    content='relations',
    content_rowid='rowid'
);

CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(
    content, source,
    content='episodes',
    content_rowid='rowid'
);

-- Triggers: keep FTS5 in sync with base tables

-- Entities FTS triggers
CREATE TRIGGER IF NOT EXISTS entities_ai AFTER INSERT ON entities BEGIN
    INSERT INTO entities_fts(rowid, name, summary, type)
    VALUES (new.rowid, new.name, new.summary, new.type);
END;

CREATE TRIGGER IF NOT EXISTS entities_ad AFTER DELETE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, name, summary, type)
    VALUES ('delete', old.rowid, old.name, old.summary, old.type);
END;

CREATE TRIGGER IF NOT EXISTS entities_au AFTER UPDATE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, name, summary, type)
    VALUES ('delete', old.rowid, old.name, old.summary, old.type);
    INSERT INTO entities_fts(rowid, name, summary, type)
    VALUES (new.rowid, new.name, new.summary, new.type);
END;

-- Relations FTS triggers
CREATE TRIGGER IF NOT EXISTS relations_ai AFTER INSERT ON relations BEGIN
    INSERT INTO relations_fts(rowid, type, summary)
    VALUES (new.rowid, new.type, new.summary);
END;

CREATE TRIGGER IF NOT EXISTS relations_ad AFTER DELETE ON relations BEGIN
    INSERT INTO relations_fts(relations_fts, rowid, type, summary)
    VALUES ('delete', old.rowid, old.type, old.summary);
END;

CREATE TRIGGER IF NOT EXISTS relations_au AFTER UPDATE ON relations BEGIN
    INSERT INTO relations_fts(relations_fts, rowid, type, summary)
    VALUES ('delete', old.rowid, old.type, old.summary);
    INSERT INTO relations_fts(rowid, type, summary)
    VALUES (new.rowid, new.type, new.summary);
END;

-- Episodes FTS triggers
CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN
    INSERT INTO episodes_fts(rowid, content, source)
    VALUES (new.rowid, new.content, new.source);
END;

CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN
    INSERT INTO episodes_fts(episodes_fts, rowid, content, source)
    VALUES ('delete', old.rowid, old.content, old.source);
END;

CREATE TRIGGER IF NOT EXISTS episodes_au AFTER UPDATE ON episodes BEGIN
    INSERT INTO episodes_fts(episodes_fts, rowid, content, source)
    VALUES ('delete', old.rowid, old.content, old.source);
    INSERT INTO episodes_fts(rowid, content, source)
    VALUES (new.rowid, new.content, new.source);
END;
