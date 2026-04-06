-- Migration 003: Partial index for relation deduplication.
-- Accelerates lookup of active (non-invalidated) edges by (source, target, type).
CREATE INDEX IF NOT EXISTS idx_relations_active_edge
    ON relations(kb_id, source_id, target_id, type)
    WHERE invalid_at IS NULL;
