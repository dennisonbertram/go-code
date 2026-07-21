package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// readClipboardImage is the clipboard-image read used by Ctrl-V paste. It is
// a package-level seam (defaulting to the slice-1 reader) so tests can stub
// platform clipboard access.
var readClipboardImage = ReadImageFromClipboard

// encodeImageAttachments reads each attachment chip's temp file and returns
// the base64 wire blocks for the run request (epic #818 slice 3).
func encodeImageAttachments(chips []inputarea.Attachment) ([]runAttachment, error) {
	out := make([]runAttachment, 0, len(chips))
	for _, chip := range chips {
		data, err := os.ReadFile(chip.Path)
		if err != nil {
			return nil, fmt.Errorf("read attached image %s: %w", filepath.Base(chip.Path), err)
		}
		mediaType := chip.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		out = append(out, runAttachment{
			Type:      "image",
			MediaType: mediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
		})
	}
	return out, nil
}

// clipboardImageReadMsg carries the result of an async clipboard image read
// back into the update loop.
type clipboardImageReadMsg struct {
	img ClipboardImage
	err error
}

// pasteImageCmd reads an image from the system clipboard off the main loop.
func pasteImageCmd() tea.Cmd {
	return func() tea.Msg {
		img, err := readClipboardImage()
		return clipboardImageReadMsg{img: img, err: err}
	}
}

// imageModalityError returns nil when the currently selected model may accept
// image input, or an actionable error when the fetched catalog data marks it
// text-only. Unknown data (model not in the fetched list, no modalities field
// from an older server, offline) is treated as allowed: this is a best-effort
// client-side pre-flight; the server enforces the gate when runs carry image
// attachments (epic #818 slice 3).
func (m Model) imageModalityError() error {
	modelID, provider := m.effectiveModelAndProvider()
	for _, e := range m.serverModels {
		if e.Provider != provider || e.ID != modelID {
			continue
		}
		for _, mod := range e.Modalities {
			if mod == "image" {
				return nil
			}
		}
		if len(e.Modalities) == 0 {
			// Entry carries no modality information — unknown, allow.
			return nil
		}
		return fmt.Errorf("%s does not support image input — switch to a model with the image modality to paste", modelID)
	}
	return nil
}

// clipboardPasteErrorHint maps the slice-1 typed clipboard errors to short
// inline status messages.
func clipboardPasteErrorHint(err error) string {
	switch {
	case errors.Is(err, ErrClipboardNoImage):
		return "no image on the clipboard"
	case errors.Is(err, ErrClipboardHeadless):
		return "image paste unavailable in headless mode"
	case errors.Is(err, ErrClipboardUnsupported):
		return "image paste unsupported on this platform (macOS: osascript; Linux: wl-paste/xclip)"
	default:
		return "clipboard image read failed: " + err.Error()
	}
}

// handleClipboardImageRead applies a clipboardImageReadMsg: on success the
// image becomes an attachment chip in the input area; on failure the typed
// error is shown inline on the status bar.
func (m *Model) handleClipboardImageRead(msg clipboardImageReadMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatusMsg(clipboardPasteErrorHint(msg.err))
	}
	m.input = m.input.AddAttachment(inputarea.Attachment{
		Path:      msg.img.Path,
		MediaType: msg.img.MediaType,
	})
	return m.setStatusMsg(fmt.Sprintf("image attached [image #%d] — backspace on empty input removes it", len(m.input.Attachments())))
}
