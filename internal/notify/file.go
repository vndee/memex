package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileNotifier appends JSONL events to a file.
type FileNotifier struct {
	mu   sync.Mutex
	file *os.File
}

// NewFileNotifier creates a file notifier that appends to the given path.
// The path must be absolute and the file is created with 0600 permissions.
func NewFileNotifier(path string) (*FileNotifier, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("notify file path must be absolute: %s", path)
	}

	file, err := os.OpenFile(clean, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("notify file: open %s: %w", clean, err)
	}
	return &FileNotifier{file: file}, nil
}

// Close closes the underlying file.
func (f *FileNotifier) Close() error {
	return f.file.Close()
}

func (f *FileNotifier) Notify(_ context.Context, event Event) error {
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("file notify: marshal: %w", err)
	}
	line = append(line, '\n')

	f.mu.Lock()
	defer f.mu.Unlock()

	_, err = f.file.Write(line)
	if err != nil {
		return fmt.Errorf("file notify: write: %w", err)
	}
	return nil
}
