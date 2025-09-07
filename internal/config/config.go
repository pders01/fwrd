package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Database DatabaseConfig `mapstructure:"database"`
	Feed     FeedConfig     `mapstructure:"feed"`
	UI       UIConfig       `mapstructure:"ui"`
	Media    MediaConfig    `mapstructure:"media"`
	Keys     KeyConfig      `mapstructure:"keys"`
}

type DatabaseConfig struct {
	Path        string        `mapstructure:"path"`
	Timeout     time.Duration `mapstructure:"timeout"`
	SearchIndex string        `mapstructure:"search_index"`
}

type FeedConfig struct {
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	RefreshInterval   time.Duration `mapstructure:"refresh_interval"`
	DefaultRetryAfter time.Duration `mapstructure:"default_retry_after"`
	UserAgent         string        `mapstructure:"user_agent"`
}

type UIConfig struct {
	Colors  UIColors      `mapstructure:"colors"`
	Article ArticleConfig `mapstructure:"article"`
}

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

type ArticleConfig struct {
	MaxDescriptionLength int `mapstructure:"max_description_length"`
	WordWrapMaxWidth     int `mapstructure:"word_wrap_max_width"`
	WordWrapMinWidth     int `mapstructure:"word_wrap_min_width"`
}

type MediaConfig struct {
	Darwin        MediaPlayers `mapstructure:"darwin"`
	Linux         MediaPlayers `mapstructure:"linux"`
	Windows       MediaPlayers `mapstructure:"windows"`
	DefaultOpener string       `mapstructure:"default_opener"`
}

type MediaPlayers struct {
	Video []string `mapstructure:"video"`
	Image []string `mapstructure:"image"`
	Audio []string `mapstructure:"audio"`
	PDF   []string `mapstructure:"pdf"`
}

type KeyConfig struct {
	Modifier string      `mapstructure:"modifier"`
	Bindings KeyBindings `mapstructure:"bindings"`
}

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

func defaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".fwrd.db")
	searchIndexPath := filepath.Join(homeDir, ".fwrd", "index.bleve")

	return &Config{
		Database: DatabaseConfig{
			Path:        dbPath,
			Timeout:     1 * time.Second,
			SearchIndex: searchIndexPath,
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

func Load(configPath string) (*Config, error) {
	v := viper.New()

	cfg := defaultConfig()
	v.SetDefault("database", cfg.Database)
	v.SetDefault("feed", cfg.Feed)
	v.SetDefault("ui", cfg.UI)
	v.SetDefault("media", cfg.Media)
	v.SetDefault("keys", cfg.Keys)

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		homeDir, _ := os.UserHomeDir()
		configDir := filepath.Join(homeDir, ".config", "fwrd")

		v.SetConfigName("config")
		v.SetConfigType("toml")
		v.AddConfigPath(configDir)
		v.AddConfigPath(".")
	}

	v.SetEnvPrefix("FWRD")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	// Expand paths after loading
	expandPaths(&config)

	return &config, nil
}

// expandPath expands ~ to home directory and converts to absolute path
func expandPath(path string) string {
	if path == "" {
		return path
	}

	// Expand tilde
	if len(path) >= 2 && path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}

	// Convert to absolute path if not already absolute
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}

	return path
}

// expandPaths expands all paths in the config
func expandPaths(cfg *Config) {
	cfg.Database.Path = expandPath(cfg.Database.Path)
	cfg.Database.SearchIndex = expandPath(cfg.Database.SearchIndex)
}

func Save(config *Config, path string) error {
	v := viper.New()

	// Convert durations to strings for TOML readability
	dbCfg := map[string]interface{}{
		"path":         config.Database.Path,
		"timeout":      config.Database.Timeout.String(),
		"search_index": config.Database.SearchIndex,
	}

	feedCfg := map[string]interface{}{
		"http_timeout":        config.Feed.HTTPTimeout.String(),
		"refresh_interval":    config.Feed.RefreshInterval.String(),
		"default_retry_after": config.Feed.DefaultRetryAfter.String(),
		"user_agent":          config.Feed.UserAgent,
	}

	v.Set("database", dbCfg)
	v.Set("feed", feedCfg)
	v.Set("ui", config.UI)
	v.Set("media", config.Media)
	v.Set("keys", config.Keys)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	return v.WriteConfigAs(path)
}

func GenerateDefaultConfig(path string) error {
	return Save(defaultConfig(), path)
}
