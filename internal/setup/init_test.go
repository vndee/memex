package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureEditor_NewConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp.json")

	editor := Editor{
		Name:   "TestEditor",
		MCPKey: "mcpServers",
	}

	err := configureEditor(editor, configPath, "/usr/local/bin/memex", "", false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	servers, ok := config["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers not found")
	}

	memex, ok := servers["memex"].(map[string]any)
	if !ok {
		t.Fatal("memex entry not found")
	}

	if memex["command"] != "/usr/local/bin/memex" {
		t.Errorf("expected command '/usr/local/bin/memex', got %v", memex["command"])
	}
}

func TestConfigureEditor_PreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "settings.json")

	// Write existing config
	existing := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"other-tool": map[string]any{
				"command": "other",
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	editor := Editor{
		Name:   "TestEditor",
		MCPKey: "mcpServers",
	}

	err := configureEditor(editor, configPath, "memex", "", false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ = os.ReadFile(configPath)
	var config map[string]any
	json.Unmarshal(data, &config)

	// Check existing settings preserved
	if config["theme"] != "dark" {
		t.Error("existing setting 'theme' was lost")
	}

	servers := config["mcpServers"].(map[string]any)
	if _, ok := servers["other-tool"]; !ok {
		t.Error("existing MCP server 'other-tool' was lost")
	}
	if _, ok := servers["memex"]; !ok {
		t.Error("memex entry was not added")
	}
}

func TestConfigureEditor_NoClobber(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp.json")

	// Write config with existing memex entry
	existing := map[string]any{
		"mcpServers": map[string]any{
			"memex": map[string]any{
				"command": "old-memex",
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	editor := Editor{
		Name:   "TestEditor",
		MCPKey: "mcpServers",
	}

	err := configureEditor(editor, configPath, "new-memex", "", false, false)
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("expected ErrAlreadyConfigured, got: %v", err)
	}
}

func TestConfigureEditor_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp.json")

	// Write config with existing memex entry
	existing := map[string]any{
		"mcpServers": map[string]any{
			"memex": map[string]any{
				"command": "old-memex",
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	editor := Editor{
		Name:   "TestEditor",
		MCPKey: "mcpServers",
	}

	err := configureEditor(editor, configPath, "new-memex", "", false, true)
	if err != nil {
		t.Fatalf("unexpected error with force: %v", err)
	}

	data, _ = os.ReadFile(configPath)
	var config map[string]any
	json.Unmarshal(data, &config)

	servers := config["mcpServers"].(map[string]any)
	memex := servers["memex"].(map[string]any)
	if memex["command"] != "new-memex" {
		t.Errorf("force overwrite failed, command = %v", memex["command"])
	}
}

func TestConfigureEditor_WithDBPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp.json")

	editor := Editor{
		Name:   "TestEditor",
		MCPKey: "mcpServers",
	}

	err := configureEditor(editor, configPath, "memex", "/custom/db.sqlite", false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(configPath)
	var config map[string]any
	json.Unmarshal(data, &config)

	servers := config["mcpServers"].(map[string]any)
	memex := servers["memex"].(map[string]any)
	args := memex["args"].([]any)

	if len(args) != 3 || args[0] != "mcp" || args[1] != "--db" || args[2] != "/custom/db.sqlite" {
		t.Errorf("expected args [mcp --db /custom/db.sqlite], got %v", args)
	}
}
