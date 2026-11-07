package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
)

func TestExampleConfigUsesSupportedSettingsAndCurrentDefaults(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.toml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]any
	if parseErr := toml.Unmarshal(content, &example); parseErr != nil {
		t.Fatal(parseErr)
	}
	defaults := defaultConfig()
	var supported map[string]any
	if decodeErr := mapstructure.Decode(defaults, &supported); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var check func(map[string]any, map[string]any, string)
	check = func(actual, schema map[string]any, prefix string) {
		t.Helper()
		for key, value := range actual {
			expected, ok := schema[key]
			if !ok {
				t.Errorf("unsupported example setting: %s%s", prefix, key)
				continue
			}
			if table, nested := value.(map[string]any); nested {
				fields, valid := expected.(map[string]any)
				if !valid {
					t.Errorf("example uses a table for scalar setting: %s%s", prefix, key)
					continue
				}
				check(table, fields, prefix+key+".")
			}
		}
	}
	check(example, supported, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if warnings := Warnings(cfg); len(warnings) != 0 {
		t.Fatalf("example has unsafe/conflicting bindings: %v", warnings)
	}
	if !reflect.DeepEqual(cfg.Keys, defaults.Keys) || !reflect.DeepEqual(cfg.UI, defaults.UI) {
		t.Fatal("example UI/key settings drifted from current defaults")
	}
	if !reflect.DeepEqual(cfg.Media, defaults.Media) {
		t.Fatal("example overrides platform-specific media defaults")
	}
	if cfg.Web.AllowUnauthenticated || cfg.Feed.AllowPrivateNetworks || cfg.Web.Auth.Username != "" || cfg.Web.Auth.Password != "" {
		t.Fatal("example enables a security exception or embeds credentials")
	}
}
