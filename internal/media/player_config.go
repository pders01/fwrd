package media

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"
)

//go:embed players.toml
var playersTOML []byte

// PlayerDefinition defines how a media player should be invoked
type PlayerDefinition struct {
	Description string           `toml:"description"`
	Platforms   []string         `toml:"platforms"`
	Video       *MediaTypeConfig `toml:"video,omitempty"`
	Audio       *MediaTypeConfig `toml:"audio,omitempty"`
	Image       *MediaTypeConfig `toml:"image,omitempty"`
	PDF         *MediaTypeConfig `toml:"pdf,omitempty"`
}

// MediaTypeConfig holds configuration for a specific media type
type MediaTypeConfig struct {
	Args        []string `toml:"args,omitempty"`
	ArgsDarwin  []string `toml:"args_darwin,omitempty"`
	ArgsLinux   []string `toml:"args_linux,omitempty"`
	ArgsWindows []string `toml:"args_windows,omitempty"`
}

// PlayersConfig holds all player definitions
type PlayersConfig struct {
	Players map[string]PlayerDefinition `toml:"players"`
}

// PlayerRegistry manages player definitions
type PlayerRegistry struct {
	players map[string]PlayerDefinition
}

// NewPlayerRegistry creates a registry from the embedded TOML
func NewPlayerRegistry() (*PlayerRegistry, error) {
	var config PlayersConfig
	if err := toml.Unmarshal(playersTOML, &config); err != nil {
		return nil, fmt.Errorf("parsing players.toml: %w", err)
	}

	registry := &PlayerRegistry{
		players: config.Players,
	}

	// Try to load user's custom player definitions
	registry.loadUserConfig()

	return registry, nil
}

// loadUserConfig loads custom player definitions from user's config directory
func (r *PlayerRegistry) loadUserConfig() {
	// Try common config locations
	configPaths := []string{
		"~/.config/fwrd/players.toml",
		"./players.toml",
	}

	for _, path := range configPaths {
		if len(path) >= 2 && path[:2] == "~/" {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}

		if data, err := os.ReadFile(path); err == nil {
			var userConfig PlayersConfig
			if err := toml.Unmarshal(data, &userConfig); err == nil {
				// Merge user config (overrides built-in)
				for name, def := range userConfig.Players {
					r.players[name] = def
				}
			}
		}
	}
}

// GetCommand builds the command for a specific player and media type
func (r *PlayerRegistry) GetCommand(playerName string, mediaType MediaType, url string) (*exec.Cmd, error) {
	player, exists := r.players[playerName]
	if !exists {
		// If player not defined, use it with no special args
		return exec.Command(playerName, url), nil
	}

	// Check if player supports this platform
	supportsPlatform := false
	for _, p := range player.Platforms {
		if p == runtime.GOOS {
			supportsPlatform = true
			break
		}
	}

	if !supportsPlatform {
		return nil, fmt.Errorf("%s not supported on %s", playerName, runtime.GOOS)
	}

	// Get the config for this media type
	var config *MediaTypeConfig
	switch mediaType {
	case MediaTypeVideo:
		config = player.Video
	case MediaTypeAudio:
		config = player.Audio
	case MediaTypeImage:
		config = player.Image
	case MediaTypePDF:
		config = player.PDF
	}

	if config == nil {
		// Player doesn't support this media type
		return nil, fmt.Errorf("%s doesn't support media type", playerName)
	}

	// Build the command with appropriate args
	args := r.getArgs(config)
	args = append(args, url)

	return exec.Command(playerName, args...), nil
}

// getArgs returns the appropriate args for the current platform
func (r *PlayerRegistry) getArgs(config *MediaTypeConfig) []string {
	if config == nil {
		return nil
	}

	// Check for platform-specific args first
	switch runtime.GOOS {
	case "darwin":
		if len(config.ArgsDarwin) > 0 {
			return config.ArgsDarwin
		}
	case "linux":
		if len(config.ArgsLinux) > 0 {
			return config.ArgsLinux
		}
	case "windows":
		if len(config.ArgsWindows) > 0 {
			return config.ArgsWindows
		}
	}

	// Fall back to generic args
	return config.Args
}

// IsPlayerAvailable checks if a player is installed
func (r *PlayerRegistry) IsPlayerAvailable(playerName string) bool {
	_, err := exec.LookPath(playerName)
	return err == nil
}

// FindAvailablePlayer finds the first available player from a list
func (r *PlayerRegistry) FindAvailablePlayer(players []string) string {
	for _, player := range players {
		if r.IsPlayerAvailable(player) {
			return player
		}
	}
	return ""
}
