package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedCommand captures one lifecycle-tool invocation (launchctl/systemctl).
type recordedCommand struct {
	name string
	args []string
}

// setupServiceTest isolates the service command surface: stdout/stderr are
// captured, the platform is forced, and the lifecycle runner is replaced with
// a recording fake so tests never exec real launchctl/systemctl.
func setupServiceTest(t *testing.T, platform string) (outBuf, errBuf *bytes.Buffer, calls *[]recordedCommand) {
	t.Helper()
	origOut, origErr := stdout, stderr
	origPlatform := servicePlatform
	origRunner := serviceRunLifecycle

	outBuf, errBuf = &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = outBuf, errBuf
	servicePlatform = platform
	calls = &[]recordedCommand{}
	serviceRunLifecycle = func(name string, args ...string) error {
		*calls = append(*calls, recordedCommand{name: name, args: append([]string(nil), args...)})
		return nil
	}

	t.Cleanup(func() {
		stdout, stderr = origOut, origErr
		servicePlatform = origPlatform
		serviceRunLifecycle = origRunner
	})
	return outBuf, errBuf, calls
}

func writeFakeHarnessd(t *testing.T) (binDir, binPath string) {
	t.Helper()
	binDir = t.TempDir()
	binPath = filepath.Join(binDir, "harnessd")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir, binPath
}

func TestServiceRenderLaunchdPlist(t *testing.T) {
	content := renderLaunchdPlist(serviceOptions{
		BinaryPath: "/usr/local/bin/harnessd",
		Addr:       ":8080",
		LogDir:     "/Users/test/.harness/logs",
	})
	plist := string(content)

	// Well-formed XML (plutil-level validity is checked separately on macOS).
	var doc struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("rendered plist is not well-formed XML: %v\n%s", err, plist)
	}

	for _, want := range []string{
		"<string>com.gocode.harnessd</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/harnessd</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"<key>StandardOutPath</key>",
		"<string>/Users/test/.harness/logs/harnessd.stdout.log</string>",
		"<key>StandardErrorPath</key>",
		"<string>/Users/test/.harness/logs/harnessd.stderr.log</string>",
		"<key>HARNESS_ADDR</key>",
		"<string>:8080</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("rendered plist missing %q\n%s", want, plist)
		}
	}

	// The binary must be the first program argument.
	paIdx := strings.Index(plist, "<key>ProgramArguments</key>")
	binIdx := strings.Index(plist, "<string>/usr/local/bin/harnessd</string>")
	if paIdx < 0 || binIdx < 0 || binIdx < paIdx {
		t.Errorf("ProgramArguments[0] must be the resolved harnessd path\n%s", plist)
	}
}

func TestServiceRenderSystemdUnit(t *testing.T) {
	unit := string(renderSystemdUnit(serviceOptions{
		BinaryPath: "/usr/local/bin/harnessd",
		Addr:       ":9090",
		LogDir:     "/home/test/.harness/logs",
	}))

	for _, want := range []string{
		"[Unit]",
		"[Service]",
		"ExecStart=/usr/local/bin/harnessd",
		"Environment=HARNESS_ADDR=:9090",
		"Restart=on-failure",
		"StandardOutput=append:/home/test/.harness/logs/harnessd.stdout.log",
		"StandardError=append:/home/test/.harness/logs/harnessd.stderr.log",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("rendered unit missing %q\n%s", want, unit)
		}
	}
}

func TestServiceInstallLaunchdWritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	out, _, calls := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}

	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("expected plist at %s: %v", unitPath, err)
	}
	if !strings.Contains(string(data), "/fake/harnessd") {
		t.Errorf("plist should reference the resolved binary path\n%s", data)
	}
	if !strings.Contains(out.String(), unitPath) {
		t.Errorf("install output %q should name the written path", out.String())
	}
	// The log directory must exist so launchd can open the log files.
	if info, err := os.Stat(filepath.Join(home, ".harness", "logs")); err != nil || !info.IsDir() {
		t.Errorf("expected log dir ~/.harness/logs to be created: %v", err)
	}
	// Slice 1 install writes the unit only; it must not bootstrap/start anything.
	if len(*calls) != 0 {
		t.Errorf("install must not invoke lifecycle tools, got %v", *calls)
	}
}

func TestServiceInstallSystemdWritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setupServiceTest(t, "linux")

	code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", "harnessd.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("expected unit at %s: %v", unitPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "ExecStart=/fake/harnessd") {
		t.Errorf("unit should reference the resolved binary path\n%s", text)
	}
	if !strings.Contains(text, "WantedBy=default.target") {
		t.Errorf("unit must install into default.target\n%s", text)
	}
}

func TestServiceInstallDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	out, _, calls := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install", "--dry-run", "--binary", "/fake/harnessd"})
	if code != 0 {
		t.Fatalf("dry-run exit code: got %d", code)
	}

	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write %s", unitPath)
	}
	if !strings.Contains(out.String(), unitPath) {
		t.Errorf("dry-run output %q should name the target path", out.String())
	}
	if !strings.Contains(out.String(), "<plist") || !strings.Contains(out.String(), "/fake/harnessd") {
		t.Errorf("dry-run output should print the rendered plist")
	}
	if len(*calls) != 0 {
		t.Errorf("dry-run must not invoke lifecycle tools, got %v", *calls)
	}
}

