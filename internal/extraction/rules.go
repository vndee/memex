package extraction

import (
	"context"
	"regexp"
	"strings"
)

// PatternMatcher detects a specific content pattern and extracts entities/relations.
type PatternMatcher struct {
	Name    string
	Match   func(text string) bool
	Extract func(text string) *ExtractionResult
}

// RuleProvider extracts entities and relations using regex-based pattern matching,
// requiring zero LLM calls. It implements the Provider interface.
type RuleProvider struct {
	matchers []PatternMatcher
}

// NewRuleProvider creates a rule-based extraction provider with all built-in patterns.
func NewRuleProvider() *RuleProvider {
	return &RuleProvider{
		matchers: []PatternMatcher{
			bashErrorMatcher(),
			gitCommitMatcher(),
			configChangeMatcher(),
			dependencyMatcher(),
			decisionMatcher(),
			preferenceMatcher(),
		},
	}
}

// Extract tries each pattern matcher and returns the first non-empty result.
// Returns an empty result (not an error) if no patterns match.
func (r *RuleProvider) Extract(_ context.Context, text string) (*ExtractionResult, error) {
	combined := &ExtractionResult{}
	for _, m := range r.matchers {
		if m.Match(text) {
			result := m.Extract(text)
			if result != nil && (len(result.Entities) > 0 || len(result.Relations) > 0) {
				combined.Entities = append(combined.Entities, result.Entities...)
				combined.Relations = append(combined.Relations, result.Relations...)
			}
		}
	}
	return combined, nil
}

// Summarize returns the first 200 characters of text (no LLM needed).
func (r *RuleProvider) Summarize(_ context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if len(text) > 200 {
		return text[:200] + "...", nil
	}
	return text, nil
}

// ResolveEntity always returns false; rule-based extraction relies on
// the existing fuzzy resolver for entity deduplication.
func (r *RuleProvider) ResolveEntity(_ context.Context, _, _ ExtractedEntity) (bool, error) {
	return false, nil
}

// HasMatch returns true if any pattern matcher matches the text.
func (r *RuleProvider) HasMatch(text string) bool {
	for _, m := range r.matchers {
		if m.Match(text) {
			return true
		}
	}
	return false
}

// --- Pattern matchers ---

var (
	reBashError  = regexp.MustCompile(`(?i)(?:command not found|permission denied|no such file|exit code \d+|error:|fatal:|ERRO(?:R)?\[|panic:|segmentation fault|killed|cannot|failed to)`)
	reGitCommit  = regexp.MustCompile(`(?m)^commit [0-9a-f]{7,40}`)
	reGitDiff    = regexp.MustCompile(`(?m)^diff --git a/(.+?) b/(.+)`)
	reGitAuthor  = regexp.MustCompile(`(?m)^Author:\s+(.+?)\s*<`)
	reGitMsg     = regexp.MustCompile(`(?m)^    (.+)`)
	reConfigFile = regexp.MustCompile(`(?i)(?:\.json|\.ya?ml|\.toml|\.env|\.ini|\.conf|\.cfg)\b`)
	reConfigVerb = regexp.MustCompile(`(?i)\b(?:set|changed|updated|configured|modified|added|removed|enabled|disabled)\b`)
	reDep        = regexp.MustCompile(`(?i)(?:go\.mod|go\.sum|package\.json|requirements\.txt|Pipfile|Cargo\.toml|Gemfile|pom\.xml|build\.gradle)\b`)
	reDepAction  = regexp.MustCompile(`(?i)\b(?:go get|go install|npm install|pip install|cargo add|gem install|yarn add|pnpm add)\s+(\S+)`)
	reDecision      = regexp.MustCompile(`(?i)\b(?:decided to|chose|went with|switched (?:from|to)|picked|selected|opted for)\b`)
	rePreference    = regexp.MustCompile(`(?i)\b(?:(?:I |we |always |never |prefer|don't use|avoid|stop using)\b)`)
	reSentenceSplit = regexp.MustCompile(`[.!?]+\s+`)
)

func bashErrorMatcher() PatternMatcher {
	return PatternMatcher{
		Name:  "bash_error",
		Match: func(text string) bool { return reBashError.MatchString(text) },
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			// Extract the command/tool that failed
			lines := strings.Split(text, "\n")
			var errorLines []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if reBashError.MatchString(line) {
					errorLines = append(errorLines, line)
				}
			}

			if len(errorLines) == 0 {
				return nil
			}

			// Extract first error as the primary error entity
			firstError := errorLines[0]
			if len(firstError) > 200 {
				firstError = firstError[:200]
			}

			result.Entities = append(result.Entities, ExtractedEntity{
				Name:    extractErrorName(firstError),
				Type:    "event",
				Summary: firstError,
			})

			// Try to extract the tool/command name
			if cmd := extractCommandName(text); cmd != "" {
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    cmd,
					Type:    "tool",
					Summary: "command that produced an error",
				})
				result.Relations = append(result.Relations, ExtractedRelation{
					Source:  cmd,
					Target:  extractErrorName(firstError),
					Type:    "failed_with",
					Summary: firstError,
					Weight:  0.8,
				})
			}

			return result
		},
	}
}

