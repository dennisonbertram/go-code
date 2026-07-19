package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"go-agent-harness/internal/config"
)

// This file implements "harnesscli service install|uninstall" — user-level OS
// service management for harnessd (launchd on macOS, systemd --user on Linux).
// The generated unit runs harnessd with the same address resolution the daemon
// itself uses (internal/config: default :8080, HARNESS_ADDR override), passed
// through the unit's environment. Lifecycle subcommands (start/stop/status)
// land in the next slice; here they are recognized stubs.

const (
	// serviceLabel is the launchd job label and the reverse-DNS stem of the
	// plist filename.
	serviceLabel = "com.gocode.harnessd"
	// serviceLaunchdFileName is the plist filename under ~/Library/LaunchAgents.
	serviceLaunchdFileName = "com.gocode.harnessd.plist"
	// serviceSystemdUnitName is the systemd --user unit name (and filename
	// under ~/.config/systemd/user).
	serviceSystemdUnitName = "harnessd.service"
)

// servicePlatform selects the service manager backend. It is a var (not
// runtime.GOOS inline) so tests can exercise the Linux path on macOS and vice
// versa.
var servicePlatform = runtime.GOOS

// serviceRunLifecycle executes a lifecycle tool (launchctl/systemctl). It is a
// var so tests can substitute a recording fake — unit tests must never exec
// real service managers.
var serviceRunLifecycle = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	return cmd.Run()
}

// serviceOptions carries the resolved values embedded in a generated unit
// file. All fields must be absolute (or address) values — the renderers are
// pure functions with no I/O.
type serviceOptions struct {
	// BinaryPath is the absolute path to the harnessd executable.
	BinaryPath string
	// Addr is the harnessd listen address (e.g. ":8080"), exported to the
	// daemon via HARNESS_ADDR in the unit environment.
	Addr string
	// LogDir is the directory that receives stdout/stderr log files.
	LogDir string
}

// stdoutLogPath returns the service's stdout log file path.
func (o serviceOptions) stdoutLogPath() string {
	return filepath.Join(o.LogDir, "harnessd.stdout.log")
}

// stderrLogPath returns the service's stderr log file path.
func (o serviceOptions) stderrLogPath() string {
	return filepath.Join(o.LogDir, "harnessd.stderr.log")
}

// runService dispatches "harnesscli service <subcommand>", following the
// runAuth nested-subcommand pattern.
func runService(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "harnesscli service: subcommand required")
		fmt.Fprintln(stderr, "usage: harnesscli service <install|uninstall> [--flags]")
		return 1
	}
	switch args[0] {
	case "install":
		return runServiceInstall(args[1:])
	case "uninstall":
		return runServiceUninstall(args[1:])
	case "start", "stop", "status":
		fmt.Fprintf(stderr, "harnesscli service %s: not yet implemented (install and uninstall are available; lifecycle commands land in the next slice)\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "harnesscli service: unknown subcommand %q (try: install, uninstall)\n", args[0])
		return 1
	}
}

// serviceUnitPath returns the unit file path for the given platform and home
// directory, or an error for platforms without a supported backend.
func serviceUnitPath(platform, home string) (string, error) {
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", serviceLaunchdFileName), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", serviceSystemdUnitName), nil
	default:
		return "", fmt.Errorf("unsupported platform %q (user services are supported on macOS via launchd and Linux via systemd --user)", platform)
	}
}

// renderServiceUnit renders the unit file for the given platform.
func renderServiceUnit(platform string, opts serviceOptions) ([]byte, error) {
	switch platform {
	case "darwin":
		return renderLaunchdPlist(opts), nil
	case "linux":
		return renderSystemdUnit(opts), nil
	default:
		return nil, fmt.Errorf("unsupported platform %q (user services are supported on macOS via launchd and Linux via systemd --user)", platform)
	}
}

// xmlEscape escapes a string for embedding in plist XML text/attribute
// content.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		// EscapeText only errors on invalid UTF-8; fall back to the raw
		// string rather than failing unit generation.
		return s
	}
	return buf.String()
}

// renderLaunchdPlist renders a user-level launchd agent plist. Pure function.
func renderLaunchdPlist(opts serviceOptions) []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", serviceLabel)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(opts.BinaryPath))
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	b.WriteString("\t\t<key>HARNESS_ADDR</key>\n")
	fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(opts.Addr))
	b.WriteString("\t</dict>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	b.WriteString("\t<key>StandardOutPath</key>\n")
	fmt.Fprintf(&b, "\t<string>%s</string>\n", xmlEscape(opts.stdoutLogPath()))
	b.WriteString("\t<key>StandardErrorPath</key>\n")
	fmt.Fprintf(&b, "\t<string>%s</string>\n", xmlEscape(opts.stderrLogPath()))
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String())
}

// renderSystemdUnit renders a systemd --user service unit. Pure function.
func renderSystemdUnit(opts serviceOptions) []byte {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=go-code harnessd (user service)\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", opts.BinaryPath)
	fmt.Fprintf(&b, "Environment=HARNESS_ADDR=%s\n", opts.Addr)
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	fmt.Fprintf(&b, "StandardOutput=append:%s\n", opts.stdoutLogPath())
	fmt.Fprintf(&b, "StandardError=append:%s\n", opts.stderrLogPath())
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return []byte(b.String())
}

