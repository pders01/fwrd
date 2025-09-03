package media

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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
	videoPlayer string
	imageViewer string
	audioPlayer string
	pdfViewer   string
}

func NewLauncher() *Launcher {
	l := &Launcher{}

	switch runtime.GOOS {
	case "darwin":
		l.videoPlayer = findCommand("iina", "mpv", "vlc")
		l.imageViewer = findCommand("sxiv", "feh", "open")
		l.audioPlayer = findCommand("mpv", "vlc", "open")
		l.pdfViewer = "open"
	case "linux":
		l.videoPlayer = findCommand("mpv", "vlc", "mplayer")
		l.imageViewer = findCommand("sxiv", "feh", "eog", "xdg-open")
		l.audioPlayer = findCommand("mpv", "vlc", "mplayer")
		l.pdfViewer = findCommand("zathura", "evince", "xdg-open")
	default:
		l.videoPlayer = "open"
		l.imageViewer = "open"
		l.audioPlayer = "open"
		l.pdfViewer = "open"
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
		cmd = exec.Command(getDefaultOpener(), url)
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
