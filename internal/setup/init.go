package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Editor describes an AI editor/tool that supports MCP configuration.
type Editor struct {
	Name       string
	ConfigPath func() string
	Detect     func() bool
	MCPKey     string // JSON key for MCP servers section
}

// InitResult tracks what happened for each editor.
type InitResult struct {
	Editor     string
	Status     string // "configured", "skipped", "not_installed", "error"
	ConfigPath string
	Error      string
}

// ErrAlreadyConfigured is returned when memex is already configured for an editor.
var ErrAlreadyConfigured = errors.New("already configured")

// SupportedEditors returns all editors that can be auto-configured.
func SupportedEditors() []Editor {
	return []Editor{
		{
			Name:       "Claude Code",
			ConfigPath: claudeCodeConfigPath,
			Detect:     func() bool { return dirExists(claudeCodeConfigDir()) },
			MCPKey:     "mcpServers",
		},
		{
			Name:       "Cursor",
			ConfigPath: func() string { return filepath.Join(homeDir(), ".cursor", "mcp.json") },
			Detect:     func() bool { return dirExists(filepath.Join(homeDir(), ".cursor")) },
			MCPKey:     "mcpServers",
		},
		{
			Name:       "Windsurf",
			ConfigPath: func() string { return filepath.Join(homeDir(), ".codeium", "windsurf", "mcp_config.json") },
			Detect:     func() bool { return dirExists(filepath.Join(homeDir(), ".codeium", "windsurf")) },
			MCPKey:     "mcpServers",
		},
		{
			Name:       "VS Code",
			ConfigPath: vscodeSettingsPath,
			Detect:     func() bool { return dirExists(filepath.Dir(vscodeSettingsPath())) },
			MCPKey:     "mcpServers",
		},
		{
			Name:       "Zed",
			ConfigPath: func() string { return filepath.Join(homeDir(), ".zed", "settings.json") },
			Detect:     func() bool { return dirExists(filepath.Join(homeDir(), ".zed")) },
			MCPKey:     "mcpServers",
		},
	}
}

// RunInit configures MCP server entries for detected editors.
// If editorFilter is non-empty, only configure that specific editor.
func RunInit(memexBin string, dbPath string, editorFilter string, dryRun bool, force bool) []InitResult {
	editors := SupportedEditors()
	var results []InitResult

	if memexBin == "" {
		// Try to find memex binary
		if p, err := exec.LookPath("memex"); err == nil {
			memexBin = p
		} else {
			memexBin = "memex"
		}
	}

	for _, editor := range editors {
		if editorFilter != "" && !strings.EqualFold(editor.Name, editorFilter) {
			continue
		}

		if !editor.Detect() {
			results = append(results, InitResult{
				Editor: editor.Name,
				Status: "not_installed",
			})
			continue
		}

		configPath := editor.ConfigPath()
		if configPath == "" {
			results = append(results, InitResult{
				Editor: editor.Name,
				Status: "error",
				Error:  "could not determine config path",
			})
			continue
		}

		err := configureEditor(editor, configPath, memexBin, dbPath, dryRun, force)
		if err != nil {
			if errors.Is(err, ErrAlreadyConfigured) {
				results = append(results, InitResult{
					Editor:     editor.Name,
					Status:     "skipped",
					ConfigPath: configPath,
				})
			} else {
				results = append(results, InitResult{
					Editor:     editor.Name,
					Status:     "error",
					ConfigPath: configPath,
					Error:      err.Error(),
				})
			}
			continue
		}

		results = append(results, InitResult{
			Editor:     editor.Name,
			Status:     "configured",
			ConfigPath: configPath,
		})
	}

	return results
}

func configureEditor(editor Editor, configPath, memexBin, dbPath string, dryRun, force bool) error {
	// Read existing config or start fresh
	config := make(map[string]any)
	data, err := os.ReadFile(configPath)
	if err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse existing config: %w", err)
		}
	}

	// Check if memex is already configured
	mcpServers, ok := config[editor.MCPKey].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
	}

	if _, exists := mcpServers["memex"]; exists && !force {
		return ErrAlreadyConfigured
	}

	// Build memex MCP server entry
	args := []string{"mcp"}
	if dbPath != "" {
		args = append(args, "--db", dbPath)
	}

	memexEntry := map[string]any{
		"command": memexBin,
		"args":    args,
	}

	mcpServers["memex"] = memexEntry
	config[editor.MCPKey] = mcpServers

	if dryRun {
		fmt.Printf("[dry-run] Would write to %s:\n", configPath)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(config)
		return nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Write config
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// --- Path helpers ---

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func claudeCodeConfigDir() string {
	return filepath.Join(homeDir(), ".claude")
}

func claudeCodeConfigPath() string {
	// Prefer ~/.claude/settings.json, fall back to ~/.claude.json
	settingsPath := filepath.Join(claudeCodeConfigDir(), "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		return settingsPath
	}
	legacyPath := filepath.Join(homeDir(), ".claude.json")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	// Default to settings.json
	return settingsPath
}

func vscodeSettingsPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", "Code", "User", "settings.json")
	case "linux":
		return filepath.Join(homeDir(), ".config", "Code", "User", "settings.json")
	default: // windows
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return ""
		}
		return filepath.Join(appdata, "Code", "User", "settings.json")
	}
}
