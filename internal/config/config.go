package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	// Database configuration
	Database DatabaseConfig `mapstructure:"database"`

	// Feed fetching configuration
	Feed FeedConfig `mapstructure:"feed"`

	// UI configuration
	UI UIConfig `mapstructure:"ui"`

	// Media players configuration
	Media MediaConfig `mapstructure:"media"`

	// Keybindings configuration
	Keys KeyConfig `mapstructure:"keys"`
}

// DatabaseConfig holds database-related settings
type DatabaseConfig struct {
	Path    string        `mapstructure:"path"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// FeedConfig holds feed fetching settings
type FeedConfig struct {
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	RefreshInterval   time.Duration `mapstructure:"refresh_interval"`
	DefaultRetryAfter time.Duration `mapstructure:"default_retry_after"`
	UserAgent         string        `mapstructure:"user_agent"`
}

// UIConfig holds UI-related settings
type UIConfig struct {
	// Colors can be hex values or named colors
	Colors UIColors `mapstructure:"colors"`

	// Article display settings
	Article ArticleConfig `mapstructure:"article"`
}

// UIColors holds color configuration
type UIColors struct {
	Primary    string `mapstructure:"primary"`
	Secondary  string `mapstructure:"secondary"`
	Accent     string `mapstructure:"accent"`
	Background string `mapstructure:"background"`
	Surface    string `mapstructure:"surface"`
	Text       string `mapstructure:"text"`
	Muted      string `mapstructure:"muted"`
	Error      string `mapstructure:"error"`
	Success    string `mapstructure:"success"`
}

// ArticleConfig holds article display settings
type ArticleConfig struct {
	MaxDescriptionLength int `mapstructure:"max_description_length"`
	WordWrapMaxWidth     int `mapstructure:"word_wrap_max_width"`
	WordWrapMinWidth     int `mapstructure:"word_wrap_min_width"`
}

// MediaConfig holds media player configuration
type MediaConfig struct {
	// Players for different platforms
	Darwin  MediaPlayers `mapstructure:"darwin"`
	Linux   MediaPlayers `mapstructure:"linux"`
	Windows MediaPlayers `mapstructure:"windows"`

	// Fallback opener for unrecognized types
	DefaultOpener string `mapstructure:"default_opener"`
}

// MediaPlayers holds media player commands
type MediaPlayers struct {
	Video []string `mapstructure:"video"`
	Image []string `mapstructure:"image"`
	Audio []string `mapstructure:"audio"`
	PDF   []string `mapstructure:"pdf"`
}

// KeyConfig holds keybinding configuration
type KeyConfig struct {
	// Modifier key for custom commands (ctrl, alt, cmd, super)
	Modifier string `mapstructure:"modifier"`

	// Custom keybindings
	Bindings KeyBindings `mapstructure:"bindings"`
}

// KeyBindings holds specific key combinations
type KeyBindings struct {
	Quit       string `mapstructure:"quit"`
	Search     string `mapstructure:"search"`
	NewFeed    string `mapstructure:"new_feed"`
	DeleteFeed string `mapstructure:"delete_feed"`
	Refresh    string `mapstructure:"refresh"`
	ToggleRead string `mapstructure:"toggle_read"`
	OpenMedia  string `mapstructure:"open_media"`
	Back       string `mapstructure:"back"`
	Help       string `mapstructure:"help"`
}

// defaultConfig returns the default configuration
func defaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".fwrd.db")

	return &Config{
		Database: DatabaseConfig{
			Path:    dbPath,
			Timeout: 1 * time.Second,
		},
		Feed: FeedConfig{
			HTTPTimeout:       30 * time.Second,
			RefreshInterval:   5 * time.Minute,
			DefaultRetryAfter: 15 * time.Minute,
			UserAgent:         "fwrd/1.0 (https://github.com/pders01/fwrd)",
		},
		UI: UIConfig{
			Colors: UIColors{
				Primary:    "#FF6B6B",
				Secondary:  "#4ECDC4",
				Accent:     "#95E1D3",
				Background: "#1A1A2E",
				Surface:    "#16213E",
				Text:       "#EAEAEA",
				Muted:      "#94A3B8",
				Error:      "#F87171",
				Success:    "#4ADE80",
			},
			Article: ArticleConfig{
				MaxDescriptionLength: 150,
				WordWrapMaxWidth:     120,
				WordWrapMinWidth:     40,
			},
		},
		Media: MediaConfig{
			Darwin: MediaPlayers{
				Video: []string{"iina", "mpv", "vlc"},
				Image: []string{"preview", "open"},
				Audio: []string{"mpv", "vlc", "open"},
				PDF:   []string{"preview", "open"},
			},
			Linux: MediaPlayers{
				Video: []string{"mpv", "vlc", "mplayer"},
				Image: []string{"sxiv", "feh", "eog", "xdg-open"},
				Audio: []string{"mpv", "vlc", "mplayer"},
				PDF:   []string{"zathura", "evince", "xdg-open"},
			},
			Windows: MediaPlayers{
				Video: []string{"mpv", "vlc"},
				Image: []string{"start"},
				Audio: []string{"mpv", "vlc"},
				PDF:   []string{"start"},
			},
			DefaultOpener: getDefaultOpener(),
		},
		Keys: KeyConfig{
			Modifier: "ctrl",
			Bindings: KeyBindings{
				Quit:       "q",
				Search:     "s",
				NewFeed:    "n",
				DeleteFeed: "x",
				Refresh:    "r",
				ToggleRead: "m",
				OpenMedia:  "o",
				Back:       "esc",
				Help:       "?",
			},
		},
	}
}

func getDefaultOpener() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "linux":
		return "xdg-open"
	case "windows":
		return "start"
	default:
		return "open"
	}
}

// Load loads configuration from file and environment
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set default configuration
	cfg := defaultConfig()
	v.SetDefault("database", cfg.Database)
	v.SetDefault("feed", cfg.Feed)
	v.SetDefault("ui", cfg.UI)
	v.SetDefault("media", cfg.Media)
	v.SetDefault("keys", cfg.Keys)

	// Set config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		// Look for config in standard locations
		homeDir, _ := os.UserHomeDir()
		configDir := filepath.Join(homeDir, ".config", "fwrd")

		v.SetConfigName("config")
		v.SetConfigType("toml")
		v.AddConfigPath(configDir)
		v.AddConfigPath(".")
	}

	// Enable environment variables
	v.SetEnvPrefix("FWRD")
	v.AutomaticEnv()

	// Read config file if it exists
	if err := v.ReadInConfig(); err != nil {
		// It's okay if config file doesn't exist
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	// Unmarshal into config struct
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &config, nil
}

// Save saves the current configuration to file
func Save(config *Config, path string) error {
	v := viper.New()

	// Convert durations to strings for readable TOML
	dbCfg := map[string]interface{}{
		"path":    config.Database.Path,
		"timeout": config.Database.Timeout.String(),
	}

	feedCfg := map[string]interface{}{
		"http_timeout":        config.Feed.HTTPTimeout.String(),
		"refresh_interval":    config.Feed.RefreshInterval.String(),
		"default_retry_after": config.Feed.DefaultRetryAfter.String(),
		"user_agent":          config.Feed.UserAgent,
	}

	// Set the configuration values
	v.Set("database", dbCfg)
	v.Set("feed", feedCfg)
	v.Set("ui", config.UI)
	v.Set("media", config.Media)
	v.Set("keys", config.Keys)

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Write config file
	return v.WriteConfigAs(path)
}

// GenerateDefaultConfig generates a default config file
func GenerateDefaultConfig(path string) error {
	return Save(defaultConfig(), path)
}
