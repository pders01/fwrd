package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGetDefaultOpener(t *testing.T) {
	expected := map[string]string{
		"darwin":  "open",
		"linux":   "xdg-open",
		"windows": "start",
	}

	opener := getDefaultOpener()

	if expectedOpener, ok := expected[runtime.GOOS]; ok {
		if opener != expectedOpener {
			t.Errorf("getDefaultOpener() = %s, want %s for %s", opener, expectedOpener, runtime.GOOS)
		}
	} else {
		// For unknown OS, should default to "open"
		if opener != "open" {
			t.Errorf("getDefaultOpener() = %s, want 'open' for unknown OS", opener)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	// Test database defaults
	if cfg.Database.Timeout != 1*time.Second {
		t.Errorf("Database.Timeout = %v, want 1s", cfg.Database.Timeout)
	}

	// Test feed defaults
	if cfg.Feed.HTTPTimeout != 30*time.Second {
		t.Errorf("Feed.HTTPTimeout = %v, want 30s", cfg.Feed.HTTPTimeout)
	}
	if cfg.Feed.RefreshInterval != 5*time.Minute {
		t.Errorf("Feed.RefreshInterval = %v, want 5m", cfg.Feed.RefreshInterval)
	}
	if cfg.Feed.DefaultRetryAfter != 15*time.Minute {
		t.Errorf("Feed.DefaultRetryAfter = %v, want 15m", cfg.Feed.DefaultRetryAfter)
	}
	if cfg.Feed.UserAgent == "" {
		t.Error("Feed.UserAgent should not be empty")
	}

	// Test UI defaults
	if cfg.UI.Article.MaxDescriptionLength != 150 {
		t.Errorf("UI.Article.MaxDescriptionLength = %d, want 150", cfg.UI.Article.MaxDescriptionLength)
	}

	// Test media defaults
	if cfg.Media.DefaultOpener == "" {
		t.Error("Media.DefaultOpener should not be empty")
	}

	// Test key bindings
	if cfg.Keys.Modifier != "ctrl" {
		t.Errorf("Keys.Modifier = %s, want 'ctrl'", cfg.Keys.Modifier)
	}
	if cfg.Keys.Bindings.Quit != "q" {
		t.Errorf("Keys.Bindings.Quit = %s, want 'q'", cfg.Keys.Bindings.Quit)
	}
}

func TestLoad_DefaultConfig(t *testing.T) {
	// Test loading without a config file (should use defaults)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}

	// Should have default values
	if cfg.Feed.RefreshInterval != 5*time.Minute {
		t.Errorf("Feed.RefreshInterval = %v, want 5m", cfg.Feed.RefreshInterval)
	}
}

func TestLoad_FromFile(t *testing.T) {
	// Create a temporary config file
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "test-config.toml")
	configContent := `
[database]
path = "/tmp/test.db"
timeout = "10s"

[feed]
http_timeout = "60s"
refresh_interval = "1h"
default_retry_after = "30m"
user_agent = "test-agent"

[ui.colors]
primary = "#FF0000"
`

	if writeErr := os.WriteFile(configPath, []byte(configContent), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Check loaded values
	if cfg.Database.Path != "/tmp/test.db" {
		t.Errorf("Database.Path = %s, want '/tmp/test.db'", cfg.Database.Path)
	}
	if cfg.Database.Timeout != 10*time.Second {
		t.Errorf("Database.Timeout = %v, want 10s", cfg.Database.Timeout)
	}
	if cfg.Feed.HTTPTimeout != 60*time.Second {
		t.Errorf("Feed.HTTPTimeout = %v, want 60s", cfg.Feed.HTTPTimeout)
	}
	if cfg.Feed.RefreshInterval != 1*time.Hour {
		t.Errorf("Feed.RefreshInterval = %v, want 1h", cfg.Feed.RefreshInterval)
	}
	if cfg.Feed.UserAgent != "test-agent" {
		t.Errorf("Feed.UserAgent = %s, want 'test-agent'", cfg.Feed.UserAgent)
	}
	if cfg.UI.Colors.Primary != "#FF0000" {
		t.Errorf("UI.Colors.Primary = %s, want '#FF0000'", cfg.UI.Colors.Primary)
	}
}

func TestSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-save-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		Database: DatabaseConfig{
			Path:    "/test/path.db",
			Timeout: 10 * time.Second,
		},
		Feed: FeedConfig{
			HTTPTimeout:       45 * time.Second,
			RefreshInterval:   20 * time.Minute,
			DefaultRetryAfter: 10 * time.Minute,
			UserAgent:         "test-save-agent",
		},
		UI: UIConfig{
			Colors: UIColors{
				Primary: "#00FF00",
			},
		},
		Media: MediaConfig{
			DefaultOpener: "test-opener",
		},
		Keys: KeyConfig{
			Modifier: "alt",
			Bindings: KeyBindings{
				Quit: "x",
			},
		},
	}

	savePath := filepath.Join(tmpDir, "saved-config.toml")
	if saveErr := Save(cfg, savePath); saveErr != nil {
		t.Fatalf("Save() error = %v", saveErr)
	}

	// Verify file was created
	if _, statErr := os.Stat(savePath); os.IsNotExist(statErr) {
		t.Fatal("Save() did not create config file")
	}

	// Load it back and verify
	loaded, err := Load(savePath)
	if err != nil {
		t.Fatalf("Failed to load saved config: %v", err)
	}

	if loaded.Database.Path != cfg.Database.Path {
		t.Errorf("Loaded Database.Path = %s, want %s", loaded.Database.Path, cfg.Database.Path)
	}
	if loaded.Feed.UserAgent != cfg.Feed.UserAgent {
		t.Errorf("Loaded Feed.UserAgent = %s, want %s", loaded.Feed.UserAgent, cfg.Feed.UserAgent)
	}
	if loaded.Keys.Modifier != cfg.Keys.Modifier {
		t.Errorf("Loaded Keys.Modifier = %s, want %s", loaded.Keys.Modifier, cfg.Keys.Modifier)
	}
}

func TestGenerateDefaultConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-gen-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "generated.toml")
	if genErr := GenerateDefaultConfig(configPath); genErr != nil {
		t.Fatalf("GenerateDefaultConfig() error = %v", genErr)
	}

	// Verify file exists
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		t.Fatal("GenerateDefaultConfig() did not create file")
	}

	// Load and verify it has defaults
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load generated config: %v", err)
	}

	if cfg.Keys.Modifier != "ctrl" {
		t.Errorf("Generated config has Keys.Modifier = %s, want 'ctrl'", cfg.Keys.Modifier)
	}
}

func TestTestConfig(t *testing.T) {
	cfg := TestConfig()

	if cfg == nil {
		t.Fatal("TestConfig() returned nil")
	}

	// Verify test-specific settings
	if cfg.Database.Path != ":memory:" {
		t.Errorf("TestConfig Database.Path = %s, want ':memory:'", cfg.Database.Path)
	}
	if cfg.Feed.UserAgent != "fwrd-test/1.0" {
		t.Errorf("TestConfig Feed.UserAgent = %s, want 'fwrd-test/1.0'", cfg.Feed.UserAgent)
	}
}
