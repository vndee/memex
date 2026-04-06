package storage

import (
	"context"
	"fmt"

	"github.com/vndee/memex/internal/domain"
)

func (s *SQLiteStore) GetStats(ctx context.Context, kbID string) (*domain.MemoryStats, error) {
	stats := &domain.MemoryStats{KBID: kbID}

	var err error
	if kbID != "" {
		err = s.db.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM episodes WHERE kb_id = ?),
				(SELECT COUNT(*) FROM entities WHERE kb_id = ?),
				(SELECT COUNT(*) FROM relations WHERE kb_id = ?),
				(SELECT COUNT(*) FROM communities WHERE kb_id = ?)`,
			kbID, kbID, kbID, kbID,
		).Scan(&stats.TotalEpisodes, &stats.TotalEntities, &stats.TotalRelations, &stats.TotalCommunities)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM episodes),
				(SELECT COUNT(*) FROM entities),
				(SELECT COUNT(*) FROM relations),
				(SELECT COUNT(*) FROM communities)`,
		).Scan(&stats.TotalEpisodes, &stats.TotalEntities, &stats.TotalRelations, &stats.TotalCommunities)
	}
	if err != nil {
		return nil, fmt.Errorf("count stats: %w", err)
	}

	size, err := s.DBSize()
	if err == nil {
		stats.DBSizeBytes = size
	}

	return stats, nil
}
