package config

import (
	"bytes"
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
	// SourcePath is the absolute loaded config file, not a persisted setting.
	SourcePath string         `mapstructure:"-"`
	Database   DatabaseConfig `mapstructure:"database"`
	Feed       FeedConfig     `mapstructure:"feed"`
	UI         UIConfig       `mapstructure:"ui"`
	Media      MediaConfig    `mapstructure:"media"`
	Keys       KeyConfig      `mapstructure:"keys"`
	Web        WebConfig      `mapstructure:"web"`
}

type WebConfig struct {
	// ExternalOrigin explicitly trusts this public HTTP(S) origin when a reverse
	// proxy terminates TLS. Empty uses the native request transport. The proxy
	// must preserve Host; Forwarded/X-Forwarded-* never establish trust.
	ExternalOrigin string `mapstructure:"external_origin"`
	// Font selects the reading font for the web view. Accepts a preset
	// that maps to the OS's own system font stack — "serif" (default,
	// e.g. New York / Georgia), "sans", or "mono" — or any raw CSS
	// font-family list to use verbatim. No web fonts are bundled or
	// fetched; rendering always uses the system font library.
	Font string `mapstructure:"font"`
	// Auth optionally protects the web view with HTTP Basic Auth. It is
	// off by default (empty username), which suits the localhost-only
	// default bind. Set it before exposing the server beyond loopback.
	Auth WebAuthConfig `mapstructure:"auth"`
	// AllowUnauthenticated explicitly opts into exposing a non-loopback web
	// listener without credentials. It is false by default.
	AllowUnauthenticated bool `mapstructure:"allow_unauthenticated"`
	// TLS controls HTTPS for the web view. HTTPS is on by default with an
	// auto-generated self-signed certificate; see WebTLSConfig.
	TLS WebTLSConfig `mapstructure:"tls"`
	// Audit optionally records every inbound and outbound HTTP request to a
	// JSON-lines log. Off by default; see WebAuditConfig.
	Audit WebAuditConfig `mapstructure:"audit"`
}

// WebAuditConfig configures the request audit log. The `serve --audit` flag
// overrides Enabled at runtime.
type WebAuditConfig struct {
	// Enabled turns the audit log on. Off by default — it records every
	// browse and every feed fetch, which has privacy and disk-growth cost.
	Enabled bool `mapstructure:"enabled"`
	// Path is the audit log file (default ~/.fwrd/audit.log). Append-only,
	// one JSON object per line.
	Path string `mapstructure:"path"`
}

// WebTLSConfig configures HTTPS for the web view. The `serve` flags
// (--tls/--tls-mode/--tls-cert/--tls-key) override these at runtime.
type WebTLSConfig struct {
	// Enabled toggles HTTPS. A nil pointer means "unset" and defaults to
	// true (HTTPS-by-default); set it to false to serve plain HTTP.
	Enabled *bool `mapstructure:"enabled"`
	// Mode selects the certificate source: "self-signed" (default),
	// "local-ca", or "file" (which requires CertFile/KeyFile).
	Mode string `mapstructure:"mode"`
	// CertFile and KeyFile supply a bring-your-own certificate; setting them
	// selects the file source regardless of Mode.
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	// Dir is where generated certificates are persisted (default ~/.fwrd/tls).
	Dir string `mapstructure:"dir"`
}

