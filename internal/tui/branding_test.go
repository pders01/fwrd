package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

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

	// Call ShowBanner with test version
	ShowBanner("1.0.0-test")

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
	// Check for version
	if !strings.Contains(out, "v1.0.0-test") {
		t.Errorf("Expected banner to contain version 'v1.0.0-test', got: %s", out)
	}
}

func TestGetCompactBanner(t *testing.T) {
	message := "Test message"
	result := GetCompactBanner(message)

	// Check that it contains the message
	if !strings.Contains(result, message) {
		t.Errorf("Expected compact banner to contain '%s', got: %s", message, result)
	}

	// Check that it contains logo elements (using one of the logo lines)
	if !strings.Contains(result, "▄████") {
		t.Errorf("Expected compact banner to contain logo elements, got: %s", result)
	}
}

func TestGetWelcomeMessage(t *testing.T) {
	result := GetWelcomeMessage()

	// Check that it contains the welcome text
	if !strings.Contains(result, "Press ctrl+n to add your first feed") {
		t.Errorf("Expected welcome message to contain correct instructions, got: %s", result)
	}

	// Check that it contains logo elements
	if !strings.Contains(result, "▄████") {
		t.Errorf("Expected welcome message to contain logo elements, got: %s", result)
	}
}

func TestLogoConstants(t *testing.T) {
	// Test that LogoLines is properly defined
	if len(LogoLines) != 5 {
		t.Errorf("Expected 5 logo lines, got %d", len(LogoLines))
	}

	// Test that first line contains expected content
	if !strings.Contains(LogoLines[0], "▄████") {
		t.Errorf("Expected first logo line to contain logo elements, got: %s", LogoLines[0])
	}

	// Test that BannerColors is properly defined
	if len(BannerColors) != 5 {
		t.Errorf("Expected 5 banner colors, got %d", len(BannerColors))
	}
}
