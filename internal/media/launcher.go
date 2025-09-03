package media

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/pders01/fwrd/internal/config"
)

type MediaType int

const (
	MediaTypeVideo MediaType = iota
	MediaTypeImage
	MediaTypeAudio
	MediaTypePDF
	MediaTypeUnknown
)

type Launcher struct {
	videoPlayer   string
	imageViewer   string
	audioPlayer   string
	pdfViewer     string
	defaultOpener string
	config        *config.MediaConfig
}

func NewLauncher(cfg *config.Config) *Launcher {
	l := &Launcher{
		config:        &cfg.Media,
		defaultOpener: cfg.Media.DefaultOpener,
	}

	// Get platform-specific players from config
	var players config.MediaPlayers
	switch runtime.GOOS {
	case "darwin":
		players = cfg.Media.Darwin
	case "linux":
		players = cfg.Media.Linux
	case "windows":
		players = cfg.Media.Windows
	default:
		// Use Darwin as fallback
		players = cfg.Media.Darwin
	}

	// Find available commands from configured preferences
	if len(players.Video) > 0 {
		l.videoPlayer = findCommand(players.Video...)
	}
	if len(players.Image) > 0 {
		l.imageViewer = findCommand(players.Image...)
	}
	if len(players.Audio) > 0 {
		l.audioPlayer = findCommand(players.Audio...)
	}
	if len(players.PDF) > 0 {
		l.pdfViewer = findCommand(players.PDF...)
	}

	// Fallback to default opener if no specific player found
	if l.videoPlayer == "" {
		l.videoPlayer = l.defaultOpener
	}
	if l.imageViewer == "" {
		l.imageViewer = l.defaultOpener
	}
	if l.audioPlayer == "" {
		l.audioPlayer = l.defaultOpener
	}
	if l.pdfViewer == "" {
		l.pdfViewer = l.defaultOpener
	}

	return l
}

func (l *Launcher) Open(url string) error {
	mediaType := detectMediaType(url)

	var cmd *exec.Cmd
	switch mediaType {
	case MediaTypeVideo:
		if l.videoPlayer == "" {
			return fmt.Errorf("no video player found")
		}
		cmd = exec.Command(l.videoPlayer, url)
	case MediaTypeImage:
		if l.imageViewer == "" {
			return fmt.Errorf("no image viewer found")
		}
		cmd = exec.Command(l.imageViewer, url)
	case MediaTypeAudio:
		if l.audioPlayer == "" {
			return fmt.Errorf("no audio player found")
		}
		cmd = exec.Command(l.audioPlayer, url)
	case MediaTypePDF:
		if l.pdfViewer == "" {
			return fmt.Errorf("no PDF viewer found")
		}
		cmd = exec.Command(l.pdfViewer, url)
	default:
		cmd = exec.Command(l.defaultOpener, url)
	}

	// For GUI applications like iina, we want to start them detached
	// but also check if the command can at least be executed
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", cmd.Args[0], err)
	}

	// Don't wait for GUI applications - let them run independently
	go func() {
		cmd.Wait() // Clean up the process when it exits
	}()

	return nil
}

func detectMediaType(url string) MediaType {
	lower := strings.ToLower(url)

	videoExts := []string{".mp4", ".webm", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".m4v"}
	for _, ext := range videoExts {
		if strings.Contains(lower, ext) {
			return MediaTypeVideo
		}
	}

	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}
	for _, ext := range imageExts {
		if strings.Contains(lower, ext) {
			return MediaTypeImage
		}
	}

	audioExts := []string{".mp3", ".ogg", ".wav", ".flac", ".m4a", ".aac"}
	for _, ext := range audioExts {
		if strings.Contains(lower, ext) {
			return MediaTypeAudio
		}
	}

	if strings.Contains(lower, ".pdf") {
		return MediaTypePDF
	}

	if strings.Contains(lower, "youtube.com") || strings.Contains(lower, "youtu.be") ||
		strings.Contains(lower, "vimeo.com") || strings.Contains(lower, "twitch.tv") {
		return MediaTypeVideo
	}

	return MediaTypeUnknown
}

func findCommand(commands ...string) string {
	for _, cmd := range commands {
		if _, err := exec.LookPath(cmd); err == nil {
			return cmd
		}
	}
	return ""
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
