//go:build darwin

package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
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
		return path, fmt.Errorf("launchctl not found; load manually: launchctl load -w %q", path)
	}
	// Unload first so a re-install picks up changes; ignore the error when it
	// was not previously loaded.
	_ = run("launchctl", "unload", "-w", path)
	if err := run("launchctl", "load", "-w", path); err != nil {
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
		_ = run("launchctl", "unload", "-w", path)
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

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
