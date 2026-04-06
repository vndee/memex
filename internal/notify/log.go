package notify

import (
	"context"
	"log/slog"
)

// LogNotifier writes job events to structured log output.
type LogNotifier struct{}

func (LogNotifier) Notify(_ context.Context, event Event) error {
	attrs := []any{
		"job_id", event.Job.ID,
		"kb_id", event.Job.KBID,
		"status", event.Job.Status,
	}
	if event.Job.EpisodeID != "" {
		attrs = append(attrs, "episode_id", event.Job.EpisodeID)
	}

	switch event.Type {
	case EventJobCompleted:
		slog.Info("ingestion job completed", attrs...)
	case EventJobFailed:
		attrs = append(attrs, "error", event.Job.Error)
		slog.Error("ingestion job failed", attrs...)
	default:
		slog.Info("ingestion job event", append(attrs, "event", event.Type)...)
	}
	return nil
}
