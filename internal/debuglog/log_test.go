package debuglog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogLevelString(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelOff, "OFF"},
	}

	for _, test := range tests {
		if got := test.level.String(); got != test.expected {
			t.Errorf("LogLevel.String() = %q, want %q", got, test.expected)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"DEBUG", LevelDebug},
		{"debug", LevelDebug},
		{"INFO", LevelInfo},
		{"info", LevelInfo},
		{"WARN", LevelWarn},
		{"warn", LevelWarn},
		{"WARNING", LevelWarn},
		{"ERROR", LevelError},
		{"error", LevelError},
		{"OFF", LevelOff},
		{"off", LevelOff},
		{"INVALID", LevelInfo}, // Default to INFO
		{"", LevelInfo},         // Default to INFO
	}

	for _, test := range tests {
		if got := ParseLogLevel(test.input); got != test.expected {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", test.input, got, test.expected)
		}
	}
}

func TestSetupWithLevel(t *testing.T) {
	// Create temporary log file
	tempDir, err := os.MkdirTemp("", "debuglog_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "test.log")

	// Test setup with INFO level
	err = Setup(LevelInfo, logPath)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	if GetLevel() != LevelInfo {
		t.Errorf("GetLevel() = %v, want %v", GetLevel(), LevelInfo)
	}

	// Test logging at different levels
	Debugf("debug message") // Should not appear
	Infof("info message")   // Should appear
	Warnf("warn message")   // Should appear
	Errorf("error message") // Should appear

	// Close and read log file
	if err := Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	logContent := string(content)
	if strings.Contains(logContent, "debug message") {
		t.Error("DEBUG message should not appear with INFO level")
	}
	if !strings.Contains(logContent, "info message") {
		t.Error("INFO message should appear with INFO level")
	}
	if !strings.Contains(logContent, "warn message") {
		t.Error("WARN message should appear with INFO level")
	}
	if !strings.Contains(logContent, "error message") {
		t.Error("ERROR message should appear with INFO level")
	}
}

func TestSetupWithLevelOff(t *testing.T) {
	// Test setup with OFF level
	err := Setup(LevelOff)
	if err != nil {
		t.Fatalf("Setup with LevelOff failed: %v", err)
	}

	if GetLevel() != LevelOff {
		t.Errorf("GetLevel() = %v, want %v", GetLevel(), LevelOff)
	}

	// All logging should be disabled
	Debugf("debug message")
	Infof("info message")
	Warnf("warn message")
	Errorf("error message")

	// No assertions needed as no log file should be created
}

func TestBackwardCompatibility(t *testing.T) {
	// Test SetupWithBool(true)
	SetupWithBool(true)
	if GetLevel() != LevelInfo {
		t.Errorf("SetupWithBool(true) should set level to INFO, got %v", GetLevel())
	}

	// Test SetupWithBool(false)
	SetupWithBool(false)
	if GetLevel() != LevelOff {
		t.Errorf("SetupWithBool(false) should set level to OFF, got %v", GetLevel())
	}
}

func TestFieldLogger(t *testing.T) {
	// Create temporary log file
	tempDir, err := os.MkdirTemp("", "debuglog_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "field_test.log")

	// Setup with DEBUG level to capture all messages
	err = Setup(LevelDebug, logPath)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer Close()

	// Test field logger
	logger := WithFields(map[string]interface{}{
		"component": "test",
		"action":    "testing",
		"count":     42,
	})

	logger.Infof("test message with fields")

	// Close and read log file
	if err := Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "test message with fields") {
		t.Error("Log message should contain the main message")
	}
	if !strings.Contains(logContent, "component=test") {
		t.Error("Log message should contain structured field component=test")
	}
	if !strings.Contains(logContent, "action=testing") {
		t.Error("Log message should contain structured field action=testing")  
	}
	if !strings.Contains(logContent, "count=42") {
		t.Error("Log message should contain structured field count=42")
	}
}

func TestSetLevel(t *testing.T) {
	// Test changing log level dynamically
	SetLevel(LevelDebug)
	if GetLevel() != LevelDebug {
		t.Errorf("SetLevel(LevelDebug) failed, got %v", GetLevel())
	}

	SetLevel(LevelError)
	if GetLevel() != LevelError {
		t.Errorf("SetLevel(LevelError) failed, got %v", GetLevel())
	}
}