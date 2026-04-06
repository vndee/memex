package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"
	"sync/atomic"

	"github.com/vndee/memex/internal/extraction"
	"github.com/vndee/memex/internal/ingestion"
	"github.com/vndee/memex/internal/search"
)

// Handler processes hook events from AI editor tool lifecycle.
type Handler struct {
	sched    *ingestion.Scheduler
	searcher *search.Searcher
	rules    *extraction.RuleProvider
	counter  atomic.Int64
}

// NewHandler creates a hook handler with the given dependencies.
func NewHandler(sched *ingestion.Scheduler, searcher *search.Searcher) *Handler {
	return &Handler{
		sched:    sched,
		searcher: searcher,
		rules:    extraction.NewRuleProvider(),
	}
}

// PostToolUse processes output from a tool invocation.
// It reads tool output from stdin, runs rule-based extraction,
// and stores the result if entities are found.
// Throttled: only processes every Nth call (default 1 = every call).
func (h *Handler) PostToolUse(ctx context.Context, kbID string, throttleN int64) error {
	if throttleN <= 0 {
		throttleN = 1
	}

	n := h.counter.Add(1)
	if n%throttleN != 0 {
		return nil // skip this invocation
	}

	text, err := readStdin()
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if text == "" {
		return nil
	}

	// Only store if rule-based extraction finds something interesting.
	if !h.rules.HasMatch(text) {
		return nil
	}

	job, err := h.sched.Submit(ctx, kbID, text, ingestion.IngestOptions{
		Source: "hook:post_tool_use",
	})
	if err != nil {
		return fmt.Errorf("submit hook content: %w", err)
	}

	slog.Info("hook stored", "event", "post_tool_use", "job", job.ID, "status", job.Status)
	return nil
}

// PreCompact processes conversation transcript before context compression.
// This is the "save everything important before context is lost" moment.
func (h *Handler) PreCompact(ctx context.Context, kbID string) error {
	text, err := readStdin()
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if text == "" {
		return nil
	}

	// Truncate very long transcripts to avoid overwhelming the pipeline.
	const maxBytes = 50000
	if len(text) > maxBytes {
		// Walk back to avoid splitting a multi-byte UTF-8 character.
		i := maxBytes
		for i > 0 && !utf8.RuneStart(text[i]) {
			i--
		}
		text = text[:i]
	}

	job, err := h.sched.Submit(ctx, kbID, text, ingestion.IngestOptions{
		Source: "hook:pre_compact",
	})
	if err != nil {
		return fmt.Errorf("submit compact content: %w", err)
	}

	slog.Info("hook stored", "event", "pre_compact", "job", job.ID, "status", job.Status)
	return nil
}

// UserPromptSubmit searches for relevant memories and outputs them as context.
// The output is written to stdout for the editor to inject into the conversation.
func (h *Handler) UserPromptSubmit(ctx context.Context, kbID string) error {
	text, err := readStdin()
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if text == "" {
		return nil
	}

	// Search for relevant memories.
	opts := search.DefaultOptions()
	opts.TopK = 5
	results, err := h.searcher.Search(ctx, kbID, text, opts)
	if err != nil {
		slog.Warn("hook search failed", "error", err)
		return nil // don't block the user prompt on search failures
	}

	if len(results) == 0 {
		return nil
	}

	// Output memories as structured context.
	output := struct {
		Memories []memoryEntry `json:"memories"`
	}{
		Memories: make([]memoryEntry, 0, len(results)),
	}

	for _, r := range results {
		output.Memories = append(output.Memories, memoryEntry{
			Type:    r.Type,
			Content: r.Content,
			Score:   r.Score,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(output)
}

type memoryEntry struct {
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

const maxStdinBytes = 1 << 20 // 1 MiB — same as maxContentBytes in server

func readStdin() (string, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		// No piped input
		return "", nil
	}

	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
