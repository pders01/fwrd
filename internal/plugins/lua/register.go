package lua

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pders01/fwrd/internal/plugins"
)

// DefaultPluginDir returns the path where fwrd looks for user-authored
// Lua plugins. The location follows XDG conventions
// ($XDG_CONFIG_HOME/fwrd/plugins, falling back to ~/.config/fwrd/plugins)
// because plugins are user-edited configuration, not application state.
//
// Returns "" when the home directory cannot be determined; callers
// should treat that as "no plugin directory configured" and skip
// loading.
func DefaultPluginDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "fwrd", "plugins")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "fwrd", "plugins")
}

// LoadAndRegister scans dir for *.lua plugins, registers each
// successfully-loaded plugin onto reg, and reports per-file load
// failures via b.Logger (if set). Returns the number of plugins
// registered.
//
// A missing directory is treated as zero plugins, not an error: this
// keeps fwrd usable on a fresh machine where the plugin dir has not yet
// been created.
func LoadAndRegister(reg *plugins.Registry, dir string, b Bindings) (int, error) {
	if reg == nil {
		return 0, fmt.Errorf("nil plugin registry")
	}
	if dir == "" {
		return 0, nil
	}

	results, err := LoadDir(dir, b)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, r := range results {
		if r.Err != nil {
			if b.Logger != nil {
				b.Logger.Warnf("skipping lua plugin %s: %v", r.Path, r.Err)
			}
			continue
		}
		reg.Register(r.Plugin)
		count++
		if b.Logger != nil {
			b.Logger.Infof("registered lua plugin %s (name=%s priority=%d)",
				r.Path, r.Plugin.Name(), r.Plugin.Priority())
		}
	}
	return count, nil
}
