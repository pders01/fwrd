package media

import (
	_ "embed"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed media_types.toml
var mediaTypesTOML []byte

type TypeConfig struct {
	Extensions  []string `toml:"extensions"`
	URLPatterns []string `toml:"url_patterns"`
}

type TypesConfig struct {
	Video     TypeConfig                `toml:"video"`
	Audio     TypeConfig                `toml:"audio"`
	Image     TypeConfig                `toml:"image"`
	PDF       TypeConfig                `toml:"pdf"`
	Platforms map[string]PlatformConfig `toml:"platforms"`
}

type PlatformConfig struct {
	DefaultOpener string `toml:"default_opener"`
}

type TypeDetector struct {
	config *TypesConfig
}

func NewTypeDetector() (*TypeDetector, error) {
	var config TypesConfig
	if err := toml.Unmarshal(mediaTypesTOML, &config); err != nil {
		return nil, err
	}

	return &TypeDetector{config: &config}, nil
}

func (d *TypeDetector) DetectType(url string) Type {
	lower := strings.ToLower(url)
	isURL := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")

	// Extract file extension, handling URLs with query params and anchors
	var ext string
	if idx := strings.LastIndex(lower, "."); idx != -1 {
		ext = lower[idx+1:] // Get extension without the dot
		if qIdx := strings.Index(ext, "?"); qIdx != -1 {
			ext = ext[:qIdx]
		}
		if aIdx := strings.Index(ext, "#"); aIdx != -1 {
			ext = ext[:aIdx]
		}
	}

	// Check file extensions
	if ext != "" {
		if d.hasExtension(d.config.Video.Extensions, ext) {
			return TypeVideo
		}
		if d.hasExtension(d.config.Audio.Extensions, ext) {
			return TypeAudio
		}
		if d.hasExtension(d.config.Image.Extensions, ext) {
			return TypeImage
		}
		if d.hasExtension(d.config.PDF.Extensions, ext) {
			return TypePDF
		}
	}

	// Check URL patterns
	if isURL {
		if d.matchesPattern(lower, d.config.Video.URLPatterns) {
			return TypeVideo
		}
		if d.matchesPattern(lower, d.config.Audio.URLPatterns) {
			return TypeAudio
		}
		if d.matchesPattern(lower, d.config.Image.URLPatterns) {
			return TypeImage
		}
		if d.matchesPattern(lower, d.config.PDF.URLPatterns) {
			return TypePDF
		}
	}

	return TypeUnknown
}

func (d *TypeDetector) GetDefaultOpener() string {
	platform := runtime.GOOS
	if platformConfig, ok := d.config.Platforms[platform]; ok {
		return platformConfig.DefaultOpener
	}
	// Fallback
	if fallback, ok := d.config.Platforms["fallback"]; ok {
		return fallback.DefaultOpener
	}
	return "open"
}

func (d *TypeDetector) hasExtension(extensions []string, ext string) bool {
	for _, e := range extensions {
		if e == ext {
			return true
		}
	}
	return false
}

func (d *TypeDetector) matchesPattern(url string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}
