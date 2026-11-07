package debuglog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDebugLogStripsTerminalControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Setup(LevelDebug, path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatal("existing debug log is not private")
	}
	Infof("feed: %s", "\x1b]52;c;payload\aNews")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(content), "\x1b\a") || !strings.Contains(string(content), "News") {
		t.Fatal("terminal control string persisted to logs")
	}
}

func TestDebugLogReconfigurationIsConcurrentSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	if err := Setup(LevelDebug, path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })
	fields := map[string]any{"feed": "original"}
	fieldLog := WithFields(fields)
	fields["feed"] = "changed"
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 30 {
				fieldLog.Infof("working")
				SetLevel(LevelDebug)
				_ = GetLevel()
			}
		})
	}
	workers.Go(func() {
		for range 10 {
			_ = Close()
			if err := Setup(LevelDebug, path); err != nil {
				t.Error(err)
				return
			}
		}
	})
	workers.Wait()
	if fieldLog.fields["feed"] != "original" {
		t.Fatal("logger retained caller-owned mutable field map")
	}
}
