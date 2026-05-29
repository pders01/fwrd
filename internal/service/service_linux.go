//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const unitName = "fwrd.service"

// unitTmpl renders the systemd user unit. ExecStart is the only dynamic field.
var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=fwrd RSS web view
Documentation=https://github.com/pders01/fwrd
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.Exec}}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`))

// unitPath returns ~/.config/systemd/user/fwrd.service.
func unitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "systemd", "user", unitName), nil
}

// Install writes the systemd user unit, reloads the user daemon, and enables
// + starts the service. It returns the unit path. systemctl failures after
// the file is written are returned with the path so the caller can surface a
// manual-enable hint.
func Install(o *Options) (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating unit directory: %w", err)
	}
	content, err := unitContent(o)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing unit: %w", err)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return path, fmt.Errorf("systemctl not found; enable manually: systemctl --user enable --now %s", unitName)
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return path, err
	}
	if err := run("systemctl", "--user", "enable", "--now", unitName); err != nil {
		return path, err
	}
	return path, nil
}

// Uninstall stops and disables the service, then removes the unit file.
func Uninstall() (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		// Best-effort: ignore errors so a partially-installed service can
		// still be cleaned up.
		_ = run("systemctl", "--user", "disable", "--now", unitName)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, fmt.Errorf("removing unit: %w", err)
	}
	return path, nil
}

func unitContent(o *Options) (string, error) {
	data := struct{ Exec string }{o.BinPath + " " + strings.Join(serveArgs(o), " ")}
	var b strings.Builder
	if err := unitTmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering unit: %w", err)
	}
	return b.String(), nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
