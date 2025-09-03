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
		// Special handling for different video players
		switch l.videoPlayer {
		case "iina":
			// iina needs --no-stdin flag for URLs to work properly
			cmd = exec.Command(l.videoPlayer, "--no-stdin", url)
		case "mpv":
			// mpv works with URLs directly, but we can add useful flags
			cmd = exec.Command(l.videoPlayer, "--force-window", url)
		case "vlc":
			// VLC needs special flags for better URL handling
			cmd = exec.Command(l.videoPlayer, "--intf", "macosx", url)
		default:
			// Fallback for other players
			cmd = exec.Command(l.videoPlayer, url)
		}
	case MediaTypeImage:
		if l.imageViewer == "" {
			return fmt.Errorf("no image viewer found")
		}
		cmd = exec.Command(l.imageViewer, url)
	case MediaTypeAudio:
		if l.audioPlayer == "" {
			return fmt.Errorf("no audio player found")
		}
		// Audio players may also need special handling for URLs
		switch l.audioPlayer {
		case "mpv":
			cmd = exec.Command(l.audioPlayer, "--force-window", "--keep-open", url)
		default:
			cmd = exec.Command(l.audioPlayer, url)
		}
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

	// Check if this is a URL or a local file path
	isURL := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")

	// Extract file extension properly (handle URLs with query params)
	var ext string
	if idx := strings.LastIndex(lower, "."); idx != -1 {
		ext = lower[idx:]
		// Remove query parameters if present (e.g., .mp4?param=value)
		if qIdx := strings.Index(ext, "?"); qIdx != -1 {
			ext = ext[:qIdx]
		}
		// Remove anchors if present (e.g., .html#section)
		if aIdx := strings.Index(ext, "#"); aIdx != -1 {
			ext = ext[:aIdx]
		}
	}

	// Check specific file extensions
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

	// For URLs without a clear media extension, make intelligent guesses
	if isURL {
		// Check for video indicators - be specific about video platforms
		if strings.Contains(lower, "/video/") || strings.Contains(lower, "/watch") ||
			strings.Contains(lower, "/embed/") || strings.Contains(lower, "/player/") ||
			strings.Contains(lower, "youtube.") || strings.Contains(lower, "youtu.be") ||
			strings.Contains(lower, "vimeo.") || strings.Contains(lower, "dailymotion.") ||
			strings.Contains(lower, "twitch.tv") {
			return MediaTypeVideo
		}

		// Check for podcast/audio indicators - very common in RSS
		if strings.Contains(lower, "/audio/") || strings.Contains(lower, "/podcast") ||
			strings.Contains(lower, "/episode") || strings.Contains(lower, "/show/") ||
			strings.Contains(lower, "soundcloud.") || strings.Contains(lower, "spotify.") ||
			strings.Contains(lower, "podcasts.") || strings.Contains(lower, "castbox.") ||
			strings.Contains(lower, "podbean.") || strings.Contains(lower, "buzzsprout.") {
			return MediaTypeAudio
		}

		// Check for image indicators
		if strings.Contains(lower, "/image/") || strings.Contains(lower, "/img/") ||
			strings.Contains(lower, "/photo/") || strings.Contains(lower, "/gallery/") ||
			strings.Contains(lower, "imgur.") || strings.Contains(lower, "flickr.") ||
			strings.Contains(lower, "instagram.") {
			return MediaTypeImage
		}

		// For unknown URLs, return Unknown and let the default opener handle it
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
