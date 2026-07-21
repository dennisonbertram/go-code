package oauth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestOpenBrowser_InvokesPlatformLauncher verifies that openBrowser invokes
// the platform launcher with the authorization URL. A fake launcher placed
// on PATH records its arguments, so no real browser is started.
func TestOpenBrowser_InvokesPlatformLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot shadow rundll32 with a PATH executable")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "invoked-args.txt")

	launcher := "xdg-open"
	if runtime.GOOS == "darwin" {
		launcher = "open"
	}
	script := "#!/bin/sh\nprintf '%s' \"$1\" > " + logPath + "\n"
	if err := os.WriteFile(filepath.Join(dir, launcher), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	const authURL = "http://127.0.0.1:54321/callback?code=test&state=xyz"
	if err := openBrowser(authURL); err != nil {
		t.Fatalf("openBrowser: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fake launcher was not invoked: %v", err)
	}
	if got := string(raw); got != authURL {
		t.Errorf("launcher received %q, want %q", got, authURL)
	}
}

// TestOpenBrowser_LauncherMissing_ReturnsError verifies a clear error when no
// platform launcher is available, so callers can surface the manual URL.
func TestOpenBrowser_LauncherMissing_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot shadow rundll32 with a PATH executable")
	}
	t.Setenv("PATH", t.TempDir()) // nothing on PATH

	if err := openBrowser("http://example.com/authorize"); err == nil {
		t.Fatal("expected an error when the launcher is missing, got nil")
	}
}
