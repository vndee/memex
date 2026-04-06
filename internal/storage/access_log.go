package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) LogAccess(ctx context.Context, kbID, entityType, entityID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Insert access log entry
	_, err = tx.ExecContext(ctx,
		`INSERT INTO memory_access_log (kb_id, entity_type, entity_id, accessed_at)
		 VALUES (?, ?, ?, ?)`,
		kbID, entityType, entityID, now)
	if err != nil {
		return fmt.Errorf("insert access log: %w", err)
	}

	// Upsert decay state
	_, err = tx.ExecContext(ctx,
		`INSERT INTO decay_state (kb_id, entity_type, entity_id, strength, access_count, last_access)
		 VALUES (?, ?, ?, 1.0, 1, ?)
		 ON CONFLICT(kb_id, entity_type, entity_id) DO UPDATE SET
		   access_count = access_count + 1,
		   last_access = excluded.last_access,
		   strength = 1.0`,
		kbID, entityType, entityID, now)
	if err != nil {
		return fmt.Errorf("upsert decay state: %w", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) GetDecayState(ctx context.Context, kbID, entityType, entityID string) (*domain.DecayState, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT kb_id, entity_type, entity_id, strength, access_count, last_access
		 FROM decay_state WHERE kb_id = ? AND entity_type = ? AND entity_id = ?`,
		kbID, entityType, entityID)

	var ds domain.DecayState
	var lastAccess string
	err := row.Scan(&ds.KBID, &ds.EntityType, &ds.EntityID, &ds.Strength, &ds.AccessCount, &lastAccess)
	if err != nil {
		return nil, fmt.Errorf("scan decay state: %w", err)
	}
	ds.LastAccess, _ = time.Parse(time.RFC3339Nano, lastAccess)
	return &ds, nil
}

func (s *SQLiteStore) UpdateDecayState(ctx context.Context, ds *domain.DecayState) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE decay_state SET strength = ?, access_count = ?, last_access = ?
		 WHERE kb_id = ? AND entity_type = ? AND entity_id = ?`,
		ds.Strength, ds.AccessCount, ds.LastAccess.UTC().Format(time.RFC3339Nano),
		ds.KBID, ds.EntityType, ds.EntityID)
	if err != nil {
		return fmt.Errorf("update decay state: %w", err)
	}
	return nil
}

// DeleteDecayState removes the decay state for a specific item.
func (s *SQLiteStore) DeleteDecayState(ctx context.Context, kbID, entityType, entityID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM decay_state WHERE kb_id = ? AND entity_type = ? AND entity_id = ?`,
		kbID, entityType, entityID)
	if err != nil {
		return fmt.Errorf("delete decay state: %w", err)
	}
	return nil
}

// BatchUpdateDecayStrength applies exponential decay to all items in a KB in a single SQL UPDATE.
// Returns the number of rows updated.
func (s *SQLiteStore) BatchUpdateDecayStrength(ctx context.Context, kbID string, halfLifeHours float64) (int64, error) {
	if halfLifeHours <= 0 {
		return 0, nil
	}
	// SQLite's exp() is available since 3.35.0.
	// decay_factor = exp(-ln(2)/halfLife * hours_since_last_access)
	// hours_since = (julianday('now') - julianday(last_access)) * 24
	res, err := s.db.ExecContext(ctx, `
		UPDATE decay_state
		SET strength = strength * exp(-0.693147180559945 / ? * (julianday('now') - julianday(last_access)) * 24.0)
		WHERE kb_id = ? AND strength > 0.001`,
		halfLifeHours, kbID)
	if err != nil {
		return 0, fmt.Errorf("batch update decay strength: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *SQLiteStore) ListDecayStates(ctx context.Context, kbID string, maxStrength float64) ([]*domain.DecayState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kb_id, entity_type, entity_id, strength, access_count, last_access
		 FROM decay_state WHERE kb_id = ? AND strength <= ?
		 ORDER BY strength ASC`,
		kbID, maxStrength)
	if err != nil {
		return nil, fmt.Errorf("query decay states: %w", err)
	}
	defer rows.Close()

	var states []*domain.DecayState
	for rows.Next() {
		var ds domain.DecayState
		var lastAccess string
		if err := rows.Scan(&ds.KBID, &ds.EntityType, &ds.EntityID, &ds.Strength, &ds.AccessCount, &lastAccess); err != nil {
			return nil, fmt.Errorf("scan decay state: %w", err)
		}
		ds.LastAccess, _ = time.Parse(time.RFC3339Nano, lastAccess)
		states = append(states, &ds)
	}
	return states, rows.Err()
}
