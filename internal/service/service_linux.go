//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
# Give up (unit enters "failed") after 5 starts in 60s so a persistent failure
# such as an in-use port surfaces in "systemctl --user status fwrd" instead of
# restart-looping forever. With RestartSec=5 that's ~20s of retries.
StartLimitIntervalSec=60
StartLimitBurst=5

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

// Install writes the systemd user unit, reloads the user daemon, enables the
// service, and restarts it to apply changed arguments. It returns the unit path.
// systemctl failures after the file is written are returned with the path so
// the caller can surface a manual-enable hint.
func Install(o *Options) (string, error) {
	prepared, prepErr := prepareOptions(o)
	if prepErr != nil {
		return "", prepErr
	}
	o = prepared
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
		return path, fmt.Errorf("systemctl not found; enable manually: systemctl --user daemon-reload && systemctl --user enable %s && systemctl --user restart %s", unitName, unitName)
	}
	return path, activateSystemdUserService(run)
}

// activateSystemdUserService restarts even an already-active unit so reinstall
// applies new arguments. Restart also starts an inactive unit. Inject the runner
// per call so sequencing can be tested without globals or host service actions.
func activateSystemdUserService(runner func(string, ...string) error) error {
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", unitName},
		{"--user", "restart", unitName},
	} {
		if err := runner("systemctl", args...); err != nil {
			return err
		}
	}
	return nil
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

// LogCommand returns the command that streams the systemd user service's logs
// from the journal (the unit logs to journald, not a file). follow maps to
// `-f`, lines to `-n`.
func LogCommand(follow bool, lines int) (name string, args []string, err error) {
	args = []string{"--user", "-u", unitName, "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	return "journalctl", args, nil
}

func unitContent(o *Options) (string, error) {
	args := append([]string{o.BinPath}, serveArgs(o)...)
	for i, arg := range args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return "", fmt.Errorf("service argument contains control characters")
		}
		args[i] = systemdArgument(arg)
	}
	data := struct{ Exec string }{strings.Join(args, " ")}
	var b strings.Builder
	if err := unitTmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering unit: %w", err)
	}
	return b.String(), nil
}

// systemd expands percent specifiers and dollar variables even inside quoted
// ExecStart arguments; escape those independently from token quoting.
func systemdArgument(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\r\n\\\"'$%") {
		return arg
	}
	arg = strings.ReplaceAll(arg, "%", "%%")
	arg = strings.ReplaceAll(arg, "$", "$$")
	return strconv.Quote(arg)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
