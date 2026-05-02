package config

import (
	"strings"
	"testing"
)

func TestWarnings_FlagsCtrlMCollision(t *testing.T) {
	cfg := &Config{}
	cfg.Keys.Modifier = "ctrl"
	cfg.Keys.Bindings.ToggleRead = "m"

	got := Warnings(cfg)
	if len(got) == 0 {
		t.Fatal("expected a warning for ctrl+m collision, got none")
	}
	found := false
	for _, w := range got {
		if strings.Contains(w, "toggle_read") && strings.Contains(w, "ctrl+m") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected toggle_read/ctrl+m warning, got: %v", got)
	}
}

func TestWarnings_FlagsDuplicateBindings(t *testing.T) {
	cfg := &Config{}
	cfg.Keys.Modifier = "ctrl"
	cfg.Keys.Bindings.NewFeed = "x"
	cfg.Keys.Bindings.DeleteFeed = "x"

	got := Warnings(cfg)
	found := false
	for _, w := range got {
		if strings.Contains(w, "ctrl+x") && strings.Contains(w, "new_feed") && strings.Contains(w, "delete_feed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate binding warning, got: %v", got)
	}
}

func TestWarnings_BackUsesLiteralKey(t *testing.T) {
	cfg := &Config{}
	cfg.Keys.Modifier = "ctrl"
	cfg.Keys.Bindings.Back = "esc"

	got := Warnings(cfg)
	for _, w := range got {
		if strings.Contains(w, "back") {
			t.Fatalf("back=esc must not warn (it is bound to a literal key, not modifier+key): %v", got)
		}
	}
}

func TestWarnings_CleanConfigSilent(t *testing.T) {
	cfg := defaultConfig()
	if got := Warnings(cfg); len(got) != 0 {
		t.Fatalf("default config should produce no warnings, got: %v", got)
	}
}
