package inputarea

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Attachment is a pending non-text attachment shown as a placeholder chip in
// the input area. Path is a temp file owned by the component: it is deleted
// (with its directory) when the chip is removed. MediaType is the MIME type
// of the payload (e.g. "image/png").
type Attachment struct {
	Path      string
	MediaType string
}

// removeAttachmentFiles deletes an attachment's temp file directory. It is a
// package-level seam so tests can observe cleanup without touching the real
// file system.
var removeAttachmentFiles = func(a Attachment) {
	if a.Path == "" {
		return
	}
	os.RemoveAll(filepath.Dir(a.Path))
}

// chipStyle renders attachment placeholder tokens as subtle chips.
var chipStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#FAFAFA"}).
	Background(lipgloss.AdaptiveColor{Light: "#D7D7D7", Dark: "#4A4A4A"})

// AddAttachment appends a pending attachment chip. Value semantics.
func (m Model) AddAttachment(a Attachment) Model {
	m.attachments = append(m.attachments, a)
	return m
}

// Attachments returns a copy of the pending attachments, oldest first.
func (m Model) Attachments() []Attachment {
	out := make([]Attachment, len(m.attachments))
	copy(out, m.attachments)
	return out
}

// WithAttachments replaces the pending attachments with the given list (a
// copy is stored). Used to carry chips across component re-creation, e.g. on
// terminal resize. Value semantics.
func (m Model) WithAttachments(atts []Attachment) Model {
	m.attachments = make([]Attachment, len(atts))
	copy(m.attachments, atts)
	return m
}

// removeLastAttachment drops the most recent chip and deletes its temp files.
func (m *Model) removeLastAttachment() {
	if len(m.attachments) == 0 {
		return
	}
	last := m.attachments[len(m.attachments)-1]
	m.attachments = m.attachments[:len(m.attachments)-1]
	removeAttachmentFiles(last)
}

// chipsView renders the chip row ("" when there are no attachments). Chips
// are numbered contiguously from 1 in attach order, e.g. "[image #1]".
func (m Model) chipsView() string {
	if len(m.attachments) == 0 {
		return ""
	}
	chips := make([]string, len(m.attachments))
	for i, a := range m.attachments {
		kind := "image"
		if mt := a.MediaType; mt != "" && !strings.HasPrefix(mt, "image/") {
			kind = "file"
		}
		chips[i] = chipStyle.Render(fmt.Sprintf("[%s #%d]", kind, i+1))
	}
	return strings.Join(chips, " ")
}
