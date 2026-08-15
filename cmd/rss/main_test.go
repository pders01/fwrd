package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/editorial"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

func TestPluginsListCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	listPlugins(nil, nil)
	w.Close()
	out := <-outC

	if !strings.Contains(out, "reddit") {
		t.Errorf("expected reddit plugin in output, got: %s", out)
	}
	if !strings.Contains(out, "youtube") {
		t.Errorf("expected youtube plugin in output, got: %s", out)
	}
	if !strings.Contains(out, filepath.Join(tmp, "fwrd", "plugins")) {
		t.Errorf("expected plugin dir path in output, got: %s", out)
	}
}

func TestVersionCommand(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Set args for version command
	originalArgs := os.Args
	os.Args = []string{"fwrd", "version"}
	defer func() { os.Args = originalArgs }()

	// Use a channel to capture output
	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// Execute version command directly
	versionCmd.Run(nil, nil)

	w.Close()
	os.Stdout = old
	out := <-outC

	// Check output - Version is "dev" by default in tests
	if !strings.Contains(out, "fwrd dev") {
		t.Errorf("Expected version output to contain 'fwrd dev', got: %s", out)
	}
	if !strings.Contains(out, "RSS aggregator") {
		t.Errorf("Expected version output to contain 'RSS aggregator', got: %s", out)
	}
	if !strings.Contains(out, "github.com/pders01/fwrd") {
		t.Errorf("Expected version output to contain 'github.com/pders01/fwrd', got: %s", out)
	}
}

func TestGenerateConfigCommand(t *testing.T) {
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

	// Set args for config generate command
	originalArgs := os.Args
	os.Args = []string{"fwrd", "config", "generate"}
	defer func() { os.Args = originalArgs }()

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// Execute config generate command directly
	configGenCmd.Run(nil, nil)

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

func TestAssignFeedDesk(t *testing.T) {
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	feed := &storage.Feed{ID: "feed-1", URL: "https://news.example/rss", Title: "News"}
	if saveErr := store.SaveFeed(feed); saveErr != nil {
		t.Fatal(saveErr)
	}

	got, desk, err := assignFeedDesk(store, feed.URL, editorial.DeskWorld)
	if err != nil {
		t.Fatal(err)
	}
	if desk.ID != editorial.DeskWorld || got.DeskID != editorial.DeskWorld {
		t.Fatalf("assignment = feed %+v, desk %+v", got, desk)
	}
	persisted, err := store.GetFeed(feed.ID)
	if err != nil || persisted.DeskID != editorial.DeskWorld {
		t.Fatalf("persisted feed = %+v, %v", persisted, err)
	}
	if _, _, err := assignFeedDesk(store, feed.ID, "made-up"); err == nil || !strings.Contains(err.Error(), editorial.DeskTechnology) {
		t.Fatalf("invalid desk error = %v; want valid choices", err)
	}
}

func TestSecureDBPathCreatesParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "fwrd.db")
	got, err := secureDBPath(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ipc.CanonicalDBPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("secureDBPath = %q, want %q", got, want)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Fatalf("parent directory was not created: info=%v err=%v", info, err)
	}
}

func TestSecureDBPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := secureDBPath(filepath.Join(link, "escaped.db")); err == nil {
		t.Fatal("secureDBPath accepted a symlink escaping allowed roots")
	}
}

func TestSecureDBPathUsesCanonicalAlias(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := secureDBPath(filepath.Join(link, "fwrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := secureDBPath(filepath.Join(target, "fwrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("alias path = %q, direct path = %q", got, want)
	}
}

func TestSecureDBPathPreservesMemoryPath(t *testing.T) {
	got, err := secureDBPath(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != storage.MemoryPath {
		t.Fatalf("secureDBPath = %q, want MemoryPath", got)
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

func TestIsVersionedPkgPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/Caskroom/fwrd/1.65.0+ci.1/fwrd", true},
		{"/usr/local/Cellar/fwrd/1.65.0/bin/fwrd", true},
		{"/home/linuxbrew/.linuxbrew/Cellar/fwrd/1.0/bin/fwrd", true},
		{"/opt/homebrew/bin/fwrd", false},
		{"/usr/local/bin/fwrd", false},
		{"/Users/me/go/bin/fwrd", false},
		{"fwrd", false},
		// "Cellar"/"Caskroom" only match a whole path segment, not a substring.
		{"/Users/me/Caskroomish/fwrd", false},
	}
	for _, c := range cases {
		if got := isVersionedPkgPath(c.path); got != c.want {
			t.Errorf("isVersionedPkgPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestNetPorts(t *testing.T) {
	cases := []struct {
		name  string
		port  int
		https bool
		want  []int
	}{
		{"https adds 443", 80, true, []int{80, 443}},
		{"no https keeps base only", 80, false, []int{80}},
		{"https dedups when base is 443", 443, true, []int{443}},
		{"custom base with https", 8080, true, []int{8080, 443}},
		{"custom base no https", 8080, false, []int{8080}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := netPorts(c.port, c.https)
			if !slices.Equal(got, c.want) {
				t.Errorf("netPorts(%d, %v) = %v, want %v", c.port, c.https, got, c.want)
			}
		})
	}
}
