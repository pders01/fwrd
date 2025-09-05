package media

import (
	"runtime"
	"testing"

	"github.com/pders01/fwrd/internal/config"
)

func TestDetectMediaType(t *testing.T) {
	detector, err := NewTypeDetector()
	if err != nil {
		t.Fatalf("Failed to create detector: %v", err)
	}

	tests := []struct {
		name     string
		url      string
		expected Type
	}{
		// Video tests
		{name: "MP4 video", url: "http://example.com/video.mp4", expected: TypeVideo},
		{name: "WebM video", url: "http://example.com/video.webm", expected: TypeVideo},
		{name: "MKV video", url: "http://example.com/video.mkv", expected: TypeVideo},
		{name: "AVI video", url: "http://example.com/video.avi", expected: TypeVideo},
		{name: "MOV video", url: "http://example.com/video.mov", expected: TypeVideo},
		{name: "YouTube URL", url: "https://www.youtube.com/watch?v=abc123", expected: TypeVideo},
		{name: "YouTube short URL", url: "https://youtu.be/abc123", expected: TypeVideo},
		{name: "Vimeo URL", url: "https://vimeo.com/123456", expected: TypeVideo},
		{name: "Twitch URL", url: "https://www.twitch.tv/stream", expected: TypeVideo},

		// Image tests
		{name: "JPEG image", url: "http://example.com/photo.jpg", expected: TypeImage},
		{name: "JPEG image alt", url: "http://example.com/photo.jpeg", expected: TypeImage},
		{name: "PNG image", url: "http://example.com/image.png", expected: TypeImage},
		{name: "GIF image", url: "http://example.com/animation.gif", expected: TypeImage},
		{name: "WebP image", url: "http://example.com/photo.webp", expected: TypeImage},
		{name: "BMP image", url: "http://example.com/bitmap.bmp", expected: TypeImage},
		{name: "SVG image", url: "http://example.com/vector.svg", expected: TypeImage},

		// Audio tests
		{name: "MP3 audio", url: "http://example.com/song.mp3", expected: TypeAudio},
		{name: "OGG audio", url: "http://example.com/sound.ogg", expected: TypeAudio},
		{name: "WAV audio", url: "http://example.com/audio.wav", expected: TypeAudio},
		{name: "FLAC audio", url: "http://example.com/music.flac", expected: TypeAudio},
		{name: "M4A audio", url: "http://example.com/track.m4a", expected: TypeAudio},
		{name: "AAC audio", url: "http://example.com/audio.aac", expected: TypeAudio},

		// PDF tests
		{name: "PDF document", url: "http://example.com/document.pdf", expected: TypePDF},
		{name: "PDF with query", url: "http://example.com/doc.pdf?version=2", expected: TypePDF},

		// Unknown tests
		{name: "HTML page", url: "http://example.com/page.html", expected: TypeUnknown},
		{name: "Text file", url: "http://example.com/readme.txt", expected: TypeUnknown},
		{name: "No extension", url: "http://example.com/resource", expected: TypeUnknown},
		{name: "Unknown extension", url: "http://example.com/file.xyz", expected: TypeUnknown},

		// Case insensitive tests
		{name: "Uppercase MP4", url: "http://example.com/VIDEO.MP4", expected: TypeVideo},
		{name: "Mixed case JPEG", url: "http://example.com/Photo.JpEg", expected: TypeImage},
		{name: "Uppercase PDF", url: "http://example.com/DOCUMENT.PDF", expected: TypePDF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectType(tt.url)
			if result != tt.expected {
				t.Errorf("DetectType(%s) = %v, want %v", tt.url, result, tt.expected)
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
	detector, err := NewTypeDetector()
	if err != nil {
		t.Fatalf("Failed to create detector: %v", err)
	}

	opener := detector.GetDefaultOpener()

	expectedOpeners := map[string]string{
		"darwin":  "open",
		"linux":   "xdg-open",
		"windows": "start",
	}

	// Check if we got a non-empty result
	if opener == "" {
		t.Error("GetDefaultOpener() returned empty string")
	}

	// If we know the expected opener for this OS, verify it
	if expected, ok := expectedOpeners[runtime.GOOS]; ok {
		if opener != expected {
			t.Errorf("GetDefaultOpener() on %s = %s, want %s", runtime.GOOS, opener, expected)
		}
	}
}
