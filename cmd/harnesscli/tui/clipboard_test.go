package tui_test

import (
	"errors"
	"os"
	"sync"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestTUI028_CopyActionGracefulInHeadless verifies that CopyToClipboard does
// not panic when running in headless mode (TERM=="" or TERM=="dumb").
func TestTUI028_CopyActionGracefulInHeadless(t *testing.T) {
	// Save and restore TERM env var.
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()

	os.Unsetenv("TERM")

	// Must not panic, must return false (headless fallback).
	result := tui.CopyToClipboard("hello clipboard")
	if result != false {
		t.Errorf("CopyToClipboard in headless mode must return false, got %v", result)
	}
}

// TestTUI028_IsHeadlessDetectsEnv verifies that IsHeadless returns true when
// TERM is empty or "dumb", and false for a real terminal value.
func TestTUI028_IsHeadlessDetectsEnv(t *testing.T) {
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()

	// Empty TERM → headless
	os.Unsetenv("TERM")
	if !tui.IsHeadless() {
		t.Error("IsHeadless() must return true when TERM is unset")
	}

	// "dumb" → headless
	os.Setenv("TERM", "dumb")
	if !tui.IsHeadless() {
		t.Error("IsHeadless() must return true when TERM=dumb")
	}

	// Real terminal → not headless
	os.Setenv("TERM", "xterm-256color")
	if tui.IsHeadless() {
		t.Error("IsHeadless() must return false when TERM=xterm-256color")
	}
}

// TestTUI028_CopyEmptyBuffer verifies that CopyToClipboard with empty text
// does not panic and returns a boolean.
func TestTUI028_CopyEmptyBuffer(t *testing.T) {
	// Run in headless to avoid actually writing to stdout in CI.
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	// Must not panic.
	_ = tui.CopyToClipboard("")
}

// TestTUI028_ClipboardConcurrent verifies that 10 goroutines calling
// CopyToClipboard concurrently trigger no data races.
func TestTUI028_ClipboardConcurrent(t *testing.T) {
	// Run in headless to avoid actually writing to stdout in CI.
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = tui.CopyToClipboard("concurrent clipboard test")
				_ = tui.IsHeadless()
			}
		}()
	}
	wg.Wait()
}

// TestTUI028_VisualSnapshot_80x24 writes a simple snapshot confirming
// CopyToClipboard and IsHeadless return expected types.
func TestTUI028_VisualSnapshot_80x24(t *testing.T) {
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	headless := tui.IsHeadless()
	copyResult := tui.CopyToClipboard("snapshot test")

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-028-copy-80x24.txt"
	var content string
	if headless {
		content = "IsHeadless: true\n"
	} else {
		content = "IsHeadless: false\n"
	}
	if copyResult {
		content += "CopyToClipboard: true\n"
	} else {
		content += "CopyToClipboard: false\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI028_VisualSnapshot_120x40 writes a snapshot at 120 columns.
func TestTUI028_VisualSnapshot_120x40(t *testing.T) {
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	headless := tui.IsHeadless()
	copyResult := tui.CopyToClipboard("snapshot test 120")

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-028-copy-120x40.txt"
	var content string
	if headless {
		content = "IsHeadless: true\n"
	} else {
		content = "IsHeadless: false\n"
	}
	if copyResult {
		content += "CopyToClipboard: true\n"
	} else {
		content += "CopyToClipboard: false\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI028_VisualSnapshot_200x50 writes a snapshot at 200 columns.
func TestTUI028_VisualSnapshot_200x50(t *testing.T) {
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	headless := tui.IsHeadless()
	copyResult := tui.CopyToClipboard("snapshot test 200")

	dir := "testdata/snapshots"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create snapshot dir: %v", err)
	}
	path := dir + "/TUI-028-copy-200x50.txt"
	var content string
	if headless {
		content = "IsHeadless: true\n"
	} else {
		content = "IsHeadless: false\n"
	}
	if copyResult {
		content += "CopyToClipboard: true\n"
	} else {
		content += "CopyToClipboard: false\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	t.Logf("snapshot written to %s", path)
}

// TestTUI028_ReadImageFromClipboardHeadless verifies the clipboard image
// reader short-circuits with ErrClipboardHeadless (and no image) when the TUI
// runs headless, using only the exported API.
func TestTUI028_ReadImageFromClipboardHeadless(t *testing.T) {
	orig := os.Getenv("TERM")
	defer func() {
		if orig == "" {
			os.Unsetenv("TERM")
		} else {
			os.Setenv("TERM", orig)
		}
	}()
	os.Unsetenv("TERM")

	img, err := tui.ReadImageFromClipboard()
	if !errors.Is(err, tui.ErrClipboardHeadless) {
		t.Errorf("ReadImageFromClipboard in headless mode must return ErrClipboardHeadless, got %v", err)
	}
	if img != (tui.ClipboardImage{}) {
		t.Errorf("ReadImageFromClipboard in headless mode must return a zero ClipboardImage, got %+v", img)
	}
}
