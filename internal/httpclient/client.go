package httpclient

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

const (
	// DefaultTimeout for HTTP requests. LLM inference can be slow.
	DefaultTimeout = 120 * time.Second

	// maxErrorBody caps the bytes read from a non-200 response body.
	maxErrorBody = 4096

	// maxResponseBody caps the bytes decoded from a 200 response body (32 MiB).
	maxResponseBody = 32 << 20
)

// Client wraps http.Client with timeouts and safety defaults.
type Client struct {
	inner *http.Client
}

// New creates a Client with a default timeout.
func New() *Client {
	return &Client{
		inner: &http.Client{Timeout: DefaultTimeout},
	}
}

// NewWithTimeout creates a Client with a custom timeout.
func NewWithTimeout(timeout time.Duration) *Client {
	return &Client{
		inner: &http.Client{Timeout: timeout},
	}
}

// NewWithHTTPClient wraps an existing *http.Client (e.g., one with OAuth2 transport).
// Sets timeout if the provided client has none.
func NewWithHTTPClient(inner *http.Client) *Client {
	if inner.Timeout == 0 {
		inner.Timeout = DefaultTimeout
	}
	return &Client{inner: inner}
}

// ValidateBaseURL checks that the URL is well-formed and uses http or https.
func ValidateBaseURL(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid base URL scheme %q: must be http or https", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid base URL: missing host")
	}
	return nil
}

// DoJSON marshals reqBody as JSON, sends a POST to the given URL with headers,
// and decodes the response into respBody. Returns a descriptive error on failure.
func (c *Client) DoJSON(ctx context.Context, url string, headers map[string]string, reqBody, respBody any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.inner.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if readErr != nil {
			return fmt.Errorf("status %d (could not read body: %v)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(errBody))
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(respBody); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
