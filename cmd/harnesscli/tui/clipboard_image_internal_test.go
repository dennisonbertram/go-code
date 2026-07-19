package tui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testPNGBytes returns real PNG bytes produced with the stdlib encoder so the
// tests never depend on fixture files.
func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 0xde, G: 0xad, B: 0xbe, A: 0xff})
	img.Set(1, 1, color.RGBA{R: 0x01, G: 0x02, B: 0x03, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// stubClipboardHooks replaces the platform/exec seams of the clipboard image
// reader and restores them when the test ends. It returns a counter of how
// many subprocess invocations were attempted.
func stubClipboardHooks(
	t *testing.T,
	goos string,
	lookPath func(string) (string, error),
	output func(string, ...string) ([]byte, error),
) *int {
	t.Helper()
	calls := new(int)
	oldGOOS, oldLookPath, oldOutput := clipboardImageGOOS, clipboardImageLookPath, clipboardImageOutput
	clipboardImageGOOS = goos
	clipboardImageLookPath = func(file string) (string, error) {
		*calls++
		return lookPath(file)
	}
	clipboardImageOutput = func(name string, args ...string) ([]byte, error) {
		*calls++
		return output(name, args...)
	}
	t.Cleanup(func() {
		clipboardImageGOOS = oldGOOS
		clipboardImageLookPath = oldLookPath
		clipboardImageOutput = oldOutput
	})
	return calls
}

func foundPath(path string) func(string) (string, error) {
	return func(file string) (string, error) {
		return filepath.Join(path, file), nil
	}
}

func notFoundPath(missing ...string) func(string) (string, error) {
	return func(file string) (string, error) {
		for _, m := range missing {
			if file == m {
				return "", fmt.Errorf("executable file not found in $PATH")
			}
		}
		return filepath.Join("/usr/bin", file), nil
	}
}

// assertClipboardImageFile verifies the returned image points at a real temp
// file holding exactly want bytes, then removes the temp directory.
func assertClipboardImageFile(t *testing.T, img ClipboardImage, want []byte) {
	t.Helper()
	if img.Path == "" {
		t.Fatal("ClipboardImage.Path must not be empty on success")
	}
	if img.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want %q", img.MediaType, "image/png")
	}
	if !strings.HasPrefix(img.Path, os.TempDir()) {
		t.Errorf("Path = %q, want it inside the system temp dir %q", img.Path, os.TempDir())
	}
	got, err := os.ReadFile(img.Path)
	if err != nil {
		t.Fatalf("read returned image file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("temp file holds %d bytes, want the exact %d PNG bytes from the clipboard", len(got), len(want))
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(img.Path)) })
}

func TestReadImageFromClipboard_HeadlessShortCircuit(t *testing.T) {
	t.Setenv("TERM", "dumb")
	calls := stubClipboardHooks(t, "darwin",
		foundPath("/usr/bin"),
		func(string, ...string) ([]byte, error) { return nil, errors.New("must not be called") },
	)

	img, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardHeadless) {
		t.Errorf("err = %v, want ErrClipboardHeadless", err)
	}
	if img != (ClipboardImage{}) {
		t.Errorf("img = %+v, want zero value on error", img)
	}
	if *calls != 0 {
		t.Errorf("headless mode spawned %d subprocess calls, want 0", *calls)
	}
}

func TestReadImageFromClipboard_UnsupportedPlatform(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	calls := stubClipboardHooks(t, "windows",
		foundPath("/usr/bin"),
		func(string, ...string) ([]byte, error) { return nil, errors.New("must not be called") },
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardUnsupported) {
		t.Errorf("err = %v, want ErrClipboardUnsupported", err)
	}
	if *calls != 0 {
		t.Errorf("unsupported platform spawned %d subprocess calls, want 0", *calls)
	}
}

func TestReadImageFromClipboard_DarwinPNG(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	pngBytes := testPNGBytes(t)
	calls := stubClipboardHooks(t, "darwin",
		foundPath("/usr/bin"),
		func(name string, args ...string) ([]byte, error) {
			if name != "osascript" {
				return nil, fmt.Errorf("unexpected command %q", name)
			}
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "clipboard info"):
				return []byte("«class PNGf», 1234\n"), nil
			case strings.Contains(joined, "PNGf"):
				// AppleScript prints uppercase hex inside a «data PNGf…» wrapper.
				return []byte("«data PNGf" + strings.ToUpper(fmt.Sprintf("%x", pngBytes)) + "»\n"), nil
			default:
				return nil, fmt.Errorf("unexpected osascript args %v", args)
			}
		},
	)

	img, err := ReadImageFromClipboard()
	if err != nil {
		t.Fatalf("ReadImageFromClipboard: %v", err)
	}
	assertClipboardImageFile(t, img, pngBytes)
	if *calls < 2 {
		t.Errorf("expected probe + read subprocess calls, got %d", *calls)
	}
}

