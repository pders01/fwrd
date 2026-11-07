//go:build darwin

package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const label = "com.fwrd.serve"

// plistTmpl renders the LaunchAgent. text/template does not escape, so the
// xml func guards every interpolated value — a binary path or label with an
// XML metacharacter can't break the document.
var plistTmpl = template.Must(template.New("plist").
	Funcs(template.FuncMap{"xml": xmlEscape}).
	Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{xml .Label}}</string>
  <key>ProgramArguments</key>
  <array>
{{- range .Args}}
    <string>{{xml .}}</string>
{{- end}}
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>{{xml .OutLog}}</string>
  <key>StandardErrorPath</key><string>{{xml .ErrLog}}</string>
</dict>
</plist>
`))

// xmlEscape escapes a value for inclusion in an XML text node.
func xmlEscape(s string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// plistPath returns ~/Library/LaunchAgents/com.fwrd.serve.plist.
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// Install writes the LaunchAgent plist and loads it. RunAtLoad + KeepAlive
// make it start now and restart on exit/login.
func Install(o *Options) (string, error) {
	prepared, prepErr := prepareOptions(o)
	if prepErr != nil {
		return "", prepErr
	}
	o = prepared
	path, err := plistPath()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	dir, err := logDir()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating log dir: %w", err)
	}
	content, err := plistContent(o, dir)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("writing plist: %w", err)
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return path, fmt.Errorf("launchctl not found; load manually: launchctl bootstrap gui/%d %q", os.Getuid(), path)
	}
	if err := reloadLaunchAgent(path, os.Getuid(), runLaunchctl, time.Sleep); err != nil {
		return path, err
	}
	return path, nil
}

// Uninstall unloads the agent and removes the plist.
func Uninstall() (string, error) {
	path, err := plistPath()
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("launchctl"); err == nil {
		target := launchTarget(os.Getuid())
		_, _ = runLaunchctl("bootout", target)
		_, _ = runLaunchctl("disable", target)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, fmt.Errorf("removing plist: %w", err)
	}
	return path, nil
}

// logDir is where the agent's stdout/stderr logs go (~/.fwrd, shared with
// the debug log).
func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home dir: %w", err)
	}
	return filepath.Join(home, ".fwrd"), nil
}

// LogCommand returns the command that streams the LaunchAgent's stdout/stderr
// logs (the agent redirects both to files under ~/.fwrd). follow maps to
// `tail -f`, lines to `-n`.
func LogCommand(follow bool, lines int) (name string, args []string, err error) {
	dir, err := logDir()
	if err != nil {
		return "", nil, err
	}
	args = []string{"-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, filepath.Join(dir, "serve.out.log"), filepath.Join(dir, "serve.err.log"))
	return "tail", args, nil
}

// plistContent renders the LaunchAgent. It is pure: the caller resolves and
// creates logDir. Program arguments and paths are XML-escaped by the
// template's xml func so a value with reserved characters can't break the
// document.
func plistContent(o *Options, logDir string) ([]byte, error) {
	data := struct {
		Label, OutLog, ErrLog string
		Args                  []string
	}{
		Label:  label,
		OutLog: filepath.Join(logDir, "serve.out.log"),
		ErrLog: filepath.Join(logDir, "serve.err.log"),
		Args:   append([]string{o.BinPath}, serveArgs(o)...),
	}
	var b bytes.Buffer
	if err := plistTmpl.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("rendering plist: %w", err)
	}
	return b.Bytes(), nil
}

type launchctlRunner func(args ...string) ([]byte, error)

type sleepFunc func(time.Duration)

func launchDomain(uid int) string { return fmt.Sprintf("gui/%d", uid) }

func launchTarget(uid int) string { return launchDomain(uid) + "/" + label }

func reloadLaunchAgent(path string, uid int, runner launchctlRunner, sleep sleepFunc) error {
	domain, target := launchDomain(uid), launchTarget(uid)

	out, err := runner("bootout", target)
	switch {
	case err == nil:
		// bootout can return before launchd has fully removed a busy job. Wait
		// until print confirms removal before registering its replacement.
		for attempt := 0; attempt < 100; attempt++ {
			out, err = runner("print", target)
			if err != nil && launchctlJobMissing(out) {
				break
			}
			if err != nil {
				return fmt.Errorf("checking launch agent removal: %w", err)
			}
			if attempt == 99 {
				return errors.New("timed out waiting for launch agent removal")
			}
			sleep(50 * time.Millisecond)
		}
	case launchctlJobMissing(out):
		// First install: there is no old job to remove.
	default:
		return fmt.Errorf("stopping existing launch agent: %w", err)
	}

	if _, err = runner("enable", target); err != nil {
		return fmt.Errorf("enabling launch agent: %w", err)
	}

	// launchd occasionally reports EIO while finalizing a preceding bootout.
	// Retry only that transient result, and always verify registration below.
	for attempt := 0; ; attempt++ {
		out, err = runner("bootstrap", domain, path)
		if err == nil {
			break
		}
		if !launchctlTransient(out) || attempt == 49 {
			return fmt.Errorf("starting launch agent: %w", err)
		}
		sleep(100 * time.Millisecond)
	}

	if _, err = runner("print", target); err != nil {
		return fmt.Errorf("verifying launch agent: %w", err)
	}
	return nil
}

func launchctlJobMissing(out []byte) bool {
	message := strings.ToLower(string(out))
	return strings.Contains(message, "could not find service") ||
		strings.Contains(message, "no such process")
}

func launchctlTransient(out []byte) bool {
	return strings.Contains(strings.ToLower(string(out)), "input/output error")
}

func runLaunchctl(args ...string) ([]byte, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
