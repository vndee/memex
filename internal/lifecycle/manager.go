package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vndee/memex/internal/domain"
	"github.com/vndee/memex/internal/storage"
)

// ManagerConfig configures the background lifecycle manager.
type ManagerConfig struct {
	DecayInterval  time.Duration // how often to run decay (default 1h)
	PruneInterval  time.Duration // how often to run prune (default 24h)
	DecayHalfLife  float64       // half-life in hours (default 168 = 1 week)
	PruneThreshold float64       // minimum strength to keep (default 0.05)
}

func (c ManagerConfig) withDefaults() ManagerConfig {
	if c.DecayInterval <= 0 {
		c.DecayInterval = 1 * time.Hour
	}
	if c.PruneInterval <= 0 {
		c.PruneInterval = 24 * time.Hour
	}
	if c.DecayHalfLife <= 0 {
		c.DecayHalfLife = 168
	}
	if c.PruneThreshold <= 0 {
		c.PruneThreshold = 0.05
	}
	return c
}

// Manager runs periodic decay and prune cycles across all knowledge bases.
type Manager struct {
	store storage.Store
	cfg   ManagerConfig
}

// NewManager creates a lifecycle manager.
func NewManager(store storage.Store, cfg ManagerConfig) *Manager {
	return &Manager{
		store: store,
		cfg:   cfg.withDefaults(),
	}
}

// Run starts the lifecycle loop. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	decayTick := time.NewTicker(m.cfg.DecayInterval)
	pruneTick := time.NewTicker(m.cfg.PruneInterval)
	defer decayTick.Stop()
	defer pruneTick.Stop()

	slog.Info("lifecycle manager started",
		"decay_interval", m.cfg.DecayInterval,
		"prune_interval", m.cfg.PruneInterval,
		"half_life_hours", m.cfg.DecayHalfLife,
		"prune_threshold", m.cfg.PruneThreshold)

	for {
		select {
		case <-ctx.Done():
			slog.Info("lifecycle manager stopped")
			return
		case <-decayTick.C:
			m.runDecayAll(ctx)
		case <-pruneTick.C:
			m.runPruneAll(ctx)
		}
	}
}

// DecayKB runs a single decay pass on one knowledge base.
func (m *Manager) DecayKB(ctx context.Context, kbID string) (int64, error) {
	n, err := m.store.BatchUpdateDecayStrength(ctx, kbID, m.cfg.DecayHalfLife)
	if err != nil {
		return 0, fmt.Errorf("decay KB %q: %w", kbID, err)
	}
	return n, nil
}

// PruneKB runs a single prune pass on one knowledge base.
func (m *Manager) PruneKB(ctx context.Context, kbID string, threshold float64) (int, error) {
	if threshold <= 0 {
		threshold = m.cfg.PruneThreshold
	}
	return m.pruneKB(ctx, kbID, threshold)
}

func (m *Manager) runDecayAll(ctx context.Context) {
	kbs, err := m.store.ListKBs(ctx)
	if err != nil {
		slog.Error("lifecycle: failed to list KBs for decay", "error", err)
		return
	}

	for _, kb := range kbs {
		n, err := m.store.BatchUpdateDecayStrength(ctx, kb.ID, m.cfg.DecayHalfLife)
		if err != nil {
			slog.Error("lifecycle: decay failed", "kb_id", kb.ID, "error", err)
			continue
		}
		if n > 0 {
			slog.Info("lifecycle: decay complete", "kb_id", kb.ID, "updated", n)
		}
	}
}

func (m *Manager) runPruneAll(ctx context.Context) {
	kbs, err := m.store.ListKBs(ctx)
	if err != nil {
		slog.Error("lifecycle: failed to list KBs for prune", "error", err)
		return
	}

	for _, kb := range kbs {
		n, err := m.pruneKB(ctx, kb.ID, m.cfg.PruneThreshold)
		if err != nil {
			slog.Error("lifecycle: prune failed", "kb_id", kb.ID, "error", err)
			continue
		}
		if n > 0 {
			slog.Info("lifecycle: prune complete", "kb_id", kb.ID, "pruned", n)
		}
	}
}

func (m *Manager) pruneKB(ctx context.Context, kbID string, threshold float64) (int, error) {
	states, err := m.store.ListDecayStates(ctx, kbID, threshold)
	if err != nil {
		return 0, fmt.Errorf("list weak items: %w", err)
	}

	pruned := 0
	now := time.Now().UTC()
	for _, ds := range states {
		// Check if entity has incoming relations — protect connected nodes.
		if ds.EntityType == domain.ItemEntity {
			rels, err := m.store.GetRelationsForEntity(ctx, kbID, ds.EntityID)
			if err == nil && len(rels) > 0 {
				continue // skip: entity is still referenced
			}
		}

		var delErr error
		switch ds.EntityType {
		case domain.ItemEntity:
			delErr = m.store.DeleteEntity(ctx, kbID, ds.EntityID)
		case domain.ItemRelation:
			delErr = m.store.InvalidateRelation(ctx, kbID, ds.EntityID, now)
		case domain.ItemEpisode:
			delErr = m.store.DeleteEpisode(ctx, kbID, ds.EntityID)
		}
		if delErr != nil {
			slog.Error("lifecycle: prune item failed", "type", ds.EntityType, "id", ds.EntityID, "error", delErr)
			continue
		}

		if err := m.store.DeleteDecayState(ctx, kbID, ds.EntityType, ds.EntityID); err != nil {
			slog.Warn("lifecycle: failed to clean decay state", "id", ds.EntityID, "error", err)
		}
		pruned++
	}
	return pruned, nil
}
