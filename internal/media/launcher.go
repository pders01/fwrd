package media

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/pders01/fwrd/internal/config"
)

type Type int

const (
	TypeVideo Type = iota
	TypeImage
	TypeAudio
	TypePDF
	TypeUnknown
)

type Launcher struct {
	videoPlayer   string
	imageViewer   string
	audioPlayer   string
	pdfViewer     string
	defaultOpener string
	config        *config.MediaConfig
	registry      *PlayerRegistry
	detector      *TypeDetector
}

func NewLauncher(cfg *config.Config) *Launcher {
	registry, err := NewPlayerRegistry()
	if err != nil {
		// Continue with basic functionality if player definitions can't be loaded
		registry = &PlayerRegistry{players: make(map[string]PlayerDefinition)}
	}

	detector, err := NewTypeDetector()
	if err != nil {
		// Fallback to a basic detector if config can't be loaded
		detector = &TypeDetector{config: &TypesConfig{}}
	}

	// Ensure we always have a default opener
	defaultOpener := cfg.Media.DefaultOpener
	if defaultOpener == "" {
		defaultOpener = detector.GetDefaultOpener()
	}

	l := &Launcher{
		config:        &cfg.Media,
		defaultOpener: defaultOpener,
		registry:      registry,
		detector:      detector,
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
	mediaType := l.detector.DetectType(url)

	var playerName string
	switch mediaType {
	case TypeVideo:
		if l.videoPlayer == "" {
			return fmt.Errorf("no video player found")
		}
		playerName = l.videoPlayer
	case TypeImage:
		if l.imageViewer == "" {
			return fmt.Errorf("no image viewer found")
		}
		playerName = l.imageViewer
	case TypeAudio:
		if l.audioPlayer == "" {
			return fmt.Errorf("no audio player found")
		}
		playerName = l.audioPlayer
	case TypePDF:
		if l.pdfViewer == "" {
			return fmt.Errorf("no PDF viewer found")
		}
		playerName = l.pdfViewer
	default:
		playerName = l.defaultOpener
		// Final fallback if defaultOpener is still empty
		if playerName == "" {
			playerName = l.detector.GetDefaultOpener()
		}
	}

	// Ensure we have a valid command
	if playerName == "" {
		return fmt.Errorf("no application found to open URL")
	}

	cmd, err := l.registry.GetCommand(playerName, mediaType, url)
	if err != nil {
		cmd = exec.Command(playerName, url)
	}

	// Start GUI applications detached
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", playerName, err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

func findCommand(commands ...string) string {
	for _, cmd := range commands {
		if _, err := exec.LookPath(cmd); err == nil {
			return cmd
		}
	}
	return ""
}
