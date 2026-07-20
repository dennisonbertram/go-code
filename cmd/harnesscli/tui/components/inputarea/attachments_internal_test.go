package inputarea

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubAttachmentCleanup replaces the temp-file cleanup seam and records every
// attachment it was asked to delete.
func stubAttachmentCleanup(t *testing.T) *[]Attachment {
	t.Helper()
	removed := new([]Attachment)
	old := removeAttachmentFiles
	removeAttachmentFiles = func(a Attachment) { *removed = append(*removed, a) }
	t.Cleanup(func() { removeAttachmentFiles = old })
	return removed
}

func testAttachment(path string) Attachment {
	return Attachment{Path: path, MediaType: "image/png"}
}

// TestAddAttachmentRendersChip verifies that adding an attachment shows a
// placeholder chip in the input area view and records the attachment state.
func TestAddAttachmentRendersChip(t *testing.T) {
	m := New(80)
	m = m.AddAttachment(testAttachment("/tmp/go-code-clipboard-a/clipboard.png"))

	atts := m.Attachments()
	if len(atts) != 1 {
		t.Fatalf("Attachments() len = %d, want 1", len(atts))
	}
	if atts[0].Path != "/tmp/go-code-clipboard-a/clipboard.png" {
		t.Errorf("attachment path = %q", atts[0].Path)
	}
	if atts[0].MediaType != "image/png" {
		t.Errorf("attachment media type = %q", atts[0].MediaType)
	}
	if !strings.Contains(m.View(), "[image #1]") {
		t.Errorf("View() must render the chip placeholder [image #1], got:\n%s", m.View())
	}
}

// TestAttachmentChipsNumberedContiguously verifies chips render in order with
// contiguous 1-based numbering.
func TestAttachmentChipsNumberedContiguously(t *testing.T) {
	m := New(80)
	m = m.AddAttachment(testAttachment("/tmp/a/clipboard.png"))
	m = m.AddAttachment(testAttachment("/tmp/b/clipboard.png"))

	if len(m.Attachments()) != 2 {
		t.Fatalf("Attachments() len = %d, want 2", len(m.Attachments()))
	}
	view := m.View()
	if !strings.Contains(view, "[image #1]") || !strings.Contains(view, "[image #2]") {
		t.Errorf("View() must render [image #1] and [image #2], got:\n%s", view)
	}
	// #1 must appear before #2.
	if strings.Index(view, "[image #1]") > strings.Index(view, "[image #2]") {
		t.Errorf("chips must render in attach order, got:\n%s", view)
	}
}

// TestBackspaceWithTextKeepsChips verifies Backspace edits text normally when
// the buffer is non-empty and never touches attachments.
func TestBackspaceWithTextKeepsChips(t *testing.T) {
	removed := stubAttachmentCleanup(t)
	m := New(80).SetValue("hi")
	m = m.AddAttachment(testAttachment("/tmp/a/clipboard.png"))

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2

	if m.Value() != "h" {
		t.Errorf("Backspace with text must delete a character, got %q", m.Value())
	}
	if len(m.Attachments()) != 1 {
		t.Errorf("Backspace with text must not remove chips, have %d", len(m.Attachments()))
	}
	if len(*removed) != 0 {
		t.Errorf("cleanup must not run while text remains, removed %d", len(*removed))
	}
}

// TestBackspaceOnEmptyRemovesLastChip verifies Backspace on an empty buffer
// removes the most recent chip and deletes its temp files (LIFO order).
func TestBackspaceOnEmptyRemovesLastChip(t *testing.T) {
	removed := stubAttachmentCleanup(t)
	m := New(80)
	m = m.AddAttachment(testAttachment("/tmp/a/clipboard.png"))
	m = m.AddAttachment(testAttachment("/tmp/b/clipboard.png"))

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2

	if len(m.Attachments()) != 1 {
		t.Fatalf("after one Backspace, Attachments() len = %d, want 1", len(m.Attachments()))
	}
	if m.Attachments()[0].Path != "/tmp/a/clipboard.png" {
		t.Errorf("the oldest chip must remain, got %q", m.Attachments()[0].Path)
	}
	if len(*removed) != 1 || (*removed)[0].Path != "/tmp/b/clipboard.png" {
		t.Errorf("cleanup must delete the removed chip's temp dir, removed %+v", *removed)
	}
	view := m.View()
	if strings.Contains(view, "[image #2]") {
		t.Errorf("removed chip must not render, got:\n%s", view)
	}
	if !strings.Contains(view, "[image #1]") {
		t.Errorf("remaining chip must still render as [image #1], got:\n%s", view)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2
	if len(m.Attachments()) != 0 {
		t.Fatalf("after two Backspaces, Attachments() len = %d, want 0", len(m.Attachments()))
	}
	if len(*removed) != 2 || (*removed)[1].Path != "/tmp/a/clipboard.png" {
		t.Errorf("second Backspace must delete the first chip's temp dir, removed %+v", *removed)
	}
	if strings.Contains(m.View(), "[image #") {
		t.Errorf("no chips may render once all are removed, got:\n%s", m.View())
	}
}

// TestBackspaceOnEmptyWithoutChipsIsNoOp verifies Backspace on a truly empty
// input (no text, no chips) does nothing and never invokes cleanup.
func TestBackspaceOnEmptyWithoutChipsIsNoOp(t *testing.T) {
	removed := stubAttachmentCleanup(t)
	m := New(80)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2

	if m.Value() != "" {
		t.Errorf("value must stay empty, got %q", m.Value())
	}
	if len(m.Attachments()) != 0 {
		t.Errorf("no attachments may appear, got %d", len(m.Attachments()))
	}
	if len(*removed) != 0 {
		t.Errorf("cleanup must not run without chips, removed %d", len(*removed))
	}
}

// TestAttachmentsReturnsCopy verifies the returned slice is a defensive copy
// so callers cannot mutate component state.
func TestAttachmentsReturnsCopy(t *testing.T) {
	m := New(80)
	m = m.AddAttachment(testAttachment("/tmp/a/clipboard.png"))

	atts := m.Attachments()
	atts[0].Path = "/tmp/evil"

	if m.Attachments()[0].Path != "/tmp/a/clipboard.png" {
		t.Error("mutating the returned slice must not change component state")
	}
}

// TestChipRowDoesNotEatText verifies the text buffer and prompt still render
// alongside the chip row.
func TestChipRowDoesNotEatText(t *testing.T) {
	m := New(80).SetValue("hello")
	m = m.AddAttachment(testAttachment("/tmp/a/clipboard.png"))

	view := m.View()
	if !strings.Contains(view, "[image #1]") {
		t.Errorf("chip row missing, got:\n%s", view)
	}
	if !strings.Contains(view, "hello") {
		t.Errorf("text buffer must still render, got:\n%s", view)
	}
}