// WebAuthConfig holds optional HTTP Basic Auth credentials for the web
// view. When Username is empty, auth is disabled and every request is
// served. When set, every request must present matching credentials.
type WebAuthConfig struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
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
	// AllowPrivateNetworks explicitly permits intranet/loopback feed and plugin
	// requests. Keep disabled for untrusted subscriptions or exposed services.
	AllowPrivateNetworks bool `mapstructure:"allow_private_networks"`
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
	ToggleStar  string `mapstructure:"toggle_star"`
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
				ToggleStar:  "f",
				OpenMedia:   "o",
				ThemeToggle: "t",
				Back:        "esc",
			},
		},
		Web: WebConfig{
			Font: "serif",
			TLS: WebTLSConfig{
				// "self-signed" + ~/.fwrd/tls mirror webtls' own defaults; kept
				// as literals here so config does not import the web subtree.
				Mode: "self-signed",
				Dir:  filepath.Join(homeDir, ".fwrd", "tls"),
			},
			Audit: WebAuditConfig{
				// Disabled by default; only the path is seeded so enabling it
				// via flag or `[web.audit] enabled` needs no path.
				Path: filepath.Join(homeDir, ".fwrd", "audit.log"),
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
			value := v.Get(altPath)
			if env, exists := os.LookupEnv("FWRD_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))); exists {
				value = env
			}
			// Both spellings must decode to the same value, regardless of map
			// iteration order; environment overrides still take precedence.
			v.Set(path, value)
			v.Set(altPath, value)
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
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AllowEmptyEnv(true)
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

	// Resolve file-relative paths once, before a service changes working dir.
	if used := v.ConfigFileUsed(); used != "" {
		base, err := filepath.Abs(filepath.Dir(used))
		if err != nil {
			return nil, fmt.Errorf("resolve config directory: %w", err)
		}
		config.SourcePath = filepath.Join(base, filepath.Base(used))
		resolveRelativePaths(&config, base)
	}
	expandPaths(&config)
	_, indexFromEnv := os.LookupEnv("FWRD_DATABASE_SEARCH_INDEX")
	if !indexFromEnv && !v.InConfig("database.search_index") && !v.InConfig("database.searchindex") {
		if config.Database.Path != cfg.Database.Path {
			config.Database.SearchIndex = DerivedIndexPath(config.Database.Path)
		}
	}
	if config.Database.SearchIndex == "" && config.Database.Path != ":memory:" {
		config.Database.SearchIndex = DerivedIndexPath(config.Database.Path)
	}
	var originErr error
	config.Web.ExternalOrigin, originErr = NormalizeWebExternalOrigin(config.Web.ExternalOrigin)
	if originErr != nil {
		return nil, originErr
	}
	if (config.Web.Auth.Username == "") != (config.Web.Auth.Password == "") {
		return nil, fmt.Errorf("web.auth requires both a non-empty username and password")
	}
	if config.Feed.HTTPTimeout <= 0 {
		return nil, fmt.Errorf("feed.http_timeout must be positive")
	}
	if config.Database.Timeout < 0 || config.Feed.RefreshInterval < 0 || config.Feed.DefaultRetryAfter < 0 {
		return nil, fmt.Errorf("database and feed durations must not be negative")
	}
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

// DerivedIndexPath keeps custom databases independent. Memory stores leave
// index ownership to their caller, which can allocate a unique temporary index.
func DerivedIndexPath(dbPath string) string {
	if dbPath == ":memory:" || dbPath == "" {
		return ""
	}
	return strings.TrimSuffix(dbPath, filepath.Ext(dbPath)) + ".bleve"
}

func pathFields(cfg *Config) []*string {
	return []*string{&cfg.Database.Path, &cfg.Database.SearchIndex,
		&cfg.Web.TLS.Dir, &cfg.Web.TLS.CertFile, &cfg.Web.TLS.KeyFile, &cfg.Web.Audit.Path}
}

func resolveRelativePaths(cfg *Config, base string) {
	for _, path := range pathFields(cfg) {
		if *path != "" && *path != ":memory:" && !filepath.IsAbs(*path) && !strings.HasPrefix(*path, "~") {
			*path = filepath.Join(base, *path)
		}
	}
}

// expandPaths expands all filesystem paths, not just the database paths.
func expandPaths(cfg *Config) {
	for _, path := range pathFields(cfg) {
		if *path != ":memory:" {
			*path = expandPath(*path)
		}
	}
}

func Save(config *Config, path string) error {
	return save(config, path, true)
}

func save(config *Config, path string, overwrite bool) error {
	v := viper.New()
	v.SetConfigType("toml")

	// Convert durations to strings for TOML readability
	dbCfg := map[string]any{
		"path":         config.Database.Path,
		"timeout":      config.Database.Timeout.String(),
		"search_index": config.Database.SearchIndex,
	}

	feedCfg := map[string]any{
		"http_timeout":             config.Feed.HTTPTimeout.String(),
		"refresh_interval":         config.Feed.RefreshInterval.String(),
		"default_retry_after":      config.Feed.DefaultRetryAfter.String(),
		"user_agent":               config.Feed.UserAgent,
		"max_concurrent_refreshes": config.Feed.MaxConcurrentRefreshes,
		"allow_private_networks":   config.Feed.AllowPrivateNetworks,
	}

	v.Set("database", dbCfg)
	v.Set("feed", feedCfg)
	v.Set("ui", config.UI)
	v.Set("media", config.Media)
	v.Set("keys", config.Keys)
	v.Set("web", config.Web)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	var encoded bytes.Buffer
	if err := v.WriteConfigTo(&encoded); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	var file *os.File
	var err error
	if overwrite {
		file, err = os.CreateTemp(dir, ".fwrd-config-*")
	} else {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return fmt.Errorf("create private config: %w", err)
	}
	name := file.Name()
	complete := false
	defer func() {
		_ = file.Close()
		if !complete || overwrite {
			_ = os.Remove(name)
		}
	}()
	if _, err = file.Write(encoded.Bytes()); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("sync config: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if overwrite {
		if err = os.Rename(name, path); err != nil {
			return fmt.Errorf("replace config: %w", err)
		}
	}
	complete = true
	return nil
}

// GenerateDefaultConfig never overwrites an existing configuration or symlink.
func GenerateDefaultConfig(path string) error {
	return save(defaultConfig(), path, false)
}
