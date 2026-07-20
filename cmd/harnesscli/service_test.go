package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// serviceRunnerFake records lifecycle-tool invocations and returns queued
// errors in order (nil once the queue is drained), so tests can simulate
// already-loaded, not-loaded, and failing service managers.
type serviceRunnerFake struct {
	calls []recordedCommand
	errs  []error
}

func (f *serviceRunnerFake) run(name string, args ...string) error {
	f.calls = append(f.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

// setupServiceTest isolates the service command surface: stdout/stderr are
// captured, the platform is forced, and the lifecycle runner is replaced with
// a recording fake so tests never exec real launchctl/systemctl.
func setupServiceTest(t *testing.T, platform string) (outBuf, errBuf *bytes.Buffer, runner *serviceRunnerFake) {
	t.Helper()
	origOut, origErr := stdout, stderr
	origPlatform := servicePlatform
	origRunner := serviceRunLifecycle

	outBuf, errBuf = &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = outBuf, errBuf
	servicePlatform = platform
	runner = &serviceRunnerFake{}
	serviceRunLifecycle = runner.run

	t.Cleanup(func() {
		stdout, stderr = origOut, origErr
		servicePlatform = origPlatform
		serviceRunLifecycle = origRunner
	})
	return outBuf, errBuf, runner
}

// requireCalls asserts the exact sequence of lifecycle-tool invocations.
func requireCalls(t *testing.T, got []recordedCommand, want ...recordedCommand) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("runner calls: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].name != want[i].name || strings.Join(got[i].args, " ") != strings.Join(want[i].args, " ") {
			t.Errorf("call %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// guiDomain is the launchd user domain for the current uid.
func guiDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// installServiceForTest installs a unit with a fake binary via the real
// install command and returns the unit path.
func installServiceForTest(t *testing.T, platform string) string {
	t.Helper()
	if code := dispatch([]string{"service", "install", "--binary", "/fake/harnessd"}); code != 0 {
		t.Fatalf("install exit code: got %d", code)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	unitPath, err := serviceUnitPath(platform, home)
	if err != nil {
		t.Fatal(err)
	}
	return unitPath
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
	out, _, runner := setupServiceTest(t, "darwin")

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
	// Install writes the unit only; it must not bootstrap/start anything.
	if len(runner.calls) != 0 {
		t.Errorf("install must not invoke lifecycle tools, got %v", runner.calls)
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
	out, _, runner := setupServiceTest(t, "darwin")

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
	if len(runner.calls) != 0 {
		t.Errorf("dry-run must not invoke lifecycle tools, got %v", runner.calls)
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
	_, _, runner := setupServiceTest(t, "darwin")

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
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"bootout", guiDomain() + "/" + serviceLabel}},
	)
}

func TestServiceUninstallSystemdDisableNow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, _, runner := setupServiceTest(t, "linux")

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

	requireCalls(t, runner.calls,
		recordedCommand{name: "systemctl", args: []string{"--user", "disable", "--now", "harnessd.service"}},
	)
}

// --- Slice 2: lifecycle commands (start/stop/status) ---

func TestServiceStartLaunchdBootstraps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	unitPath := installServiceForTest(t, "darwin")

	code := dispatch([]string{"service", "start"})
	if code != 0 {
		t.Fatalf("start exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"bootstrap", guiDomain(), unitPath}},
	)
	if !strings.Contains(out.String(), "started") {
		t.Errorf("start output %q should confirm the service started", out.String())
	}
}

func TestServiceStartLaunchdAlreadyLoadedKickstarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	unitPath := installServiceForTest(t, "darwin")
	// First call (bootstrap) fails because the job is already loaded; the
	// fallback kickstart -k succeeds and restarts it.
	runner.errs = []error{errors.New("Bootstrap failed: 5: Input/output error")}

	code := dispatch([]string{"service", "start"})
	if code != 0 {
		t.Fatalf("start exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"bootstrap", guiDomain(), unitPath}},
		recordedCommand{name: "launchctl", args: []string{"kickstart", "-k", guiDomain() + "/" + serviceLabel}},
	)
	if !strings.Contains(out.String(), "restarted") {
		t.Errorf("start output %q should report the restart of an already-loaded service", out.String())
	}
}

func TestServiceStartLaunchdBootstrapFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, runner := setupServiceTest(t, "darwin")
	installServiceForTest(t, "darwin")
	runner.errs = []error{errors.New("bootstrap exploded"), errors.New("kickstart exploded")}

	code := dispatch([]string{"service", "start"})
	if code == 0 {
		t.Fatal("start must fail when both bootstrap and kickstart fail")
	}
	if !strings.Contains(errOut.String(), "service start") || !strings.Contains(errOut.String(), "bootstrap exploded") {
		t.Errorf("error %q should surface the bootstrap failure", errOut.String())
	}
}

func TestServiceStartSystemdStarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "linux")
	installServiceForTest(t, "linux")

	code := dispatch([]string{"service", "start"})
	if code != 0 {
		t.Fatalf("start exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "systemctl", args: []string{"--user", "start", "harnessd.service"}},
	)
	if !strings.Contains(out.String(), "started") {
		t.Errorf("start output %q should confirm the service started", out.String())
	}
}

func TestServiceStartSystemdFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, runner := setupServiceTest(t, "linux")
	installServiceForTest(t, "linux")
	runner.errs = []error{errors.New("Failed to start harnessd.service: Unit not found")}

	code := dispatch([]string{"service", "start"})
	if code == 0 {
		t.Fatal("start must fail when systemctl start fails")
	}
	if !strings.Contains(errOut.String(), "service start") {
		t.Errorf("error %q should name the failing command", errOut.String())
	}
}

func TestServiceStartNotInstalledFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, errOut, runner := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "start"})
	if code == 0 {
		t.Fatal("start before install must fail")
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")
	if !strings.Contains(errOut.String(), "not installed") || !strings.Contains(errOut.String(), unitPath) {
		t.Errorf("error %q should say not installed and name the unit path", errOut.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("start before install must not invoke lifecycle tools, got %v", runner.calls)
	}
}

func TestServiceStartUnsupportedPlatformFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, _ := setupServiceTest(t, "windows")

	code := dispatch([]string{"service", "start"})
	if code == 0 {
		t.Fatal("start on an unsupported platform must fail")
	}
	if !strings.Contains(errOut.String(), "unsupported platform") {
		t.Errorf("error %q should report the unsupported platform", errOut.String())
	}
}

func TestServiceStopLaunchdBootsOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	installServiceForTest(t, "darwin")

	code := dispatch([]string{"service", "stop"})
	if code != 0 {
		t.Fatalf("stop exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"bootout", guiDomain() + "/" + serviceLabel}},
	)
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("stop output %q should confirm the service stopped", out.String())
	}
}

func TestServiceStopSystemdStops(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "linux")
	installServiceForTest(t, "linux")

	code := dispatch([]string{"service", "stop"})
	if code != 0 {
		t.Fatalf("stop exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "systemctl", args: []string{"--user", "stop", "harnessd.service"}},
	)
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("stop output %q should confirm the service stopped", out.String())
	}
}

func TestServiceStopRunnerErrorFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, runner := setupServiceTest(t, "darwin")
	installServiceForTest(t, "darwin")
	runner.errs = []error{errors.New("Boot-out failed: 3: No such process")}

	code := dispatch([]string{"service", "stop"})
	if code == 0 {
		t.Fatal("stop must fail when the service manager errors")
	}
	if !strings.Contains(errOut.String(), "service stop") || !strings.Contains(errOut.String(), "No such process") {
		t.Errorf("error %q should surface the bootout failure", errOut.String())
	}
}

func TestServiceStopNotInstalledFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, errOut, runner := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "stop"})
	if code == 0 {
		t.Fatal("stop before install must fail")
	}
	if !strings.Contains(errOut.String(), "not installed") {
		t.Errorf("error %q should say not installed", errOut.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("stop before install must not invoke lifecycle tools, got %v", runner.calls)
	}
}

func TestServiceStatusNotInstalledFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, errOut, _ := setupServiceTest(t, "darwin")

	code := dispatch([]string{"service", "status"})
	if code == 0 {
		t.Fatal("status before install must fail")
	}
	unitPath := filepath.Join(home, "Library", "LaunchAgents", "com.gocode.harnessd.plist")
	if !strings.Contains(errOut.String(), "not installed") || !strings.Contains(errOut.String(), unitPath) {
		t.Errorf("error %q should say not installed and name the unit path", errOut.String())
	}
}

func TestServiceStatusInstalledNotRunningLaunchd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	unitPath := installServiceForTest(t, "darwin")
	// launchctl print exits non-zero when the job is not loaded.
	runner.errs = []error{errors.New("Could not find service \"com.gocode.harnessd\" in domain for uid")}

	code := dispatch([]string{"service", "status"})
	if code != 0 {
		t.Fatalf("status exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"print", guiDomain() + "/" + serviceLabel}},
	)
	if !strings.Contains(out.String(), "not running") || !strings.Contains(out.String(), unitPath) {
		t.Errorf("status output %q should report installed-but-not-running and the unit path", out.String())
	}
}

func TestServiceStatusInstalledNotRunningSystemd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "linux")
	installServiceForTest(t, "linux")
	// systemctl is-active exits non-zero (3) when the unit is inactive.
	runner.errs = []error{errors.New("exit status 3")}

	code := dispatch([]string{"service", "status"})
	if code != 0 {
		t.Fatalf("status exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "systemctl", args: []string{"--user", "is-active", "harnessd.service"}},
	)
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("status output %q should report installed-but-not-running", out.String())
	}
}

func TestServiceStatusRunningHealthy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	installServiceForTest(t, "darwin")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	code := dispatch([]string{"service", "status", "--base-url", ts.URL})
	if code != 0 {
		t.Fatalf("status exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"print", guiDomain() + "/" + serviceLabel}},
	)
	text := out.String()
	if !strings.Contains(text, "running") || !strings.Contains(text, "healthy") {
		t.Errorf("status output %q should report running and healthy", text)
	}
	if !strings.Contains(text, ts.URL+"/healthz") {
		t.Errorf("status output %q should name the probed health URL", text)
	}
}

func TestServiceStatusRunningButUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, _, runner := setupServiceTest(t, "darwin")
	installServiceForTest(t, "darwin")

	// Port 1 is never listening: the service manager reports the job loaded
	// but the daemon does not answer the health probe.
	code := dispatch([]string{"service", "status", "--base-url", "http://127.0.0.1:1"})
	if code != 0 {
		t.Fatalf("status exit code: got %d", code)
	}
	requireCalls(t, runner.calls,
		recordedCommand{name: "launchctl", args: []string{"print", guiDomain() + "/" + serviceLabel}},
	)
	text := out.String()
	if !strings.Contains(text, "running") || !strings.Contains(text, "unreachable") {
		t.Errorf("status output %q should report running-but-unreachable", text)
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
