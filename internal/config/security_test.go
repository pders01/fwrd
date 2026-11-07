package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func configFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnvironmentOverridesNestedConfiguration(t *testing.T) {
	path := configFixture(t, "[web.auth]\nusername='file-user'\npassword='file-password'\n[feed]\nhttptimeout='1s'\n")
	db := filepath.Join(t.TempDir(), "private.db")
	t.Setenv("FWRD_DATABASE_PATH", db)
	t.Setenv("FWRD_WEB_AUTH_USERNAME", "env-user")
	t.Setenv("FWRD_WEB_AUTH_PASSWORD", "env-password")
	t.Setenv("FWRD_FEED_HTTP_TIMEOUT", "7s")
	t.Setenv("FWRD_WEB_TLS_ENABLED", "false")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != db || cfg.Database.SearchIndex != DerivedIndexPath(db) {
		t.Fatalf("database environment override = %+v", cfg.Database)
	}
	if cfg.Web.Auth.Username != "env-user" || cfg.Web.Auth.Password != "env-password" {
		t.Fatal("nested auth environment overrides were ignored")
	}
	if cfg.Feed.HTTPTimeout != 7*time.Second {
		t.Fatalf("alias overrode environment timeout: %v", cfg.Feed.HTTPTimeout)
	}
	if cfg.Web.TLS.Enabled == nil || *cfg.Web.TLS.Enabled {
		t.Fatal("boolean environment override was ignored")
	}
}

func TestConfigurationRejectsIncompleteAuth(t *testing.T) {
	for _, body := range []string{"[web.auth]\nusername='user'\n", "[web.auth]\npassword='secret'\n"} {
		if _, err := Load(configFixture(t, body)); err == nil {
			t.Fatal("partial credentials accepted")
		}
	}
	path := configFixture(t, "[web.auth]\nusername='user'\npassword='secret'\n")
	t.Setenv("FWRD_WEB_AUTH_USERNAME", "")
	if _, err := Load(path); err == nil {
		t.Fatal("empty username override silently disabled configured authentication")
	}
}

func TestConfigurationResolvesFileRelativePathsAndCustomIndex(t *testing.T) {
	path := configFixture(t, "[database]\npath='data/news.db'\n[web.tls]\ncert_file='cert.pem'\nkey_file='key.pem'\n[web.audit]\npath='audit.jsonl'\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(path)
	if cfg.Database.Path != filepath.Join(base, "data", "news.db") || cfg.Database.SearchIndex != filepath.Join(base, "data", "news.bleve") {
		t.Fatalf("database paths = %+v", cfg.Database)
	}
	if cfg.Web.TLS.CertFile != filepath.Join(base, "cert.pem") || cfg.Web.TLS.KeyFile != filepath.Join(base, "key.pem") || cfg.Web.Audit.Path != filepath.Join(base, "audit.jsonl") {
		t.Fatal("service paths remained relative")
	}
}

func TestConfigurationPreservesExplicitIndexAndMemorySentinel(t *testing.T) {
	path := configFixture(t, "[database]\npath='custom.db'\nsearch_index='selected.bleve'\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.SearchIndex != filepath.Join(filepath.Dir(path), "selected.bleve") {
		t.Fatal("explicit index was replaced")
	}
	cfg, err = Load(configFixture(t, "[database]\npath=':memory:'\n"))
	if err != nil || cfg.Database.Path != ":memory:" || cfg.Database.SearchIndex != "" {
		t.Fatalf("memory config = %+v, %v", cfg, err)
	}
}

func TestConfigSaveIsPrivateAndPreservesConcurrency(t *testing.T) {
	cfg := defaultConfig()
	cfg.Web.Auth = WebAuthConfig{Username: "user", Password: "secret"}
	cfg.Feed.MaxConcurrentRefreshes = 9
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(cfg, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil || loaded.Feed.MaxConcurrentRefreshes != 9 || loaded.Web.Auth.Password != "secret" {
		t.Fatal("configuration did not round trip")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if generateErr := GenerateDefaultConfig(path); generateErr == nil {
		t.Fatal("generation overwrote existing credentials")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing config changed")
	}
}

func TestConfigurationRejectsUnboundedHTTPTimeout(t *testing.T) {
	_, err := Load(configFixture(t, "[feed]\nhttp_timeout='0s'\n"))
	if err == nil || !strings.Contains(err.Error(), "http_timeout") {
		t.Fatalf("zero timeout error = %v", err)
	}
}
