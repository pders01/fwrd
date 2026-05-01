package lua

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed builtin/*.lua
var builtinFS embed.FS

// EnsureDefaults seeds dir with the bundled Lua plugins on first run.
//
// "First run" means dir does not yet exist. If dir is already present
// the function is a no-op, even if it is empty: a user who deletes a
// shipped plugin must not have it re-created on the next startup.
//
// Returns nil when dir is "" (no plugin directory configured) so the
// caller can chain it before LoadAndRegister without a pre-flight check.
func EnsureDefaults(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat plugin dir: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating plugin dir: %w", err)
	}

	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return fmt.Errorf("reading embedded plugins: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(builtinFS, filepath.ToSlash(filepath.Join("builtin", e.Name())))
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", e.Name(), err)
		}
		dest := filepath.Join(dir, e.Name())
		if err := writeIfAbsent(dest, data); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// writeIfAbsent atomically creates dest with data and 0o644 perms,
// failing silently if a file already exists at the path. The O_EXCL
// flag prevents a TOCTOU race when two fwrd processes both observe an
// empty plugin dir at startup and both try to seed the same file.
func writeIfAbsent(dest string, data []byte) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return werr
	}
	return f.Close()
}
