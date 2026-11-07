package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func TestFeedListingStripsUntrustedTerminalControls(t *testing.T) {
	dir := t.TempDir()
	cfg := config.TestConfig()
	cfg.Database.Path = filepath.Join(dir, "news.db")
	store, err := storage.NewStore(cfg.Database.Path)
	if err != nil {
		t.Fatal(err)
	}
	if saveErr := store.SaveFeed(&storage.Feed{ID: "feed", URL: "https://example.com/feed", Title: "\x1b]52;c;payload\aNews"}); saveErr != nil {
		t.Fatal(saveErr)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	path := filepath.Join(dir, "config.toml")
	if saveErr := config.Save(cfg, path); saveErr != nil {
		t.Fatal(saveErr)
	}
	previousConfig, previousDB := cfgFile, dbPath
	cfgFile, dbPath = path, ""
	t.Cleanup(func() { cfgFile, dbPath = previousConfig, previousDB })
	previousOutput := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = previousOutput; _ = writer.Close() })
	done := make(chan string, 1)
	go func() { var output bytes.Buffer; _, _ = io.Copy(&output, reader); done <- output.String() }()
	listFeeds(nil, nil)
	_ = writer.Close()
	os.Stdout = previousOutput
	output := <-done
	if strings.ContainsAny(output, "\x1b\a") || !strings.Contains(output, "News") {
		t.Fatal("feed listing emitted untrusted terminal controls")
	}
}

func TestServeExposureRequiresExplicitLANConsent(t *testing.T) {
	cfg := &config.Config{}
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if err := validateServeExposure(address, cfg, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"0.0.0.0:8080", "[::]:8080", ":8080", "192.168.1.2:8080"} {
		if err := validateServeExposure(address, cfg, false); err == nil {
			t.Fatalf("unauthenticated bind %s accepted", address)
		}
		if err := validateServeExposure(address, cfg, true); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Web.Auth = config.WebAuthConfig{Username: "reader", Password: "secret"}
	if err := validateServeExposure("0.0.0.0:8080", cfg, false); err != nil {
		t.Fatal(err)
	}
	cfg.Web.Auth.Password = ""
	if err := validateServeExposure("127.0.0.1:8080", cfg, true); err == nil {
		t.Fatal("partial authentication silently accepted")
	}
}

func TestServiceDefaultsArePrivate(t *testing.T) {
	addr := serviceInstallCmd.Flags().Lookup("addr")
	mdns := serviceInstallCmd.Flags().Lookup("mdns")
	if addr == nil || addr.DefValue != "127.0.0.1:8080" || mdns == nil || mdns.DefValue != "false" {
		t.Fatal("service exposes feeds by default")
	}
}

func TestCustomDatabaseIndexDerivation(t *testing.T) {
	if got := deriveIndexPath("/tmp/independent.db"); got != "/tmp/independent.bleve" {
		t.Fatalf("index = %q", got)
	}
	if got := deriveIndexPath(":memory:"); got != "" {
		t.Fatalf("memory store shares persistent index %q", got)
	}
}
