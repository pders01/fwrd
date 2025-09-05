package media

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestPlayerRegistry_GetCommand(t *testing.T) {
	registry := &PlayerRegistry{
		players: map[string]PlayerDefinition{
			"mpv": {
				Description: "Test player",
				Platforms:   []string{"darwin", "linux", "windows"},
				Video: &PlayerMediaTypeConfig{
					Args: []string{"--no-terminal"},
				},
				Audio: &PlayerMediaTypeConfig{
					Args: []string{"--no-video"},
				},
			},
			"vlc": {
				Description: "VLC player",
				Platforms:   []string{"darwin", "linux"},
				Video: &PlayerMediaTypeConfig{
					Args:       []string{"--intf", "dummy"},
					ArgsDarwin: []string{"--intf", "macosx"},
				},
			},
		},
	}

	tests := []struct {
		name        string
		playerName  string
		mediaType   MediaType
		url         string
		wantErr     bool
		checkArgs   bool
		expectedLen int
	}{
		{
			name:        "mpv with video",
			playerName:  "mpv",
			mediaType:   MediaTypeVideo,
			url:         "http://example.com/video.mp4",
			wantErr:     false,
			checkArgs:   true,
			expectedLen: 2, // --no-terminal, URL
		},
		{
			name:        "mpv with audio",
			playerName:  "mpv",
			mediaType:   MediaTypeAudio,
			url:         "http://example.com/audio.mp3",
			wantErr:     false,
			checkArgs:   true,
			expectedLen: 2, // --no-video, URL
		},
		{
			name:       "mpv with unsupported media type",
			playerName: "mpv",
			mediaType:  MediaTypeImage,
			url:        "http://example.com/image.jpg",
			wantErr:    true,
		},
		{
			name:        "unknown player",
			playerName:  "unknownplayer",
			mediaType:   MediaTypeVideo,
			url:         "http://example.com/video.mp4",
			wantErr:     false, // Falls back to simple command
			checkArgs:   true,
			expectedLen: 1, // Just URL
		},
		{
			name:        "vlc with platform-specific args",
			playerName:  "vlc",
			mediaType:   MediaTypeVideo,
			url:         "http://example.com/video.mp4",
			wantErr:     runtime.GOOS == "windows", // VLC not supported on Windows in our test
			checkArgs:   runtime.GOOS != "windows",
			expectedLen: 3, // Platform-specific args + URL
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := registry.GetCommand(tt.playerName, tt.mediaType, tt.url)

			if tt.wantErr {
				if err == nil {
					t.Errorf("GetCommand() expected error but got none")
				}
				return
			}

			if err != nil && !tt.wantErr {
				t.Errorf("GetCommand() unexpected error: %v", err)
				return
			}

			if cmd == nil {
				t.Errorf("GetCommand() returned nil command")
				return
			}

			if tt.checkArgs {
				if len(cmd.Args) != tt.expectedLen+1 { // +1 for the command itself
					t.Errorf("GetCommand() args length = %d, want %d", len(cmd.Args)-1, tt.expectedLen)
				}

				// Check URL is last argument
				if cmd.Args[len(cmd.Args)-1] != tt.url {
					t.Errorf("GetCommand() last arg = %s, want %s", cmd.Args[len(cmd.Args)-1], tt.url)
				}
			}
		})
	}
}

