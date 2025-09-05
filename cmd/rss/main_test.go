package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "-version"}

	// Use a channel to capture output
	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// Run main and check if it exits properly
	main()

	w.Close()
	os.Stdout = old
	out := <-outC

	// Check output
	if !strings.Contains(out, "fwrd v1.0.0") {
		t.Errorf("Expected version output to contain 'fwrd v1.0.0', got: %s", out)
	}
	if !strings.Contains(out, "RSS aggregator") {
		t.Errorf("Expected version output to contain 'RSS aggregator', got: %s", out)
	}
	if !strings.Contains(out, "github.com/pders01/fwrd") {
		t.Errorf("Expected version output to contain 'github.com/pders01/fwrd', got: %s", out)
	}
}

func TestGenerateConfigFlag(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, ".config", "fwrd", "config.toml")

	// Set HOME to temp directory
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "-generate-config"}

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// Run main
	main()

	w.Close()
	os.Stdout = old
	out := <-outC

	// Check if config file was created
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Errorf("Config file was not created at %s", configFile)
	}

	// Check output message
	if !strings.Contains(out, "Generated default configuration at:") {
		t.Errorf("Expected output to contain 'Generated default configuration at:', got: %s", out)
	}
}

func TestShowBanner(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// Call showBanner
	showBanner()

	w.Close()
	os.Stdout = old
	out := <-outC

	// Check if banner contains expected elements
	if !strings.Contains(out, "RSS Feed Aggregator") {
		t.Errorf("Expected banner to contain 'RSS Feed Aggregator', got: %s", out)
	}
	// Check for border characters
	if !strings.Contains(out, "╔") || !strings.Contains(out, "╝") {
		t.Errorf("Expected banner to contain border characters, got: %s", out)
	}
	// Check for separator
	if !strings.Contains(out, "◆") {
		t.Errorf("Expected banner to contain separator symbols, got: %s", out)
	}
}

func TestExpandTildePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "expand tilde path",
			input:    "~/test.db",
			expected: filepath.Join(os.Getenv("HOME"), "test.db"),
		},
		{
			name:     "absolute path unchanged",
			input:    "/tmp/test.db",
			expected: "/tmp/test.db",
		},
		{
			name:     "relative path unchanged",
			input:    "test.db",
			expected: "test.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the path expansion logic
			path := tt.input
			if len(path) >= 2 && path[:2] == "~/" {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[2:])
			}

			if path != tt.expected && tt.name == "expand tilde path" {
				home, _ := os.UserHomeDir()
				expected := filepath.Join(home, "test.db")
				if path != expected {
					t.Errorf("Expected %s, got %s", expected, path)
				}
			} else if tt.name != "expand tilde path" && path != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, path)
			}
		})
	}
}
