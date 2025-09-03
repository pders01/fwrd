package media

import (
	_ "embed"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed media_types.toml
var mediaTypesTOML []byte

type MediaTypeConfig struct {
	Extensions  []string `toml:"extensions"`
	URLPatterns []string `toml:"url_patterns"`
}

type MediaTypesConfig struct {
	Video     MediaTypeConfig           `toml:"video"`
	Audio     MediaTypeConfig           `toml:"audio"`
	Image     MediaTypeConfig           `toml:"image"`
	PDF       MediaTypeConfig           `toml:"pdf"`
	Platforms map[string]PlatformConfig `toml:"platforms"`
}

type PlatformConfig struct {
	DefaultOpener string `toml:"default_opener"`
}

type MediaTypeDetector struct {
	config *MediaTypesConfig
}

func NewMediaTypeDetector() (*MediaTypeDetector, error) {
	var config MediaTypesConfig
	if err := toml.Unmarshal(mediaTypesTOML, &config); err != nil {
		return nil, err
	}

	return &MediaTypeDetector{config: &config}, nil
}

func (d *MediaTypeDetector) DetectType(url string) MediaType {
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
			return MediaTypeVideo
		}
		if d.hasExtension(d.config.Audio.Extensions, ext) {
			return MediaTypeAudio
		}
		if d.hasExtension(d.config.Image.Extensions, ext) {
			return MediaTypeImage
		}
		if d.hasExtension(d.config.PDF.Extensions, ext) {
			return MediaTypePDF
		}
	}

	// Check URL patterns
	if isURL {
		if d.matchesPattern(lower, d.config.Video.URLPatterns) {
			return MediaTypeVideo
		}
		if d.matchesPattern(lower, d.config.Audio.URLPatterns) {
			return MediaTypeAudio
		}
		if d.matchesPattern(lower, d.config.Image.URLPatterns) {
			return MediaTypeImage
		}
		if d.matchesPattern(lower, d.config.PDF.URLPatterns) {
			return MediaTypePDF
		}
	}

	return MediaTypeUnknown
}

func (d *MediaTypeDetector) GetDefaultOpener() string {
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

func (d *MediaTypeDetector) hasExtension(extensions []string, ext string) bool {
	for _, e := range extensions {
		if e == ext {
			return true
		}
	}
	return false
}

func (d *MediaTypeDetector) matchesPattern(url string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}
