package storage

import (
	"context"
	"database/sql"
	"fmt"
)

func requireEntityInKB(ctx context.Context, tx *sql.Tx, kbID, entityID string) error {
	return requireRecordInKB(ctx, tx, "entity", "entities", kbID, entityID)
}

func requireEpisodeInKB(ctx context.Context, tx *sql.Tx, kbID, episodeID string) error {
	return requireRecordInKB(ctx, tx, "episode", "episodes", kbID, episodeID)
}

func requireRecordInKB(ctx context.Context, tx *sql.Tx, label, table, kbID, id string) error {
	if kbID == "" || id == "" {
		return fmt.Errorf("%s lookup requires non-empty kb_id and id", label)
	}

	query := fmt.Sprintf(`SELECT 1 FROM %s WHERE kb_id = ? AND id = ?`, table)

	var exists int
	err := tx.QueryRowContext(ctx, query, kbID, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%s %q not found in knowledge base %q", label, id, kbID)
	}
	if err != nil {
		return fmt.Errorf("lookup %s: %w", label, err)
	}

	return nil
}
