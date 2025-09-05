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

type PlayerDefinition struct {
	Description string                 `toml:"description"`
	Platforms   []string               `toml:"platforms"`
	Video       *PlayerMediaTypeConfig `toml:"video,omitempty"`
	Audio       *PlayerMediaTypeConfig `toml:"audio,omitempty"`
	Image       *PlayerMediaTypeConfig `toml:"image,omitempty"`
	PDF         *PlayerMediaTypeConfig `toml:"pdf,omitempty"`
}

type PlayerMediaTypeConfig struct {
	Args        []string `toml:"args,omitempty"`
	ArgsDarwin  []string `toml:"args_darwin,omitempty"`
	ArgsLinux   []string `toml:"args_linux,omitempty"`
	ArgsWindows []string `toml:"args_windows,omitempty"`
}

type PlayersConfig struct {
	Players map[string]PlayerDefinition `toml:"players"`
}

type PlayerRegistry struct {
	players map[string]PlayerDefinition
}

func NewPlayerRegistry() (*PlayerRegistry, error) {
	var config PlayersConfig
	if err := toml.Unmarshal(playersTOML, &config); err != nil {
		return nil, fmt.Errorf("parsing players.toml: %w", err)
	}

	registry := &PlayerRegistry{
		players: config.Players,
	}

	registry.loadUserConfig()

	return registry, nil
}

func (r *PlayerRegistry) loadUserConfig() {
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
				for name, def := range userConfig.Players {
					r.players[name] = def
				}
			}
		}
	}
}

func (r *PlayerRegistry) GetCommand(playerName string, mediaType Type, url string) (*exec.Cmd, error) {
	player, exists := r.players[playerName]
	if !exists {
		return exec.Command(playerName, url), nil
	}

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

	var config *PlayerMediaTypeConfig
	switch mediaType {
	case TypeVideo:
		config = player.Video
	case TypeAudio:
		config = player.Audio
	case TypeImage:
		config = player.Image
	case TypePDF:
		config = player.PDF
	}

	if config == nil {
		return nil, fmt.Errorf("%s doesn't support media type", playerName)
	}

	args := r.getArgs(config)
	args = append(args, url)

	return exec.Command(playerName, args...), nil
}

func (r *PlayerRegistry) getArgs(config *PlayerMediaTypeConfig) []string {
	if config == nil {
		return nil
	}

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

	return config.Args
}

func (r *PlayerRegistry) IsPlayerAvailable(playerName string) bool {
	_, err := exec.LookPath(playerName)
	return err == nil
}

func (r *PlayerRegistry) FindAvailablePlayer(players []string) string {
	for _, player := range players {
		if r.IsPlayerAvailable(player) {
			return player
		}
	}
	return ""
}