func gitCommitMatcher() PatternMatcher {
	return PatternMatcher{
		Name:  "git_commit",
		Match: func(text string) bool { return reGitCommit.MatchString(text) || reGitDiff.MatchString(text) },
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			// Extract modified files from diff
			files := reGitDiff.FindAllStringSubmatch(text, -1)
			fileSet := make(map[string]bool)
			for _, m := range files {
				if len(m) > 2 {
					fileSet[m[2]] = true
				}
			}

			// Extract author
			if m := reGitAuthor.FindStringSubmatch(text); len(m) > 1 {
				author := strings.TrimSpace(m[1])
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    author,
					Type:    "person",
					Summary: "git commit author",
				})
			}

			// Extract commit messages
			var commitMsg string
			for _, m := range reGitMsg.FindAllStringSubmatch(text, 5) {
				if len(m) > 1 {
					msg := strings.TrimSpace(m[1])
					if msg != "" && !strings.HasPrefix(msg, "Merge") {
						commitMsg = msg
						break
					}
				}
			}

			if commitMsg != "" {
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    truncateStr(commitMsg, 80),
					Type:    "event",
					Summary: "git commit: " + commitMsg,
				})
			}

			// Add file entities and relations
			for file := range fileSet {
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    file,
					Type:    "concept",
					Summary: "source file modified in commit",
				})
				if commitMsg != "" {
					result.Relations = append(result.Relations, ExtractedRelation{
						Source:  truncateStr(commitMsg, 80),
						Target:  file,
						Type:    "modified",
						Summary: "commit modified this file",
						Weight:  0.7,
					})
				}
			}

			return result
		},
	}
}

func configChangeMatcher() PatternMatcher {
	return PatternMatcher{
		Name: "config_change",
		Match: func(text string) bool {
			return reConfigFile.MatchString(text) && reConfigVerb.MatchString(text)
		},
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			// Extract config file names
			files := reConfigFile.FindAllString(text, 5)
			fileSet := make(map[string]bool)
			for _, f := range files {
				fileSet[f] = true
			}

			// Find what was configured
			verbs := reConfigVerb.FindAllString(text, 3)
			action := "configured"
			if len(verbs) > 0 {
				action = strings.ToLower(verbs[0])
			}

			for file := range fileSet {
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    file,
					Type:    "concept",
					Summary: "configuration file that was " + action,
				})
			}

			summary := truncateStr(strings.TrimSpace(text), 200)
			if len(fileSet) > 0 {
				result.Entities = append(result.Entities, ExtractedEntity{
					Name:    "config change",
					Type:    "event",
					Summary: summary,
				})
			}

			return result
		},
	}
}

func dependencyMatcher() PatternMatcher {
	return PatternMatcher{
		Name: "dependency",
		Match: func(text string) bool {
			return reDep.MatchString(text) || reDepAction.MatchString(text)
		},
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			// Extract explicit install commands
			installs := reDepAction.FindAllStringSubmatch(text, 10)
			for _, m := range installs {
				if len(m) > 1 {
					pkg := strings.TrimSpace(m[1])
					if pkg != "" {
						result.Entities = append(result.Entities, ExtractedEntity{
							Name:    pkg,
							Type:    "tool",
							Summary: "dependency package",
						})
						result.Relations = append(result.Relations, ExtractedRelation{
							Source:  "project",
							Target:  pkg,
							Type:    "depends_on",
							Summary: "project depends on this package",
							Weight:  0.7,
						})
					}
				}
			}

			return result
		},
	}
}

func decisionMatcher() PatternMatcher {
	return PatternMatcher{
		Name:  "decision",
		Match: func(text string) bool { return reDecision.MatchString(text) },
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			// Find the sentence containing the decision
			sentences := splitSentences(text)
			for _, s := range sentences {
				if reDecision.MatchString(s) {
					s = strings.TrimSpace(s)
					if len(s) > 200 {
						s = s[:200]
					}
					result.Entities = append(result.Entities, ExtractedEntity{
						Name:    truncateStr(s, 80),
						Type:    "concept",
						Summary: "decision: " + s,
					})
					break // one decision per text
				}
			}

			return result
		},
	}
}

func preferenceMatcher() PatternMatcher {
	return PatternMatcher{
		Name:  "preference",
		Match: func(text string) bool { return rePreference.MatchString(text) },
		Extract: func(text string) *ExtractionResult {
			result := &ExtractionResult{}

			sentences := splitSentences(text)
			for _, s := range sentences {
				if rePreference.MatchString(s) {
					s = strings.TrimSpace(s)
					if len(s) > 200 {
						s = s[:200]
					}
					result.Entities = append(result.Entities, ExtractedEntity{
						Name:    truncateStr(s, 80),
						Type:    "concept",
						Summary: "preference: " + s,
					})
					break
				}
			}

			return result
		},
	}
}

// --- Helpers ---

func extractErrorName(errorLine string) string {
	// Try to create a short, meaningful name from the error
	errorLine = strings.TrimSpace(errorLine)

	// Common patterns
	for _, prefix := range []string{"error:", "fatal:", "Error:", "Fatal:", "ERROR:", "FATAL:"} {
		if _, after, ok := strings.Cut(errorLine, prefix); ok {
			rest := strings.TrimSpace(after)
			if rest != "" {
				return truncateStr(rest, 80)
			}
		}
	}

	return truncateStr(errorLine, 80)
}

func extractCommandName(text string) string {
	// Try to find a command name, e.g. from "$ cmd ..." or first line before error
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			parts := strings.Fields(line[2:])
			if len(parts) > 0 {
				return parts[0]
			}
		}
		if strings.HasPrefix(line, "> ") {
			parts := strings.Fields(line[2:])
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	return ""
}

func splitSentences(text string) []string {
	// Simple sentence splitter on period, exclamation, or newline boundaries
	var sentences []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Split on sentence-ending punctuation
		for _, s := range reSentenceSplit.Split(line, -1) {
			s = strings.TrimSpace(s)
			if s != "" {
				sentences = append(sentences, s)
			}
		}
	}
	return sentences
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
