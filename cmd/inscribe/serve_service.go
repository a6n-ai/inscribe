package main

import (
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/a6n-ai/inscribe/internal/config"
)

// The dashboard runs as a per-user background service so it is up for every
// session: a launchd agent on macOS, a systemd user unit on Linux.
const serviceLabel = "ai.a6n.inscribe.serve"

type service struct {
	path    string // the plist or unit file
	content string
	start   [][]string // commands run after writing, in order
	stop    [][]string // commands run before removing; failures ignored
	restart []string
}

func newService(bin, addr, home string) (service, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return service{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(dir, "Library", "LaunchAgents", serviceLabel+".plist")
		domain := "gui/" + strconv.Itoa(os.Getuid())
		return service{
			path:    path,
			content: launchdPlist(bin, addr, home),
			start:   [][]string{{"launchctl", "bootout", domain + "/" + serviceLabel}, {"launchctl", "bootstrap", domain, path}},
			stop:    [][]string{{"launchctl", "bootout", domain + "/" + serviceLabel}},
			restart: []string{"launchctl", "kickstart", "-k", domain + "/" + serviceLabel},
		}, nil
	case "linux":
		return service{
			path:    filepath.Join(dir, ".config", "systemd", "user", "inscribe-serve.service"),
			content: systemdUnit(bin, addr, home),
			start:   [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", "inscribe-serve.service"}, {"systemctl", "--user", "restart", "inscribe-serve.service"}},
			stop:    [][]string{{"systemctl", "--user", "disable", "--now", "inscribe-serve.service"}},
			restart: []string{"systemctl", "--user", "restart", "inscribe-serve.service"},
		}, nil
	}
	return service{}, fmt.Errorf("serve install supports macOS and Linux, not %s; run inscribe serve yourself", runtime.GOOS)
}

// launchdPlist restarts serve only when it fails: a clean exit means another
// dashboard already holds the port, and relaunching would loop.
func launchdPlist(bin, addr, home string) string {
	x := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array><string>` + x(bin) + `</string><string>serve</string><string>--addr</string><string>` + x(addr) + `</string></array>
	<key>EnvironmentVariables</key>
	<dict><key>INSCRIBE_HOME</key><string>` + x(home) + `</string></dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>ThrottleInterval</key><integer>30</integer>
	<key>StandardOutPath</key><string>` + x(filepath.Join(home, "serve.log")) + `</string>
	<key>StandardErrorPath</key><string>` + x(filepath.Join(home, "serve.log")) + `</string>
</dict>
</plist>
`
}

func systemdUnit(bin, addr, home string) string {
	q := strconv.Quote
	return `[Unit]
Description=Inscribe dashboard

[Service]
ExecStart=` + q(bin) + ` serve --addr ` + q(addr) + `
Environment=` + q("INSCRIBE_HOME="+home) + `
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`
}

// serviceBinary is the path the service runs. The symlink is kept, not
// resolved, so a Homebrew upgrade (which moves the real file) still works.
func serviceBinary() (string, error) {
	if p, err := exec.LookPath("inscribe"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
	}
	return os.Executable()
}

func serviceCmd(action, addr string, stdout io.Writer) error {
	if _, _, err := loopbackPort(addr); err != nil {
		return err
	}
	bin, err := serviceBinary()
	if err != nil {
		return err
	}
	home, err := filepath.Abs(config.Home())
	if err != nil {
		return err
	}
	svc, err := newService(bin, addr, home)
	if err != nil {
		if action == "uninstall" {
			return nil // nothing can be installed on this OS
		}
		return err
	}
	switch action {
	case "install":
		if err := os.MkdirAll(filepath.Dir(svc.path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(svc.path, []byte(svc.content), 0o644); err != nil { //nolint:gosec // G306: launchd and systemd read user agents at 0644
			return err
		}
		for i, c := range svc.start {
			err := runQuiet(c)
			if err != nil && (runtime.GOOS != "darwin" || i > 0) { // a bootout of a job not loaded fails; that is fine
				return fmt.Errorf("%s: %w", strings.Join(c, " "), err)
			}
		}
		return say(stdout, "inscribe serve now runs in the background at http://%s and starts at login (%s)", addr, svc.path)
	case "uninstall":
		return uninstallService(svc, stdout)
	}
	return fmt.Errorf("unknown serve action %q; use install or uninstall", action)
}

func uninstallService(svc service, stdout io.Writer) error {
	if _, err := os.Stat(svc.path); errors.Is(err, os.ErrNotExist) {
		return say(stdout, "no background dashboard installed")
	}
	for _, c := range svc.stop {
		_ = runQuiet(c)
	}
	if err := os.Remove(svc.path); err != nil {
		return err
	}
	return say(stdout, "removed the background dashboard (%s)", svc.path)
}

// restartService restarts an installed background dashboard so it runs the
// binary update just wrote. With none installed it does nothing.
func restartService(stdout io.Writer) error {
	bin, err := serviceBinary()
	if err != nil {
		return err
	}
	svc, err := newService(bin, defaultServeAddr, config.Home())
	if err != nil {
		return nil
	}
	if _, err := os.Stat(svc.path); err != nil {
		return nil
	}
	if err := runQuiet(svc.restart); err != nil {
		return fmt.Errorf("restart the background dashboard: %w", err)
	}
	return say(stdout, "restarted the background dashboard")
}

func runQuiet(c []string) error {
	out, err := exec.Command(c[0], c[1:]...).CombinedOutput() //nolint:gosec // G204: fixed service commands
	if err != nil && len(out) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return err
}