func TestReadImageFromClipboard_DarwinNoImage(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	extractCalls := 0
	stubClipboardHooks(t, "darwin",
		foundPath("/usr/bin"),
		func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "clipboard info") {
				return []byte("«class utf8», 5\n"), nil
			}
			extractCalls++
			return nil, fmt.Errorf("extraction must not run when no PNGf class is advertised")
		},
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardNoImage) {
		t.Errorf("err = %v, want ErrClipboardNoImage for a text-only clipboard", err)
	}
	if extractCalls != 0 {
		t.Errorf("extraction ran %d times despite a text-only clipboard, want 0", extractCalls)
	}
}

func TestReadImageFromClipboard_DarwinMissingOsascript(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "darwin",
		notFoundPath("osascript"),
		func(string, ...string) ([]byte, error) { return nil, errors.New("must not be called") },
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardUnsupported) {
		t.Errorf("err = %v, want ErrClipboardUnsupported when osascript is unavailable", err)
	}
}

func TestReadImageFromClipboard_DarwinMalformedPayload(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "darwin",
		foundPath("/usr/bin"),
		func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "clipboard info") {
				return []byte("«class PNGf», 1234\n"), nil
			}
			return []byte("this is not a data record\n"), nil
		},
	)

	_, err := ReadImageFromClipboard()
	if err == nil {
		t.Fatal("err = nil, want an error for malformed osascript output")
	}
	if errors.Is(err, ErrClipboardHeadless) || errors.Is(err, ErrClipboardUnsupported) {
		t.Errorf("err = %v, want a decode failure, not headless/unsupported", err)
	}
}

func TestReadImageFromClipboard_DarwinNonPNGPayload(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "darwin",
		foundPath("/usr/bin"),
		func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "clipboard info") {
				return []byte("«class PNGf», 1234\n"), nil
			}
			return []byte("«data PNGf" + strings.ToUpper(fmt.Sprintf("%x", []byte("definitely not a png"))) + "»\n"), nil
		},
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardNoImage) {
		t.Errorf("err = %v, want ErrClipboardNoImage when decoded bytes lack the PNG magic", err)
	}
}

func TestReadImageFromClipboard_LinuxWlPaste(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	pngBytes := testPNGBytes(t)
	stubClipboardHooks(t, "linux",
		notFoundPath("xclip"),
		func(name string, args ...string) ([]byte, error) {
			if name != "wl-paste" {
				return nil, fmt.Errorf("unexpected command %q", name)
			}
			for _, a := range args {
				if a == "--list-types" {
					return []byte("text/plain\nimage/png\n"), nil
				}
			}
			return pngBytes, nil
		},
	)

	img, err := ReadImageFromClipboard()
	if err != nil {
		t.Fatalf("ReadImageFromClipboard: %v", err)
	}
	assertClipboardImageFile(t, img, pngBytes)
}

func TestReadImageFromClipboard_LinuxWlPasteNoImage(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "linux",
		foundPath("/usr/bin"),
		func(name string, args ...string) ([]byte, error) {
			return []byte("text/plain\ntext/html\n"), nil
		},
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardNoImage) {
		t.Errorf("err = %v, want ErrClipboardNoImage when wl-paste lists no image/png type", err)
	}
}

func TestReadImageFromClipboard_LinuxXclip(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	pngBytes := testPNGBytes(t)
	stubClipboardHooks(t, "linux",
		notFoundPath("wl-paste"),
		func(name string, args ...string) ([]byte, error) {
			if name != "xclip" {
				return nil, fmt.Errorf("unexpected command %q", name)
			}
			for i, a := range args {
				if a == "-t" && i+1 < len(args) && args[i+1] == "TARGETS" {
					return []byte("TARGETS\nimage/png\ntext/plain\n"), nil
				}
			}
			return pngBytes, nil
		},
	)

	img, err := ReadImageFromClipboard()
	if err != nil {
		t.Fatalf("ReadImageFromClipboard: %v", err)
	}
	assertClipboardImageFile(t, img, pngBytes)
}

func TestReadImageFromClipboard_LinuxXclipNoImage(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "linux",
		notFoundPath("wl-paste"),
		func(name string, args ...string) ([]byte, error) {
			return []byte("TARGETS\ntext/plain\n"), nil
		},
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardNoImage) {
		t.Errorf("err = %v, want ErrClipboardNoImage when xclip TARGETS lacks image/png", err)
	}
}

func TestReadImageFromClipboard_LinuxNoClipboardTool(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	stubClipboardHooks(t, "linux",
		notFoundPath("wl-paste", "xclip"),
		func(string, ...string) ([]byte, error) { return nil, errors.New("must not be called") },
	)

	_, err := ReadImageFromClipboard()
	if !errors.Is(err, ErrClipboardUnsupported) {
		t.Errorf("err = %v, want ErrClipboardUnsupported when neither wl-paste nor xclip exists", err)
	}
}
