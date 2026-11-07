package media

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestPlayerExamplesMatchTheLauncherArgumentContract(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "players.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var definitions PlayersConfig
	if decodeErr := toml.NewDecoder(bytes.NewReader(content)).DisallowUnknownFields().Decode(&definitions); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if definitions.Players["iina"].Audio == nil {
		t.Fatal("example replacement silently removes built-in audio support")
	}
	registry := &PlayerRegistry{players: definitions.Players}
	for name, player := range definitions.Players {
		for _, platform := range player.Platforms {
			if platform != runtime.GOOS {
				continue
			}
			cmd, commandErr := registry.GetCommand(name, TypeVideo, "https://media.example.org/video")
			if commandErr != nil {
				t.Fatal(commandErr)
			}
			args := registry.getArgs(player.Video)
			want := append(append([]string{name}, args...), "https://media.example.org/video")
			if !reflect.DeepEqual(cmd.Args, want) {
				t.Fatalf("example %s argument ordering = %q, want %q", name, cmd.Args, want)
			}
		}
	}
	wantStreamlink := []string{"--player", "mpv", "--default-stream", "best"}
	if !reflect.DeepEqual(definitions.Players["streamlink"].Video.Args, wantStreamlink) {
		t.Fatal("streamlink example requires a positional argument after the appended URL")
	}
}
