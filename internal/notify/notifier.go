package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vndee/memex/internal/domain"
)

// Event type constants.
const (
	EventJobCompleted = "job.completed"
	EventJobFailed    = "job.failed"
)

// Event describes something that happened to an ingestion job.
type Event struct {
	Type string             `json:"type"`
	Job  domain.IngestionJob `json:"job"`
}

// Notifier sends notifications about job lifecycle events.
type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

// Multi fans out to multiple notifiers. Individual errors are logged
// and all notifiers are attempted regardless of failures.
type Multi []Notifier

func (m Multi) Notify(ctx context.Context, event Event) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, event); err != nil {
			slog.Warn("notifier error", "notifier", fmt.Sprintf("%T", n), "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
