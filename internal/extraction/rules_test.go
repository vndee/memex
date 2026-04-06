package extraction

import (
	"context"
	"testing"
)

func TestRuleProvider_BashError(t *testing.T) {
	rp := NewRuleProvider()
	tests := []struct {
		name       string
		input      string
		wantMatch  bool
		wantEntity string
	}{
		{
			name:       "command not found",
			input:      "$ foo\nbash: foo: command not found",
			wantMatch:  true,
			wantEntity: "foo",
		},
		{
			name:       "permission denied",
			input:      "error: permission denied for /etc/shadow",
			wantMatch:  true,
			wantEntity: "permission denied for /etc/shadow",
		},
		{
			name:       "exit code",
			input:      "Process exited with exit code 1",
			wantMatch:  true,
			wantEntity: "Process exited with exit code 1",
		},
		{
			name:      "plain text no error",
			input:     "The weather is nice today",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := rp.Extract(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			hasEntities := len(result.Entities) > 0
			if hasEntities != tt.wantMatch {
				t.Errorf("match = %v, want %v", hasEntities, tt.wantMatch)
			}

			if tt.wantMatch && tt.wantEntity != "" {
				found := false
				for _, e := range result.Entities {
					if e.Name == tt.wantEntity {
						found = true
						break
					}
				}
				if !found {
					names := make([]string, len(result.Entities))
					for i, e := range result.Entities {
						names[i] = e.Name
					}
					t.Errorf("expected entity %q not found in %v", tt.wantEntity, names)
				}
			}
		})
	}
}

func TestRuleProvider_GitCommit(t *testing.T) {
	rp := NewRuleProvider()

	input := `commit 41e17e8abcdef1234567890
Author: Duy Huynh <duy@example.com>

    Fix authentication bug in login flow

diff --git a/internal/auth/login.go b/internal/auth/login.go
diff --git a/internal/auth/token.go b/internal/auth/token.go`

	result, err := rp.Extract(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Entities) == 0 {
		t.Fatal("expected entities from git commit")
	}

	// Should have author, commit message, and files
	types := make(map[string]bool)
	for _, e := range result.Entities {
		types[e.Type] = true
	}
	if !types["person"] {
		t.Error("expected person entity for author")
	}

	// Should have modified relations
	if len(result.Relations) == 0 {
		t.Error("expected relations for modified files")
	}

	for _, r := range result.Relations {
		if r.Type != "modified" {
			t.Errorf("expected relation type 'modified', got %q", r.Type)
		}
	}
}

func TestRuleProvider_Dependency(t *testing.T) {
	rp := NewRuleProvider()

	input := "npm install express@4.18.0"
	result, err := rp.Extract(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Entities) == 0 {
		t.Fatal("expected entities from dependency install")
	}

	found := false
	for _, e := range result.Entities {
		if e.Name == "express@4.18.0" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected entity 'express@4.18.0'")
	}

	if len(result.Relations) == 0 {
		t.Fatal("expected depends_on relation")
	}
	if result.Relations[0].Type != "depends_on" {
		t.Errorf("expected relation type 'depends_on', got %q", result.Relations[0].Type)
	}
}

func TestRuleProvider_Decision(t *testing.T) {
	rp := NewRuleProvider()

	input := "We decided to use PostgreSQL instead of MySQL for the new service"
	result, err := rp.Extract(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Entities) == 0 {
		t.Fatal("expected entity from decision")
	}

	if result.Entities[0].Type != "concept" {
		t.Errorf("expected type 'concept', got %q", result.Entities[0].Type)
	}
}

func TestRuleProvider_NoFalsePositives(t *testing.T) {
	rp := NewRuleProvider()

	inputs := []string{
		"Hello, how are you?",
		"The function returns a list of items.",
		"Processing complete successfully.",
		"All tests passed.",
	}

	for _, input := range inputs {
		result, err := rp.Extract(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Entities) > 0 {
			t.Errorf("false positive on %q: got %d entities", input, len(result.Entities))
		}
	}
}

func TestRuleProvider_HasMatch(t *testing.T) {
	rp := NewRuleProvider()

	if !rp.HasMatch("error: something failed") {
		t.Error("expected match for error text")
	}
	if rp.HasMatch("all good here") {
		t.Error("unexpected match for plain text")
	}
}

func TestRuleProvider_Summarize(t *testing.T) {
	rp := NewRuleProvider()

	long := string(make([]byte, 300))
	for i := range long {
		long = long[:i] + "a"
	}

	summary, err := rp.Summarize(context.Background(), "short text")
	if err != nil {
		t.Fatal(err)
	}
	if summary != "short text" {
		t.Errorf("expected 'short text', got %q", summary)
	}
}
