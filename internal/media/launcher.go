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
	registry      *PlayerRegistry
}

func NewLauncher(cfg *config.Config) *Launcher {
	registry, err := NewPlayerRegistry()
	if err != nil {
		// Continue with basic functionality if player definitions can't be loaded
		registry = &PlayerRegistry{players: make(map[string]PlayerDefinition)}
	}

	l := &Launcher{
		config:        &cfg.Media,
		defaultOpener: cfg.Media.DefaultOpener,
		registry:      registry,
	}

	var players config.MediaPlayers
	switch runtime.GOOS {
	case "darwin":
		players = cfg.Media.Darwin
	case "linux":
		players = cfg.Media.Linux
	case "windows":
		players = cfg.Media.Windows
	default:
		players = cfg.Media.Darwin
	}

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

	var playerName string
	switch mediaType {
	case MediaTypeVideo:
		if l.videoPlayer == "" {
			return fmt.Errorf("no video player found")
		}
		playerName = l.videoPlayer
	case MediaTypeImage:
		if l.imageViewer == "" {
			return fmt.Errorf("no image viewer found")
		}
		playerName = l.imageViewer
	case MediaTypeAudio:
		if l.audioPlayer == "" {
			return fmt.Errorf("no audio player found")
		}
		playerName = l.audioPlayer
	case MediaTypePDF:
		if l.pdfViewer == "" {
			return fmt.Errorf("no PDF viewer found")
		}
		playerName = l.pdfViewer
	default:
		playerName = l.defaultOpener
	}

	cmd, err := l.registry.GetCommand(playerName, mediaType, url)
	if err != nil {
		cmd = exec.Command(playerName, url)
	}

	// Start GUI applications detached
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", cmd.Args[0], err)
	}

	go func() {
		cmd.Wait()
	}()

	return nil
}

func detectMediaType(url string) MediaType {
	lower := strings.ToLower(url)
	isURL := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")

	// Extract file extension, handling URLs with query params and anchors
	var ext string
	if idx := strings.LastIndex(lower, "."); idx != -1 {
		ext = lower[idx:]
		if qIdx := strings.Index(ext, "?"); qIdx != -1 {
			ext = ext[:qIdx]
		}
		if aIdx := strings.Index(ext, "#"); aIdx != -1 {
			ext = ext[:aIdx]
		}
	}

	switch ext {
	case ".mp4", ".webm", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg", ".3gp":
		return MediaTypeVideo
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg", ".ico", ".tiff":
		return MediaTypeImage
	case ".mp3", ".ogg", ".wav", ".flac", ".m4a", ".aac", ".opus", ".wma":
		return MediaTypeAudio
	case ".pdf":
		return MediaTypePDF
	}

	if isURL {
		// Video platforms
		if strings.Contains(lower, "/video/") || strings.Contains(lower, "/watch") ||
			strings.Contains(lower, "/embed/") || strings.Contains(lower, "/player/") ||
			strings.Contains(lower, "youtube.") || strings.Contains(lower, "youtu.be") ||
			strings.Contains(lower, "vimeo.") || strings.Contains(lower, "dailymotion.") ||
			strings.Contains(lower, "twitch.tv") {
			return MediaTypeVideo
		}

		// Podcast/audio platforms (common in RSS)
		if strings.Contains(lower, "/audio/") || strings.Contains(lower, "/podcast") ||
			strings.Contains(lower, "/episode") || strings.Contains(lower, "/show/") ||
			strings.Contains(lower, "soundcloud.") || strings.Contains(lower, "spotify.") ||
			strings.Contains(lower, "podcasts.") || strings.Contains(lower, "castbox.") ||
			strings.Contains(lower, "podbean.") || strings.Contains(lower, "buzzsprout.") {
			return MediaTypeAudio
		}

		// Image platforms
		if strings.Contains(lower, "/image/") || strings.Contains(lower, "/img/") ||
			strings.Contains(lower, "/photo/") || strings.Contains(lower, "/gallery/") ||
			strings.Contains(lower, "imgur.") || strings.Contains(lower, "flickr.") ||
			strings.Contains(lower, "instagram.") {
			return MediaTypeImage
		}

		// Most URLs in RSS feeds are articles/web pages, not media files
		return MediaTypeUnknown
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
