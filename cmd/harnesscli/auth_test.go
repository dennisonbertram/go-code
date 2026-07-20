package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAuthLogin_GeneratesAndSavesKey(t *testing.T) {
	// Redirect stdout/stderr for capture.
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()

	var outBuf, errBuf bytes.Buffer
	stdout = &outBuf
	stderr = &errBuf

	// Use a temporary directory for the config file.
	tmpDir := t.TempDir()
	origConfigPath := os.Getenv("HOME")
	_ = origConfigPath

	// Override home by setting HOME env var temporarily.
	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	os.Setenv("HOME", tmpDir)

	code := runAuthLogin([]string{
		"-server=http://localhost:8080",
		"-tenant=test-tenant",
		"-name=test-cli",
	})
	if code != 0 {
		t.Fatalf("runAuthLogin returned %d, stderr: %s", code, errBuf.String())
	}

	outStr := outBuf.String()

	// Output must contain the API key.
	if !strings.Contains(outStr, "harness_sk_") {
		t.Errorf("output does not contain 'harness_sk_': %s", outStr)
	}

	// Config file must exist.
	cfgFile := filepath.Join(tmpDir, ".harness", "config.json")
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "harness_sk_") {
		t.Errorf("config file does not contain API key: %s", content)
	}
	if !strings.Contains(content, "http://localhost:8080") {
		t.Errorf("config file does not contain server URL: %s", content)
	}

	// Config file permissions must be 0600 (owner read/write only).
	fi, err := os.Stat(cfgFile)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config file permissions: got %o, want 0600", fi.Mode().Perm())
	}
}

func TestRunAuth_UnknownSubcommand(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var errBuf bytes.Buffer
	stderr = &errBuf

	code := runAuth([]string{"unknown"})
	if code == 0 {
		t.Error("expected non-zero exit for unknown subcommand")
	}
}

func TestRunAuth_NoSubcommand(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var errBuf bytes.Buffer
	stderr = &errBuf

	code := runAuth([]string{})
	if code == 0 {
		t.Error("expected non-zero exit for missing subcommand")
	}
}

func TestRunAuthKimiLifecycleNeverPrintsCredential(t *testing.T) {
	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	_ = os.Setenv("HOME", tmp)
	vendor := filepath.Join(tmp, ".kimi-code", "credentials")
	if err := os.MkdirAll(vendor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendor, "kimi-code.json"), []byte(`{"access_token":"fake-access","refresh_token":"fake-refresh","expires_at":2000000000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	origOut, origErr := stdout, stderr
	stdout, stderr = &out, &errOut
	t.Cleanup(func() { stdout, stderr = origOut, origErr })
	if got := runAuth([]string{"kimi", "login"}); got != 0 {
		t.Fatalf("login = %d: %s", got, errOut.String())
	}
	if strings.Contains(out.String(), "fake-access") || strings.Contains(out.String(), "fake-refresh") {
		t.Fatal("credential printed")
	}
	out.Reset()
	if got := runAuth([]string{"kimi", "status"}); got != 0 || !strings.Contains(out.String(), "configured") {
		t.Fatalf("status: %d %s", got, out.String())
	}
	if got := runAuth([]string{"kimi", "logout"}); got != 0 {
		t.Fatal("logout failed")
	}
}

func TestDispatch_AuthRouted(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var errBuf bytes.Buffer
	stderr = &errBuf

	// "dispatch auth unknown" should go to runAuth and return non-zero.
	code := dispatch([]string{"auth", "unknown"})
	if code == 0 {
		t.Error("expected non-zero exit for auth unknown")
	}
}

func TestDispatch_FallsBackToRun(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var errBuf bytes.Buffer
	stderr = &errBuf

	// dispatch with -prompt missing should fall to run() and return non-zero.
	code := dispatch([]string{"-prompt="})
	if code == 0 {
		t.Error("expected non-zero exit when prompt is empty")
	}
}

func TestLoadConfigNotExist(t *testing.T) {
	// Point configPath to non-existent file.
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadConfig_CorruptFile_WarnsOnStderr(t *testing.T) {
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	cfgDir := filepath.Join(tmp, ".harness")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}

	origStderr := stderr
	var errBuf bytes.Buffer
	stderr = &errBuf
	defer func() { stderr = origStderr }()

	cfg, err := loadConfig()
	if err == nil {
		t.Fatal("expected an error for a corrupt config file")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on parse failure, got: %+v", cfg)
	}
	if !strings.Contains(errBuf.String(), "corrupt") {
		t.Errorf("expected a clear corrupt-config warning on stderr, got: %q", errBuf.String())
	}
}
