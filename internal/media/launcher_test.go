package media

import (
	"runtime"
	"testing"

	"github.com/pders01/fwrd/internal/config"
)

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected MediaType
	}{
		// Video tests
		{name: "MP4 video", url: "http://example.com/video.mp4", expected: MediaTypeVideo},
		{name: "WebM video", url: "http://example.com/video.webm", expected: MediaTypeVideo},
		{name: "MKV video", url: "http://example.com/video.mkv", expected: MediaTypeVideo},
		{name: "AVI video", url: "http://example.com/video.avi", expected: MediaTypeVideo},
		{name: "MOV video", url: "http://example.com/video.mov", expected: MediaTypeVideo},
		{name: "YouTube URL", url: "https://www.youtube.com/watch?v=abc123", expected: MediaTypeVideo},
		{name: "YouTube short URL", url: "https://youtu.be/abc123", expected: MediaTypeVideo},
		{name: "Vimeo URL", url: "https://vimeo.com/123456", expected: MediaTypeVideo},
		{name: "Twitch URL", url: "https://www.twitch.tv/stream", expected: MediaTypeVideo},

		// Image tests
		{name: "JPEG image", url: "http://example.com/photo.jpg", expected: MediaTypeImage},
		{name: "JPEG image alt", url: "http://example.com/photo.jpeg", expected: MediaTypeImage},
		{name: "PNG image", url: "http://example.com/image.png", expected: MediaTypeImage},
		{name: "GIF image", url: "http://example.com/animation.gif", expected: MediaTypeImage},
		{name: "WebP image", url: "http://example.com/photo.webp", expected: MediaTypeImage},
		{name: "BMP image", url: "http://example.com/bitmap.bmp", expected: MediaTypeImage},
		{name: "SVG image", url: "http://example.com/vector.svg", expected: MediaTypeImage},

		// Audio tests
		{name: "MP3 audio", url: "http://example.com/song.mp3", expected: MediaTypeAudio},
		{name: "OGG audio", url: "http://example.com/sound.ogg", expected: MediaTypeAudio},
		{name: "WAV audio", url: "http://example.com/audio.wav", expected: MediaTypeAudio},
		{name: "FLAC audio", url: "http://example.com/music.flac", expected: MediaTypeAudio},
		{name: "M4A audio", url: "http://example.com/track.m4a", expected: MediaTypeAudio},
		{name: "AAC audio", url: "http://example.com/audio.aac", expected: MediaTypeAudio},

		// PDF tests
		{name: "PDF document", url: "http://example.com/document.pdf", expected: MediaTypePDF},
		{name: "PDF with query", url: "http://example.com/doc.pdf?version=2", expected: MediaTypePDF},

		// Unknown tests
		{name: "HTML page", url: "http://example.com/page.html", expected: MediaTypeUnknown},
		{name: "Text file", url: "http://example.com/readme.txt", expected: MediaTypeUnknown},
		{name: "No extension", url: "http://example.com/resource", expected: MediaTypeUnknown},
		{name: "Unknown extension", url: "http://example.com/file.xyz", expected: MediaTypeUnknown},

		// Case insensitive tests
		{name: "Uppercase MP4", url: "http://example.com/VIDEO.MP4", expected: MediaTypeVideo},
		{name: "Mixed case JPEG", url: "http://example.com/Photo.JpEg", expected: MediaTypeImage},
		{name: "Uppercase PDF", url: "http://example.com/DOCUMENT.PDF", expected: MediaTypePDF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectMediaType(tt.url)
			if result != tt.expected {
				t.Errorf("detectMediaType(%s) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestFindCommand(t *testing.T) {
	// This test is limited as it depends on system configuration
	// We'll test the basic functionality

	tests := []struct {
		name     string
		commands []string
		validate func(result string) bool
	}{
		{
			name:     "empty list returns empty",
			commands: []string{},
			validate: func(result string) bool {
				return result == ""
			},
		},
		{
			name:     "non-existent commands return empty",
			commands: []string{"nonexistent1", "nonexistent2", "nonexistent3"},
			validate: func(result string) bool {
				return result == ""
			},
		},
		{
			name:     "common command found",
			commands: []string{"nonexistent", "sh", "alsononexistent"},
			validate: func(result string) bool {
				return result == "sh"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findCommand(tt.commands...)
			if !tt.validate(result) {
				t.Errorf("findCommand(%v) validation failed, got: %s", tt.commands, result)
			}
		})
	}
}

func TestNewLauncher(t *testing.T) {
	cfg := &config.Config{
		Media: config.MediaConfig{
			Darwin: config.MediaPlayers{
				Video: []string{"mpv", "vlc"},
				Image: []string{"open"},
				Audio: []string{"mpv"},
				PDF:   []string{"open"},
			},
			DefaultOpener: "open",
		},
	}
	launcher := NewLauncher(cfg)

	if launcher == nil {
		t.Fatal("NewLauncher() returned nil")
	}

	// Verify that launcher has been initialized
	// The actual values will depend on the system and installed software
	// We just verify the structure is correct

	// On any system, at least the fallback values should be set
	if launcher.videoPlayer == "" && launcher.imageViewer == "" &&
		launcher.audioPlayer == "" && launcher.pdfViewer == "" {
		t.Error("NewLauncher() did not initialize any media handlers")
	}
}

func TestGetDefaultOpener(t *testing.T) {
	opener := getDefaultOpener()

	expectedOpeners := map[string]string{
		"darwin":  "open",
		"linux":   "xdg-open",
		"windows": "start",
	}

	// Check if we got a non-empty result
	if opener == "" {
		t.Error("getDefaultOpener() returned empty string")
	}

	// If we know the expected opener for this OS, verify it
	if expected, ok := expectedOpeners[runtime.GOOS]; ok {
		if opener != expected {
			t.Errorf("getDefaultOpener() on %s = %s, want %s", runtime.GOOS, opener, expected)
		}
	}
}
