package undopicker_test

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/undopicker"
)

func TestViewClosedRendersEmpty(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries())
	if got := m.View(80); got != "" {
		t.Errorf("closed picker should render empty, got %q", got)
	}
}

func TestViewShowsPreviewsNewestFirst(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	out := m.View(80)

	third := strings.Index(out, "third question")
	second := strings.Index(out, "second question")
	first := strings.Index(out, "first question")
	if third < 0 || second < 0 || first < 0 {
		t.Fatalf("view is missing prompt previews:\n%s", out)
	}
	if !(third < second && second < first) {
		t.Errorf("previews not in newest-first order: third@%d second@%d first@%d\n%s", third, second, first, out)
	}
}

func TestViewDisabledRowShowsCompactionHint(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	out := m.View(80)

	if !strings.Contains(out, "compaction") {
		t.Errorf("view does not show the compaction hint for the disabled row:\n%s", out)
	}
}

func TestViewFooterHint(t *testing.T) {
	t.Parallel()

	m := undopicker.New(pickerEntries()).Open()
	out := m.View(80)
	if !strings.Contains(out, "enter") || !strings.Contains(out, "esc") {
		t.Errorf("view is missing the footer navigation hint:\n%s", out)
	}
}

func TestViewEmptyEntries(t *testing.T) {
	t.Parallel()

	m := undopicker.New(nil).Open()
	out := m.View(80)
	if !strings.Contains(out, "Nothing") {
		t.Errorf("empty picker should say there is nothing to undo:\n%s", out)
	}
}