// resolveServiceBinary resolves the harnessd executable: an explicit --binary
// flag wins, otherwise harnessd is looked up on PATH.
func resolveServiceBinary(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		abs, err := filepath.Abs(flagValue)
		if err != nil {
			return "", fmt.Errorf("resolve --binary path: %w", err)
		}
		return abs, nil
	}
	path, err := exec.LookPath("harnessd")
	if err != nil {
		return "", fmt.Errorf("harnessd binary not found on PATH; install go-code first or pass --binary /path/to/harnessd")
	}
	return path, nil
}

// resolveServiceAddr resolves the listen address the generated unit exports as
// HARNESS_ADDR: an explicit --addr flag wins, otherwise the internal/config
// stack (defaults → ~/.harness/config.toml → HARNESS_ADDR env) decides — the
// same resolution harnessd applies to itself. The project-config layer is
// deliberately skipped: an installed user service has no meaningful working
// directory, so only user-global config and env apply.
func resolveServiceAddr(flagValue, home string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue, nil
	}
	cfg, err := config.Load(config.LoadOptions{
		UserConfigPath: filepath.Join(home, ".harness", "config.toml"),
	})
	if err != nil {
		return "", fmt.Errorf("resolve addr from config: %w", err)
	}
	return cfg.Addr, nil
}

// runServiceInstall implements "harnesscli service install".
func runServiceInstall(args []string) int {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	binary := fs.String("binary", "", "path to the harnessd binary (default: look up harnessd on PATH)")
	addr := fs.String("addr", "", "listen address for harnessd (default: resolve like harnessd — HARNESS_ADDR env or :8080)")
	logDir := fs.String("log-dir", "", "directory for service logs (default ~/.harness/logs)")
	dryRun := fs.Bool("dry-run", false, "print the rendered unit file and target path without writing anything")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: resolve home directory: %v\n", err)
		return 1
	}

	unitPath, err := serviceUnitPath(servicePlatform, home)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: %v\n", err)
		return 1
	}

	binaryPath, err := resolveServiceBinary(*binary)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: %v\n", err)
		return 1
	}

	resolvedAddr, err := resolveServiceAddr(*addr, home)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: %v\n", err)
		return 1
	}

	resolvedLogDir := *logDir
	if strings.TrimSpace(resolvedLogDir) == "" {
		resolvedLogDir = filepath.Join(home, ".harness", "logs")
	}

	content, err := renderServiceUnit(servicePlatform, serviceOptions{
		BinaryPath: binaryPath,
		Addr:       resolvedAddr,
		LogDir:     resolvedLogDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: %v\n", err)
		return 1
	}

	if *dryRun {
		fmt.Fprintf(stdout, "target: %s\n\n%s", unitPath, content)
		fmt.Fprintln(stdout, "(dry run: nothing written)")
		return 0
	}

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: create unit dir: %v\n", err)
		return 1
	}
	// launchd/systemd do not create the log directory; do it here so the
	// service can open its log files on first start.
	if err := os.MkdirAll(resolvedLogDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: create log dir: %v\n", err)
		return 1
	}
	if err := os.WriteFile(unitPath, content, 0o644); err != nil {
		fmt.Fprintf(stderr, "harnesscli service install: write unit file: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "installed %s user service unit at %s\n", servicePlatformName(servicePlatform), unitPath)
	fmt.Fprintf(stdout, "logs: %s\n", resolvedLogDir)
	return 0
}

// runServiceUninstall implements "harnesscli service uninstall": best-effort
// unload/disable of the running service, then removal of the unit file.
func runServiceUninstall(args []string) int {
	fs := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service uninstall: resolve home directory: %v\n", err)
		return 1
	}

	unitPath, err := serviceUnitPath(servicePlatform, home)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli service uninstall: %v\n", err)
		return 1
	}

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		fmt.Fprintf(stderr, "harnesscli service uninstall: service not installed (no unit file at %s)\n", unitPath)
		return 1
	} else if err != nil {
		fmt.Fprintf(stderr, "harnesscli service uninstall: stat unit file: %v\n", err)
		return 1
	}

	unloadServiceBestEffort(servicePlatform)

	if err := os.Remove(unitPath); err != nil {
		fmt.Fprintf(stderr, "harnesscli service uninstall: remove unit file: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s\n", unitPath)
	return 0
}

// unloadServiceBestEffort asks the platform service manager to stop and
// disable the unit. Failures are warnings, not errors: the unit may simply
// not be loaded, and the file removal must still proceed.
func unloadServiceBestEffort(platform string) {
	var name string
	var args []string
	switch platform {
	case "darwin":
		name = "launchctl"
		args = []string{"bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), serviceLabel)}
	case "linux":
		name = "systemctl"
		args = []string{"--user", "disable", "--now", serviceSystemdUnitName}
	default:
		return
	}
	if err := serviceRunLifecycle(name, args...); err != nil {
		fmt.Fprintf(stderr, "harnesscli service uninstall: warning: %s %s failed: %v (continuing)\n",
			name, strings.Join(args, " "), err)
	}
}

// servicePlatformName returns a human label for the service backend.
func servicePlatformName(platform string) string {
	switch platform {
	case "darwin":
		return "launchd"
	case "linux":
		return "systemd"
	default:
		return platform
	}
}
