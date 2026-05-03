package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pders01/fwrd/internal/validation"
	"github.com/spf13/viper"
)

// Defaults exposed as constants so other packages can reference them
// without re-declaring the literal. Keep these in sync with
// defaultConfig() — defaultConfig is the single source of truth at
// runtime, but these provide stable names for fallbacks elsewhere.
const (
	// DefaultSearchDebounceMs is the delay between the last keystroke
	// in the search input and firing a query against the index.
	DefaultSearchDebounceMs = 200
	// DefaultMaxConcurrentRefreshes is the worker count used by the
	// feed manager when no override is configured.
	DefaultMaxConcurrentRefreshes = 5
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
	// MaxConcurrentRefreshes caps the number of feeds refreshed in
	// parallel during RefreshAllFeeds. Set <= 0 to fall back to
	// DefaultMaxConcurrentRefreshes.
	MaxConcurrentRefreshes int `mapstructure:"max_concurrent_refreshes"`
}

type UIConfig struct {
	Article ArticleConfig `mapstructure:"article"`
	Icons   string        `mapstructure:"icons"`
	// Theme controls the glamour render style. Accepted values:
	//   "auto"  — detect from terminal/OS (default)
	//   "light" — force light style
	//   "dark"  — force dark style
	Theme string `mapstructure:"theme"`
	// SearchDebounceMs is the delay between the last keystroke in the
	// search input and firing a query against the index.
	SearchDebounceMs int `mapstructure:"search_debounce_ms"`
}

type ArticleConfig struct {
	MaxDescriptionLength int `mapstructure:"max_description_length"`
	WordWrapMaxWidth     int `mapstructure:"word_wrap_max_width"`
	WordWrapMinWidth     int `mapstructure:"word_wrap_min_width"`
	// ListLimit caps how many articles are loaded into the article list
	// per feed. Set <= 0 to fall back to DefaultArticleLimit.
	ListLimit int `mapstructure:"list_limit"`
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
	Quit        string `mapstructure:"quit"`
	Search      string `mapstructure:"search"`
	NewFeed     string `mapstructure:"new_feed"`
	RenameFeed  string `mapstructure:"rename_feed"`
	DeleteFeed  string `mapstructure:"delete_feed"`
	Refresh     string `mapstructure:"refresh"`
	ToggleRead  string `mapstructure:"toggle_read"`
	OpenMedia   string `mapstructure:"open_media"`
	ThemeToggle string `mapstructure:"theme_toggle"`
	Back        string `mapstructure:"back"`
}

func defaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".fwrd", "fwrd.db")
	searchIndexPath := filepath.Join(homeDir, ".fwrd", "index.bleve")

	return &Config{
		Database: DatabaseConfig{
			Path:        dbPath,
			Timeout:     1 * time.Second,
			SearchIndex: searchIndexPath,
		},
		Feed: FeedConfig{
			HTTPTimeout:            30 * time.Second,
			RefreshInterval:        5 * time.Minute,
			DefaultRetryAfter:      15 * time.Minute,
			UserAgent:              "fwrd/1.0 (https://github.com/pders01/fwrd)",
			MaxConcurrentRefreshes: DefaultMaxConcurrentRefreshes,
		},
		UI: UIConfig{
			Article: ArticleConfig{
				MaxDescriptionLength: 150,
				WordWrapMaxWidth:     120,
				WordWrapMinWidth:     40,
				ListLimit:            50,
			},
			Icons:            "nerd",
			Theme:            "auto",
			SearchDebounceMs: DefaultSearchDebounceMs,
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
				Quit:        "q",
				Search:      "s",
				NewFeed:     "n",
				RenameFeed:  "e",
				DeleteFeed:  "x",
				Refresh:     "r",
				ToggleRead:  "u",
				OpenMedia:   "o",
				ThemeToggle: "t",
				Back:        "esc",
			},
		},
	}
}

// normalizeOverrides promotes user-provided no-underscore variants
// (e.g. "openmedia") to their canonical snake_case path ("open_media") so
// they override the default at the same key. Without this the user's value
// and the default coexist as siblings and mapstructure may pick either.
func normalizeOverrides(v *viper.Viper, prefix string, defaults map[string]any) {
	for k, val := range defaults {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if sub, ok := val.(map[string]any); ok {
			normalizeOverrides(v, path, sub)
			continue
		}
		if !strings.Contains(k, "_") {
			continue
		}
		altPath := strings.ReplaceAll(k, "_", "")
		if prefix != "" {
			altPath = prefix + "." + altPath
		}
		if v.InConfig(altPath) {
			v.Set(path, v.Get(altPath))
		}
	}
}

// seedDefaults walks a snake_case-keyed map and registers each leaf with
// viper.SetDefault, so values land in viper's defaults layer rather than the
// config layer that ReadInConfig replaces.
func seedDefaults(v *viper.Viper, prefix string, m map[string]any) {
	for k, val := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if sub, ok := val.(map[string]any); ok {
			seedDefaults(v, path, sub)
			continue
		}
		v.SetDefault(path, val)
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
	defaultsMap := map[string]any{}
	if err := mapstructure.Decode(cfg, &defaultsMap); err != nil {
		return nil, fmt.Errorf("encoding defaults: %w", err)
	}
	seedDefaults(v, "", defaultsMap)

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

	normalizeOverrides(v, "", defaultsMap)

	var config Config
	matchNameOpt := func(dc *mapstructure.DecoderConfig) {
		dc.MatchName = func(mapKey, fieldName string) bool {
			return strings.EqualFold(strings.ReplaceAll(mapKey, "_", ""), strings.ReplaceAll(fieldName, "_", ""))
		}
	}
	if err := v.Unmarshal(&config, matchNameOpt); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	// Expand paths after loading
	expandPaths(&config)

	return &config, nil
}

// expandPath securely expands and validates a path
func expandPath(path string) string {
	if path == "" {
		return path
	}

	// Use secure path handler for validation
	pathHandler := validation.NewSecurePathHandler()

	// Attempt secure expansion and validation
	validatedPath, err := pathHandler.ExpandAndValidatePath(path)
	if err != nil {
		// Log error but return original path to maintain compatibility
		// In production, this might want to fail more gracefully
		return path
	}

	return validatedPath
}

// expandPaths expands all paths in the config
func expandPaths(cfg *Config) {
	cfg.Database.Path = expandPath(cfg.Database.Path)
	cfg.Database.SearchIndex = expandPath(cfg.Database.SearchIndex)
}

func Save(config *Config, path string) error {
	v := viper.New()

	// Convert durations to strings for TOML readability
	dbCfg := map[string]any{
		"path":         config.Database.Path,
		"timeout":      config.Database.Timeout.String(),
		"search_index": config.Database.SearchIndex,
	}

	feedCfg := map[string]any{
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
