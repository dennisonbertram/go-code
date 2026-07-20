package tui

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// Typed errors returned by ReadImageFromClipboard so callers can distinguish
// "nothing to paste" from "cannot paste here".
var (
	// ErrClipboardHeadless is returned when the TUI runs headless (see
	// IsHeadless); no subprocess is ever spawned in that mode.
	ErrClipboardHeadless = errors.New("clipboard image read unavailable in headless mode")
	// ErrClipboardUnsupported is returned on platforms without a supported
	// clipboard tool (anything but darwin/linux, or linux without
	// wl-paste/xclip).
	ErrClipboardUnsupported = errors.New("clipboard image read unsupported on this platform")
	// ErrClipboardNoImage is returned when the clipboard holds no PNG image.
	ErrClipboardNoImage = errors.New("no image on the system clipboard")
)

// ClipboardImage is an image read from the system clipboard, persisted to a
// temporary file. The caller owns the file and must remove it (and its
// directory) when done.
type ClipboardImage struct {
	// Path is the temporary file holding the PNG bytes.
	Path string
	// MediaType is always "image/png" in this slice.
	MediaType string
}

// Test seams: the platform and every subprocess interaction are replaceable
// from internal tests.
var (
	clipboardImageGOOS     = runtime.GOOS
	clipboardImageLookPath = exec.LookPath
	clipboardImageOutput   = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
)

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// ReadImageFromClipboard reads a PNG image from the system clipboard and
// writes it to a new os.MkdirTemp directory, returning the file path and
// media type. Platform support: macOS via osascript, Linux via wl-paste
// (Wayland) or xclip (X11); other platforms return ErrClipboardUnsupported.
// In headless mode it returns ErrClipboardHeadless without spawning any
// subprocess.
func ReadImageFromClipboard() (ClipboardImage, error) {
	if IsHeadless() {
		return ClipboardImage{}, ErrClipboardHeadless
	}
	var (
		data []byte
		err  error
	)
	switch clipboardImageGOOS {
	case "darwin":
		data, err = readClipboardImageDarwin()
	case "linux":
		data, err = readClipboardImageLinux()
	default:
		return ClipboardImage{}, fmt.Errorf("%w: %s", ErrClipboardUnsupported, clipboardImageGOOS)
	}
	if err != nil {
		return ClipboardImage{}, err
	}
	if !bytes.HasPrefix(data, pngMagic) {
		return ClipboardImage{}, fmt.Errorf("%w: clipboard payload is not a PNG image", ErrClipboardNoImage)
	}
	return writeClipboardImageTemp(data)
}

// readClipboardImageDarwin extracts PNG clipboard data with osascript.
// pbpaste cannot read image flavors (its -Prefer flag accepts only txt, rtf,
// and ps), so the PNGf clipboard class is read as an AppleScript data record
// («data PNGf<hex>») and hex-decoded in-process.
func readClipboardImageDarwin() ([]byte, error) {
	if _, err := clipboardImageLookPath("osascript"); err != nil {
		return nil, fmt.Errorf("%w: osascript not found: %v", ErrClipboardUnsupported, err)
	}
	info, err := clipboardImageOutput("osascript", "-e", "clipboard info")
	if err != nil {
		return nil, fmt.Errorf("probe clipboard contents: %w", err)
	}
	if !strings.Contains(string(info), "PNGf") {
		return nil, ErrClipboardNoImage
	}
	out, err := clipboardImageOutput("osascript", "-e", "get the clipboard as «class PNGf»")
	if err != nil {
		return nil, fmt.Errorf("read clipboard PNG data: %w", err)
	}
	return decodeAppleScriptPNGData(out)
}

// decodeAppleScriptPNGData parses the «data PNGf<hex>» record printed by
// osascript and returns the decoded bytes.
func decodeAppleScriptPNGData(out []byte) ([]byte, error) {
	s := strings.TrimSpace(string(out))
	const prefix = "«data PNGf"
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "»") {
		return nil, fmt.Errorf("unexpected osascript data record: %.60q", s)
	}
	hexText := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s[len(prefix):len(s)-len("»")])
	data, err := hex.DecodeString(hexText)
	if err != nil {
		return nil, fmt.Errorf("decode clipboard PNG data record: %w", err)
	}
	return data, nil
}

// readClipboardImageLinux extracts PNG clipboard data via wl-paste (Wayland,
// preferred) or xclip (X11).
func readClipboardImageLinux() ([]byte, error) {
	if _, err := clipboardImageLookPath("wl-paste"); err == nil {
		types, err := clipboardImageOutput("wl-paste", "--list-types")
		if err != nil {
			return nil, fmt.Errorf("%w: wl-paste could not list clipboard types: %v", ErrClipboardNoImage, err)
		}
		if !clipboardListsPNG(types) {
			return nil, ErrClipboardNoImage
		}
		return clipboardImageOutput("wl-paste", "--type", "image/png")
	}
	if _, err := clipboardImageLookPath("xclip"); err == nil {
		targets, err := clipboardImageOutput("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if err != nil {
			return nil, fmt.Errorf("%w: xclip could not list clipboard targets: %v", ErrClipboardNoImage, err)
		}
		if !clipboardListsPNG(targets) {
			return nil, ErrClipboardNoImage
		}
		return clipboardImageOutput("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	}
	return nil, fmt.Errorf("%w: install wl-paste (Wayland) or xclip (X11) to paste images", ErrClipboardUnsupported)
}

// clipboardListsPNG reports whether a newline-separated clipboard type
// listing (wl-paste --list-types or xclip TARGETS) advertises image/png.
func clipboardListsPNG(types []byte) bool {
	for line := range strings.Lines(string(types)) {
		if strings.TrimSpace(line) == "image/png" {
			return true
		}
	}
	return false
}

// writeClipboardImageTemp persists data as clipboard.png inside a fresh
// os.MkdirTemp directory.
func writeClipboardImageTemp(data []byte) (ClipboardImage, error) {
	dir, err := os.MkdirTemp("", "go-code-clipboard-")
	if err != nil {
		return ClipboardImage{}, fmt.Errorf("create clipboard temp dir: %w", err)
	}
	path := filepath.Join(dir, "clipboard.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		os.RemoveAll(dir)
		return ClipboardImage{}, fmt.Errorf("write clipboard image: %w", err)
	}
	return ClipboardImage{Path: path, MediaType: "image/png"}, nil
}
