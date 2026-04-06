package cloudauth

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/vndee/memex/internal/httpclient"
)

const (
	// GenAIBaseURL is the OpenAI-compatible endpoint for Google GenAI (Gemini).
	GenAIBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"

	// VertexScope is the OAuth2 scope required for Vertex AI API calls.
	VertexScope = "https://www.googleapis.com/auth/cloud-platform"
)

// ResolveGoogleAPIKey returns the first non-empty value from: explicit key,
// GOOGLE_API_KEY env var, GEMINI_API_KEY env var.
func ResolveGoogleAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("GEMINI_API_KEY")
}

// VertexBaseURL constructs the Vertex AI OpenAI-compatible base URL from env vars.
// Falls back to us-central1 if no location is set.
func VertexBaseURL() (string, error) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("CLOUDSDK_CORE_PROJECT")
	}
	if project == "" {
		return "", fmt.Errorf("vertex: GOOGLE_CLOUD_PROJECT env var required (or set base_url)")
	}

	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if location == "" {
		location = os.Getenv("CLOUD_ML_REGION")
	}
	if location == "" {
		location = "us-central1"
	}

	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1beta1/projects/%s/locations/%s/endpoints/openapi",
		location, project, location), nil
}

// VertexClient creates an HTTP client with ADC-based OAuth2 token refresh.
func VertexClient() (*httpclient.Client, error) {
	ts, err := google.DefaultTokenSource(context.Background(), VertexScope)
	if err != nil {
		return nil, fmt.Errorf("vertex: failed to get credentials (run 'gcloud auth application-default login'): %w", err)
	}
	httpClient := oauth2.NewClient(context.Background(), ts)
	return httpclient.NewWithHTTPClient(httpClient), nil
}
