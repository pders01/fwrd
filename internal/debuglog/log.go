package debuglog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
		case LevelDebug: return "DEBUG"
		case LevelInfo:  return "INFO"
		case LevelWarn:  return "WARN"
		case LevelError: return "ERROR"
		case LevelOff:   return "OFF"
		default:         return "UNKNOWN"
	}
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(s string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
		case "DEBUG": return LevelDebug
		case "INFO":  return LevelInfo
		case "WARN", "WARNING": return LevelWarn
		case "ERROR": return LevelError
		case "OFF":   return LevelOff
		default:      return LevelInfo // Default to INFO
	}
}

var (
	currentLevel LogLevel = LevelOff
	logger       *log.Logger
	logFile      *os.File
)

// Setup configures the logging system with the specified level and optional file path.
// If filePath is empty, defaults to ~/.fwrd/fwrd.log.
func Setup(level LogLevel, filePath ...string) error {
	currentLevel = level
	
	// Close existing log file if open
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	
	if level == LevelOff {
		logger = nil
		return nil
	}
	
	// Determine log file path
	var logPath string
	if len(filePath) > 0 && filePath[0] != "" {
		logPath = filePath[0]
	} else {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".fwrd")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}
		logPath = filepath.Join(dir, "fwrd.log")
	}
	
	// Open log file
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}
	
	logFile = f
	logger = log.New(f, "fwrd ", log.LstdFlags|log.Lmicroseconds)
	return nil
}

// SetupWithBool provides backward compatibility with the old Setup(bool) signature
func SetupWithBool(enabled bool) {
	if enabled {
		Setup(LevelInfo)
	} else {
		Setup(LevelOff)
	}
}

// SetLevel changes the current logging level
func SetLevel(level LogLevel) {
	currentLevel = level
}

// GetLevel returns the current logging level
func GetLevel() LogLevel {
	return currentLevel
}

// Close closes the log file if open
func Close() error {
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
	if level < currentLevel || logger == nil {
		return
	}
	
	message := fmt.Sprintf(format, args...)
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
	fields map[string]interface{}
}

// WithFields returns a new logger with the specified fields
func WithFields(fields map[string]interface{}) *FieldLogger {
	return &FieldLogger{fields: fields}
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
	if LevelDebug >= currentLevel && logger != nil {
		message := fmt.Sprintf(format, args...) + fl.formatFields()
		logf(LevelDebug, "%s", message)
	}
}

func (fl *FieldLogger) Infof(format string, args ...any) {
	if LevelInfo >= currentLevel && logger != nil {
		message := fmt.Sprintf(format, args...) + fl.formatFields()
		logf(LevelInfo, "%s", message)
	}
}

func (fl *FieldLogger) Warnf(format string, args ...any) {
	if LevelWarn >= currentLevel && logger != nil {
		message := fmt.Sprintf(format, args...) + fl.formatFields()
		logf(LevelWarn, "%s", message)
	}
}

func (fl *FieldLogger) Errorf(format string, args ...any) {
	if LevelError >= currentLevel && logger != nil {
		message := fmt.Sprintf(format, args...) + fl.formatFields()
		logf(LevelError, "%s", message)
	}
}
