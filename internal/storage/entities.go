package storage

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) CreateEntity(ctx context.Context, e *domain.Entity) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO entities (id, kb_id, name, type, summary, embedding, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.KBID, e.Name, e.Type, e.Summary, encodeEmbedding(e.Embedding),
		e.CreatedAt.UTC().Format(time.RFC3339Nano),
		e.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert entity: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetEntity(ctx context.Context, kbID, id string) (*domain.Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kb_id, name, type, summary, embedding, created_at, updated_at
		 FROM entities WHERE kb_id = ? AND id = ?`, kbID, id)

	return scanEntity(row)
}

func (s *SQLiteStore) GetEntitiesByIDs(ctx context.Context, kbID string, ids []string) (map[string]*domain.Entity, error) {
	entities := make(map[string]*domain.Entity)
	if len(ids) == 0 {
		return entities, nil
	}

	const maxBatchIDs = 900
	for start := 0; start < len(ids); start += maxBatchIDs {
		end := min(start+maxBatchIDs, len(ids))
		batch := ids[start:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		query := `SELECT id, kb_id, name, type, summary, embedding, created_at, updated_at
			FROM entities WHERE kb_id = ? AND id IN (` + placeholders + `)`

		args := make([]any, 0, len(batch)+1)
		args = append(args, kbID)
		for _, id := range batch {
			args = append(args, id)
		}

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("query entities by ids: %w", err)
		}
		for rows.Next() {
			e, err := scanEntity(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			entities[e.ID] = e
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate entities by ids: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close entities by ids rows: %w", err)
		}
	}
	return entities, nil
}

func (s *SQLiteStore) UpdateEntity(ctx context.Context, e *domain.Entity) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE entities SET name = ?, type = ?, summary = ?, embedding = ?, updated_at = ?
		 WHERE kb_id = ? AND id = ?`,
		e.Name, e.Type, e.Summary, encodeEmbedding(e.Embedding),
		time.Now().UTC().Format(time.RFC3339Nano), e.KBID, e.ID,
	)
	if err != nil {
		return fmt.Errorf("update entity: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) DeleteEntity(ctx context.Context, kbID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entities WHERE kb_id = ? AND id = ?`, kbID, id)
	if err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) FindEntitiesByName(ctx context.Context, kbID, name string) ([]*domain.Entity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, name, type, summary, embedding, created_at, updated_at
		 FROM entities WHERE kb_id = ? AND LOWER(name) = LOWER(?)`,
		kbID, name)
	if err != nil {
		return nil, fmt.Errorf("query entities by name: %w", err)
	}
	defer rows.Close()

	return scanEntities(rows)
}

func (s *SQLiteStore) ListEntities(ctx context.Context, kbID string, limit, offset int) ([]*domain.Entity, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, name, type, summary, embedding, created_at, updated_at
		 FROM entities WHERE kb_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		kbID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query entities: %w", err)
	}
	defer rows.Close()

	return scanEntities(rows)
}

// ListEntityNames returns all entities for a KB with only id, name, type, summary populated.
// Skips embedding BLOBs for efficiency during entity resolution.
func (s *SQLiteStore) ListEntityNames(ctx context.Context, kbID string) ([]*domain.Entity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kb_id, name, type, summary FROM entities WHERE kb_id = ?`, kbID)
	if err != nil {
		return nil, fmt.Errorf("query entity names: %w", err)
	}
	defer rows.Close()

	var entities []*domain.Entity
	for rows.Next() {
		var e domain.Entity
		if err := rows.Scan(&e.ID, &e.KBID, &e.Name, &e.Type, &e.Summary); err != nil {
			return nil, fmt.Errorf("scan entity name: %w", err)
		}
		entities = append(entities, &e)
	}
	return entities, rows.Err()
}

func scanEntity(row scanner) (*domain.Entity, error) {
	var e domain.Entity
	var embBlob []byte
	var createdAt, updatedAt string

	err := row.Scan(&e.ID, &e.KBID, &e.Name, &e.Type, &e.Summary, &embBlob, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("scan entity: %w", err)
	}

	e.Embedding = decodeEmbedding(embBlob)
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &e, nil
}

func scanEntities(rows *sql.Rows) ([]*domain.Entity, error) {
	var entities []*domain.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

// Embedding encoding: little-endian float32 array, no header.
func encodeEmbedding(vec []float32) []byte {
	if len(vec) == 0 {
		return nil
	}
	b := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	return b
}

func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	n := len(b) / 4
	vec := make([]float32, n)
	for i := range n {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return vec
}