func TestServiceInstallBinaryFlagWinsOverPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir, _ := writeFakeHarnessd(t)
	t.Setenv("PATH", binDir)
	setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install", "--binary", "/explicit/harnessd"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/explicit/harnessd") {
		t.Errorf("--binary flag must win over PATH lookup\n%s", data)
	}
}

func TestServiceInstallBinaryFromPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir, binPath := writeFakeHarnessd(t)
	t.Setenv("PATH", binDir)
	setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), binPath) {
		t.Errorf("install should resolve harnessd from PATH as %q\n%s", binPath, data)
	}
}

func TestServiceInstallMissingBinaryFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // empty dir: no harnessd on PATH
	_, errOut, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install"})
	if code == 0 {
		t.Fatal("install without --binary and without harnessd on PATH must fail")
	}
	if !strings.Contains(errOut.String(), "harnessd") || !strings.Contains(errOut.String(), "--binary") {
		t.Errorf("error %q should name the missing binary and the --binary escape hatch", errOut.String())
	}
}

func TestServiceInstallAddrFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HARNESS_ADDR", ":9999")
	out, _, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install", "--dry-run", "--binary", "/fake/harnessd"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	if !strings.Contains(out.String(), ":9999") {
		t.Errorf("rendered unit should honor HARNESS_ADDR\n%s", out.String())
	}
}

func TestServiceInstallAddrFlagOverridesEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HARNESS_ADDR", ":9999")
	out, _, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "install", "--dry-run", "--binary", "/fake/harnessd", "--addr", ":7777"})
	if code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	if !strings.Contains(out.String(), ":7777") {
		t.Errorf("--addr flag must override HARNESS_ADDR\n%s", out.String())
	}
	if strings.Contains(out.String(), ":9999") {
		t.Errorf("env addr should not appear when --addr is given\n%s", out.String())
	}
}

func TestServiceUninstallNotInstalledFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, errOut, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "uninstall"})
	if code == 0 {
		t.Fatal("uninstall of a non-installed service must fail")
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")
	if !strings.Contains(errOut.String(), "not installed") || !strings.Contains(errOut.String(), unitPath) {
		t.Errorf("error %q should say not installed and name the unit path", errOut.String())
	}
}

func TestServiceUninstallRemovesFileAndBootsOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, _, calls := setupServiceTest(t, "darwin")

	if code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"}); code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")

	code := dispatch([]string{"service", "uninstall"})
	if code != 0 {
		t.Fatalf("uninstall exit code: got %d", code)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Errorf("uninstall must remove %s", unitPath)
	}

	// Best-effort launchctl bootout of the user agent before removal.
	want := recordedCommand{
		name: "launchctl",
		args: []string{"bootout", fmt.Sprintf("gui/%d/com.gocode.harnessd", os.Getuid())},
	}
	if len(*calls) != 1 || (*calls)[0].name != want.name || strings.Join((*calls)[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("expected exactly %v, got %v", want, *calls)
	}
}

func TestServiceUninstallSystemdDisableNow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, _, calls := setupServiceTest(t, "linux")

	if code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"}); code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "harnessd.service")

	code := dispatch([]string{"service", "uninstall"})
	if code != 0 {
		t.Fatalf("uninstall exit code: got %d", code)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Errorf("uninstall must remove %s", unitPath)
	}

	want := recordedCommand{name: "systemctl", args: []string{"--user", "disable", "--now", "harnessd.service"}}
	if len(*calls) != 1 || (*calls)[0].name != want.name || strings.Join((*calls)[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("expected exactly %v, got %v", want, *calls)
	}
}

func TestServiceLifecycleStubsNotImplemented(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setupServiceTest(t, "darwin")

	for _, sub := range []string{"start", "stop", "status"} {
		_, errOut, _ := setupServiceTest(t, "darwin")
		code := dispatch([]string{"service", sub})
		if code == 0 {
			t.Errorf("service %s must exit non-zero until implemented", sub)
		}
		if !strings.Contains(errOut.String(), "not yet implemented") {
			t.Errorf("service %s error %q should say not yet implemented", sub, errOut.String())
		}
	}
}

func TestServiceUnknownSubcommandFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "explode"})
	if code == 0 {
		t.Fatal("unknown subcommand must fail")
	}
	if !strings.Contains(errOut.String(), "unknown subcommand") {
		t.Errorf("error %q should report unknown subcommand", errOut.String())
	}
}

func TestServiceNoSubcommandPrintsUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service"})
	if code == 0 {
		t.Fatal("bare 'service' must print usage and fail")
	}
	if !strings.Contains(errOut.String(), "install") || !strings.Contains(errOut.String(), "uninstall") {
		t.Errorf("usage %q should name subcommands", errOut.String())
	}
}

func TestServiceInstallUnsupportedPlatformFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, _ := setupServiceTest(t, "windows")

	code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"})
	if code == 0 {
		t.Fatal("install on an unsupported platform must fail")
	}
	if !strings.Contains(errOut.String(), "unsupported platform") {
		t.Errorf("error %q should report the unsupported platform", errOut.String())
	}
}
