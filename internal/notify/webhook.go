package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const defaultWebhookTimeout = 5 * time.Second

// WebhookNotifier sends a POST request with the event JSON to a URL.
type WebhookNotifier struct {
	URL    string
	client *http.Client
}

// NewWebhookNotifier creates a webhook notifier. The URL must use http or https.
func NewWebhookNotifier(rawURL string, timeout time.Duration) (*WebhookNotifier, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("webhook: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("webhook: URL scheme must be http or https, got %q", u.Scheme)
	}
	if timeout <= 0 {
		timeout = defaultWebhookTimeout
	}
	return &WebhookNotifier{
		URL: rawURL,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (w *WebhookNotifier) Notify(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("webhook: marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: POST %s: %w", w.URL, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook: POST %s returned %d", w.URL, resp.StatusCode)
	}
	return nil
}
