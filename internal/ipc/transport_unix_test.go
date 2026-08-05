//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"testing"
)

func testEndpoint(t *testing.T, name string) Endpoint {
	t.Helper()
	path, err := CanonicalDBPath(filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewEndpoint(path)
}

func TestUnixEndpointIsShortAndPrivate(t *testing.T) {
	endpoint := testEndpoint(t, "private")
	if len(endpoint.Address) >= 100 {
		t.Fatalf("socket path is too long for supported Unix platforms: %q", endpoint.Address)
	}
	listener, err := ListenEndpoint(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
		_ = RemoveEndpoint(endpoint.Address)
	}()

	dirInfo, err := os.Stat(RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("run dir mode = %o, want 700", got)
	}
	socketInfo, err := os.Lstat(endpoint.Address)
	if err != nil {
		t.Fatal(err)
	}
	if got := socketInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}
}

func TestListenEndpointRefusesRegularFile(t *testing.T) {
	endpoint := testEndpoint(t, "regular")
	if err := ensurePrivateRunDir(RunDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(endpoint.Address, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(endpoint.Address)
	if _, err := ListenEndpoint(endpoint); err == nil {
		t.Fatal("ListenEndpoint removed a regular file")
	}
	if data, err := os.ReadFile(endpoint.Address); err != nil || string(data) != "keep" {
		t.Fatalf("regular file changed: data=%q err=%v", data, err)
	}
}