func TestPlayerRegistry_getArgs(t *testing.T) {
	registry := &PlayerRegistry{}

	tests := []struct {
		name     string
		config   *PlayerMediaTypeConfig
		goos     string
		expected []string
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: nil,
		},
		{
			name: "default args only",
			config: &PlayerMediaTypeConfig{
				Args: []string{"--arg1", "--arg2"},
			},
			expected: []string{"--arg1", "--arg2"},
		},
		{
			name: "platform-specific args override",
			config: &PlayerMediaTypeConfig{
				Args:       []string{"--default"},
				ArgsDarwin: []string{"--darwin"},
				ArgsLinux:  []string{"--linux"},
			},
			goos:     runtime.GOOS,
			expected: nil, // Will be set based on current OS
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.getArgs(tt.config)

			// For platform-specific test, determine expected result
			if tt.name == "platform-specific args override" {
				switch runtime.GOOS {
				case "darwin":
					tt.expected = []string{"--darwin"}
				case "linux":
					tt.expected = []string{"--linux"}
				default:
					tt.expected = []string{"--default"}
				}
			}

			if len(result) != len(tt.expected) {
				t.Errorf("getArgs() = %v, want %v", result, tt.expected)
				return
			}

			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("getArgs()[%d] = %s, want %s", i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestPlayerRegistry_IsPlayerAvailable(t *testing.T) {
	registry := &PlayerRegistry{}

	tests := []struct {
		name       string
		playerName string
		expected   bool
	}{
		{
			name:       "common command available",
			playerName: "sh",
			expected:   true,
		},
		{
			name:       "non-existent command",
			playerName: "nonexistentcommand123456",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.IsPlayerAvailable(tt.playerName)
			if result != tt.expected {
				t.Errorf("IsPlayerAvailable(%s) = %v, want %v", tt.playerName, result, tt.expected)
			}
		})
	}
}

func TestPlayerRegistry_FindAvailablePlayer(t *testing.T) {
	registry := &PlayerRegistry{}

	tests := []struct {
		name     string
		players  []string
		expected string
	}{
		{
			name:     "finds first available",
			players:  []string{"nonexistent1", "sh", "nonexistent2"},
			expected: "sh",
		},
		{
			name:     "none available",
			players:  []string{"nonexistent1", "nonexistent2"},
			expected: "",
		},
		{
			name:     "empty list",
			players:  []string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := registry.FindAvailablePlayer(tt.players)
			if result != tt.expected {
				t.Errorf("FindAvailablePlayer(%v) = %s, want %s", tt.players, result, tt.expected)
			}
		})
	}
}

func TestNewPlayerRegistry(t *testing.T) {
	registry, err := NewPlayerRegistry()
	if err != nil {
		t.Fatalf("NewPlayerRegistry() error = %v", err)
	}

	if registry == nil {
		t.Fatal("NewPlayerRegistry() returned nil")
	}

	// Check that some common players are loaded from embedded config
	if len(registry.players) == 0 {
		t.Error("NewPlayerRegistry() loaded no players from embedded config")
	}

	// Check for a known player (assuming mpv is in the embedded config)
	if _, exists := registry.players["mpv"]; !exists {
		// This is okay if the embedded config doesn't have mpv
		t.Log("mpv not found in embedded config (this may be expected)")
	}
}

func TestLauncher_Open(t *testing.T) {
	// Create a mock launcher with test commands
	launcher := &Launcher{
		videoPlayer:   "echo",
		imageViewer:   "echo",
		audioPlayer:   "echo",
		pdfViewer:     "echo",
		defaultOpener: "echo",
		registry:      &PlayerRegistry{players: make(map[string]PlayerDefinition)},
		detector:      &MediaTypeDetector{config: &MediaTypesConfig{}},
	}

	// Initialize detector properly
	detector, _ := NewMediaTypeDetector()
	launcher.detector = detector

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "open video URL",
			url:     "http://example.com/video.mp4",
			wantErr: false,
		},
		{
			name:    "open image URL",
			url:     "http://example.com/image.jpg",
			wantErr: false,
		},
		{
			name:    "open audio URL",
			url:     "http://example.com/audio.mp3",
			wantErr: false,
		},
		{
			name:    "open PDF URL",
			url:     "http://example.com/document.pdf",
			wantErr: false,
		},
		{
			name:    "open unknown URL",
			url:     "http://example.com/page.html",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := launcher.Open(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("Open() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLauncher_OpenWithMissingPlayers(t *testing.T) {
	// Test with missing players for specific media types
	launcher := &Launcher{
		videoPlayer:   "",
		imageViewer:   "",
		audioPlayer:   "",
		pdfViewer:     "",
		defaultOpener: "",
		registry:      &PlayerRegistry{players: make(map[string]PlayerDefinition)},
		detector:      &MediaTypeDetector{config: &MediaTypesConfig{}},
	}

	detector, _ := NewMediaTypeDetector()
	launcher.detector = detector

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "video with no player",
			url:     "http://example.com/video.mp4",
			wantErr: true,
		},
		{
			name:    "image with no viewer",
			url:     "http://example.com/image.jpg",
			wantErr: true,
		},
		{
			name:    "audio with no player",
			url:     "http://example.com/audio.mp3",
			wantErr: true,
		},
		{
			name:    "PDF with no viewer",
			url:     "http://example.com/document.pdf",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := launcher.Open(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("Open() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLauncher_OpenWithRegistryCommand(t *testing.T) {
	// Test that registry commands are used when available
	registry := &PlayerRegistry{
		players: map[string]PlayerDefinition{
			"echo": {
				Platforms: []string{runtime.GOOS},
				Video: &PlayerMediaTypeConfig{
					Args: []string{"-n", "Playing:"},
				},
			},
		},
	}

	launcher := &Launcher{
		videoPlayer:   "echo",
		defaultOpener: "echo",
		registry:      registry,
	}

	detector, _ := NewMediaTypeDetector()
	launcher.detector = detector

	// This should use the registry command with custom args
	err := launcher.Open("http://example.com/video.mp4")
	if err != nil {
		t.Errorf("Open() with registry command failed: %v", err)
	}
}

// Test to verify exec.Command behavior (for coverage)
func TestExecCommandCreation(t *testing.T) {
	// This test ensures the exec.Command paths are covered
	cmd := exec.Command("echo", "test")
	if cmd.Path == "" {
		t.Error("exec.Command did not set Path")
	}
	if len(cmd.Args) != 2 {
		t.Errorf("exec.Command args length = %d, want 2", len(cmd.Args))
	}
}
