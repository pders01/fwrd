package debuglog

import (
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pders01/fwrd/internal/terminaltext"
)

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelOff // Disables all logging
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelOff:
		return "OFF"
	default:
		return "UNKNOWN"
	}
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(s string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug
	case "INFO":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "OFF":
		return LevelOff
	default:
		return LevelInfo // Default to INFO
	}
}

var (
	stateMu      sync.RWMutex
	currentLevel = LevelOff
	logger       *log.Logger
	logFile      *os.File
)

// Setup configures the logging system with the specified level and optional file path.
// If filePath is empty, defaults to ~/.fwrd/fwrd.log.
func Setup(level LogLevel, filePath ...string) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	currentLevel = level

	// Close existing log file if open
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}

	logger = nil
	if level == LevelOff {
		return nil
	}

	// Determine log file path
	var logPath string
	if len(filePath) > 0 && filePath[0] != "" {
		logPath = filePath[0]
	} else {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".fwrd")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}
		logPath = filepath.Join(dir, "fwrd.log")
	}

	// Open log file
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	if chmodErr := f.Chmod(0o600); chmodErr != nil {
		_ = f.Close()
		return fmt.Errorf("protect log file: %w", chmodErr)
	}
	logFile = f
	logger = log.New(f, "fwrd ", log.LstdFlags|log.Lmicroseconds)
	return nil
}

// DefaultPath returns the default debug-log file location, ~/.fwrd/fwrd.log.
// It does not create the file. Used by `fwrd logs` to tail the app's own log.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fwrd", "fwrd.log"), nil
}

// SetupWithBool provides backward compatibility with the old Setup(bool) signature
func SetupWithBool(enabled bool) {
	if enabled {
		_ = Setup(LevelInfo)
	} else {
		_ = Setup(LevelOff)
	}
}

// SetLevel changes the current logging level
func SetLevel(level LogLevel) {
	stateMu.Lock()
	defer stateMu.Unlock()
	currentLevel = level
}

// GetLevel returns the current logging level
func GetLevel() LogLevel {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return currentLevel
}

// Close closes the log file if open
func Close() error {
	stateMu.Lock()
	defer stateMu.Unlock()
	if logFile != nil {
		err := logFile.Close()
		logFile = nil
		logger = nil
		return err
	}
	return nil
}

// logf writes a log message at the specified level
func logf(level LogLevel, format string, args ...any) {
	stateMu.RLock()
	defer stateMu.RUnlock()
	if level < currentLevel || logger == nil {
		return
	}

	message := terminaltext.Safe(fmt.Sprintf(format, args...))
	logger.Printf("[%s] %s", level.String(), message)
}

// Structured logging functions

func Debugf(format string, args ...any) {
	logf(LevelDebug, format, args...)
}

func Infof(format string, args ...any) {
	logf(LevelInfo, format, args...)
}

func Warnf(format string, args ...any) {
	logf(LevelWarn, format, args...)
}

func Errorf(format string, args ...any) {
	logf(LevelError, format, args...)
}

// WithFields creates a logger with structured fields (basic key-value support)
type FieldLogger struct {
	fields map[string]any
}

// WithFields returns a new logger with the specified fields
func WithFields(fields map[string]any) *FieldLogger {
	return &FieldLogger{fields: maps.Clone(fields)}
}

// formatFields converts fields to a string representation
func (fl *FieldLogger) formatFields() string {
	if len(fl.fields) == 0 {
		return ""
	}

	var parts []string
	for key, value := range fl.fields {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	return " [" + strings.Join(parts, " ") + "]"
}

func (fl *FieldLogger) Debugf(format string, args ...any) {
	logf(LevelDebug, "%s", fmt.Sprintf(format, args...)+fl.formatFields())
}

func (fl *FieldLogger) Infof(format string, args ...any) {
	logf(LevelInfo, "%s", fmt.Sprintf(format, args...)+fl.formatFields())
}

func (fl *FieldLogger) Warnf(format string, args ...any) {
	logf(LevelWarn, "%s", fmt.Sprintf(format, args...)+fl.formatFields())
}

func (fl *FieldLogger) Errorf(format string, args ...any) {
	logf(LevelError, "%s", fmt.Sprintf(format, args...)+fl.formatFields())
}
