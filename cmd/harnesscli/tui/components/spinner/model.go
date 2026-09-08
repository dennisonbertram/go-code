// Package spinner implements the TUI thinking spinner. It provides an immutable
// BubbleTea-style Model that advances frame-by-frame while displaying a label
// describing what the run is actually doing.
//
// The label is supplied by the caller through SetAction and changes only when
// the run's state changes — never on a timer. See issue #1415.
package spinner

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// pulse is the glyph sequence, ordered by visual weight and ping-ponged so the
// cycle grows and shrinks — a breath rather than a tick. It uses the same six
// star/asterisk glyphs as before (not the braille frames in theme.go); only
// their order changed. The previous order, ✶ · ✻ ✽ ✳ ✢, jumped from the
// heaviest glyph straight to the lightest, which reads as a stutter at any
// speed. The extremes are not repeated back-to-back at the turn. Issue #1420.
var pulse = []string{"·", "✢", "✳", "✻", "✽", "✶", "✽", "✻", "✳", "✢"}

// holds is how many ticks each step of the pulse is displayed for. The
// animation lingers at the top and bottom of the breath (3 ticks = 360ms) and
// passes quickly through the middle (1 tick = 120ms). That unevenness is the
// easing: a flat cadence is what made the old spinner read as mechanical.
//
// The tick rate itself deliberately stays at 120ms (tui.SpinnerInterval),
// because the same tick redraws the elapsed-time counter. Slowing the timer
// would slow the clock; gating advance slows only the glyph.
//
// Sum: 18 ticks, about 2.16s per cycle, against 720ms before.
var holds = []int{3, 2, 1, 1, 2, 3, 2, 1, 1, 2}

// durationThreshold is the elapsed time after which the spinner shows a duration.
const durationThreshold = 2 * time.Second

// completionFramesDefault is how many Tick() calls to keep showing the
// completion line after Stop(). At ~100ms per tick this is roughly 1 second.
const completionFramesDefault = 10

// CancelHint is the persistent hint appended to the active spinner line so
// the user always knows how to interrupt the current run.
const CancelHint = "(esc to interrupt)"

// SpinnerTickMsg triggers a spinner frame advance.
// This is the local equivalent of tui.SpinnerTickMsg; spinner-specific
// so the package has no import cycle with the parent tui package.
type SpinnerTickMsg struct{ T time.Time }

// Styles groups the styles used to render the spinner. Obtain defaults via
// DefaultStyles() and override individual fields.
type Styles struct {
	Dim lipgloss.Style // spinner line and completion summary
}

// DefaultStyles returns the styling the spinner used before theming: faint,
// no color.
func DefaultStyles() Styles {
	return Styles{Dim: lipgloss.NewStyle().Faint(true)}
}

// Model is the immutable thinking spinner state.
// All mutation methods return a new Model value — never modify in place.
// This keeps it safe for use in BubbleTea's single-goroutine Update().
type Model struct {
	step             int       // index into pulse
	stepTicks        int       // ticks already spent on the current step
	action           string    // what the run is currently doing; empty falls back to fallbackLabel
	startTime        time.Time // when spinner started (for duration)
	tokens           int       // token count stored on Stop()
	active           bool      // true while spinner is running
	done             bool      // true after Stop()
	tickCount        int       // total ticks received
	completionFrames int       // ticks remaining to show completion line after Stop()
	// Seed is retained only so the many existing New(seed) call sites keep
	// compiling. Nothing reads it: the label comes from run state, not from a
	// random source, so rendering is deterministic without a seed. Issue #1415.
	Seed int64

	// styles overrides DefaultStyles when non-nil (theme injection point,
	// epic #810).
	styles *Styles
}

// New creates a new Model. seed is ignored — it is kept only for call-site
// compatibility, since rendering no longer depends on randomness. See Model.Seed.
func New(seed int64) Model {
	return Model{Seed: seed}
}

// Start activates the spinner and records the start time.
// Returns a new Model; the receiver is unchanged.
func (m Model) Start() Model {
	m.active = true
	m.done = false
	m.startTime = time.Now()
	m.step = 0
	m.stepTicks = 0
	m.tickCount = 0
	return m
}

// Tick advances the animation by one frame. The label is deliberately untouched:
// it changes only when the caller reports a new action.
// When the spinner is done and completionFrames > 0, decrements completionFrames
// toward silence. Has no effect if neither active nor in completion mode.
// Returns a new Model; the receiver is unchanged.
func (m Model) Tick() Model {
	// Handle completion countdown when done.
	if m.done {
		if m.completionFrames > 0 {
			m.completionFrames--
		}
		return m
	}

	if !m.active {
		return m
	}
	m.tickCount++

	// Advance only once the current step has been held for its full duration.
	m.stepTicks++
	if m.stepTicks >= holds[m.step] {
		m.stepTicks = 0
		m.step = (m.step + 1) % len(pulse)
	}
	return m
}

// Stop deactivates the spinner and records the final token count.
// Sets completionFrames to completionFramesDefault so the completion line
// remains visible for N ticks before going silent.
// Returns a new Model; the receiver is unchanged.
func (m Model) Stop(tokens int) Model {
	m.active = false
	m.done = true
	m.tokens = tokens
	m.completionFrames = completionFramesDefault
	return m
}

// SetAction sets what the run is currently doing (e.g. "Running bash",
// "Writing response"). This is the label View() renders. Pass "" only when
// nothing is known, which falls back to fallbackLabel.
// Returns a new Model; the receiver is unchanged.
func (m Model) SetAction(action string) Model {
	m.action = action
	return m
}

// WithStyles replaces the styles used to render the spinner (theme injection
// point, epic #810). Returns a new Model; the receiver is unchanged.
func (m Model) WithStyles(s Styles) Model {
	m.styles = &s
	return m
}

func (m Model) stylesOrDefault() Styles {
	if m.styles == nil {
		return DefaultStyles()
	}
	return *m.styles
}

// Glyph returns the animation character for the current step of the pulse.
func (m Model) Glyph() string {
	if len(pulse) == 0 {
		return ""
	}
	return pulse[m.step%len(pulse)]
}

// IsActive returns true while the spinner is running (between Start and Stop).
func (m Model) IsActive() bool { return m.active }

// IsDone returns true after Stop() has been called.
func (m Model) IsDone() bool { return m.done }

// ShowsCompletion returns true when the spinner is done AND still within the
// completion display window (completionFrames > 0). Once completionFrames
// reaches 0 via Tick() calls, this returns false and View() goes silent.
func (m Model) ShowsCompletion() bool { return m.done && m.completionFrames > 0 }

// ElapsedSeconds returns the number of seconds since Start() was called.
// Returns 0 if the spinner has not been started (startTime is zero value).
func (m Model) ElapsedSeconds() float64 {
	if m.startTime.IsZero() {
		return 0
	}
	return time.Since(m.startTime).Seconds()
}

// View renders the spinner as a single line. The width parameter controls the
// maximum character width; the view degrades gracefully at narrow widths.
//
// States:
//   - Active: "✻ Running bash (esc to interrupt)"; a duration is inserted once
//     durationThreshold passes.
//   - ShowsCompletion() true: CompletionLine using ElapsedSeconds().
//   - Done and silent (completionFrames == 0): returns "".
func (m Model) View(width int) string {
	if width <= 0 {
		width = 80
	}

	// Completion mode: show the finalized line for N frames, then go silent.
	if m.done {
		if m.ShowsCompletion() {
			return m.CompletionLine(m.ElapsedSeconds())
		}
		// Silent after completion window expires.
		return ""
	}

	currentFrame := m.Glyph()

	// The label states what is actually happening. No ellipsis: "Running bash"
	// is a fact, and trailing dots would only suggest vagueness it does not have.
	label := m.action
	if label == "" {
		label = fallbackLabel
	}
	base := currentFrame + " " + label

	// Append duration if we've exceeded the threshold.
	if m.active && !m.startTime.IsZero() {
		elapsed := time.Since(m.startTime)
		if elapsed >= durationThreshold {
			base += " " + formatDuration(elapsed)
		}
	}

	// Always surface how to cancel while active.
	if m.active {
		base += " " + CancelHint
	}

	// At narrow widths the label yields before the cancel hint does. Truncating
	// from the right would eat "(esc to interrupt)" first, leaving the user
	// staring at "(esc to inter" with no way to know how to stop the run — the
	// hint is the one part of this line that is actionable. Labels grew long
	// enough to hit this when they became truthful ("Waiting for gpt-4.1-mini"
	// rather than "Computing..."), so the trade-off is now worth making
	// explicit. Issue #1415.
	if lipgloss.Width(base) > width {
		base = shortenLabel(currentFrame, label, base, width)
	}

	style := m.stylesOrDefault().Dim
	rendered := style.Render(base)

	// Final clamp: even a shortened line cannot exceed the terminal.
	if lipgloss.Width(base) > width {
		rendered = lipgloss.NewStyle().MaxWidth(width).Render(base)
	}

	return rendered
}

// shortenLabel rebuilds an over-long spinner line so the cancel hint survives.
// It first drops the duration, then truncates the label itself, and gives up
// only when even "<glyph> <hint>" will not fit — at which point the caller's
// MaxWidth clamp takes over.
func shortenLabel(glyph, label, full string, width int) string {
	withoutDuration := glyph + " " + label + " " + CancelHint
	if lipgloss.Width(withoutDuration) <= width {
		return withoutDuration
	}

	// Budget for the label: width minus the glyph, the hint, and the two spaces
	// separating them.
	budget := width - lipgloss.Width(glyph) - lipgloss.Width(CancelHint) - 2
	if budget < 4 {
		// Not even a stub of a label fits; the hint alone is more useful.
		return glyph + " " + CancelHint
	}
	label = truncateToWidth(label, budget)
	return glyph + " " + label + " " + CancelHint
}

// truncateToWidth shortens label to at most budget terminal columns, appending
// an ellipsis when it cuts.
//
// It measures display columns rather than runes. Counting runes was the bug in
// issue #1432: CJK and emoji occupy two columns each, so a rune-budgeted label
// could be twice its allowance in columns, overrun the line, and get clipped
// from the right — taking the cancel hint with it. ASCII hid this completely,
// because there columns and runes are the same number.
//
// Rune-by-rune accumulation is deliberate and imperfect: it does not segment
// grapheme clusters, so a combining mark or ZWJ emoji sequence can still be
// split. That is a strict improvement over rune counting and a much smaller
// change than full segmentation, which is out of scope until a real case
// appears.
func truncateToWidth(label string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if lipgloss.Width(label) <= budget {
		return label
	}

	const ellipsis = "\u2026"
	room := budget - lipgloss.Width(ellipsis)
	if room <= 0 {
		return ellipsis
	}

	var (
		out   []rune
		width int
	)
	for _, r := range label {
		w := lipgloss.Width(string(r))
		if width+w > room {
			break
		}
		out = append(out, r)
		width += w
	}
	return string(out) + ellipsis
}

// CompletionLine returns the one-line completion summary shown after the spinner stops.
//
// Format: "✻ Worked for 5s" or "✻ Worked for 1m 30s"
func (m Model) CompletionLine(seconds float64) string {
	glyph := m.Glyph()
	duration := formatSeconds(seconds)
	line := glyph + " Worked for " + duration
	return m.stylesOrDefault().Dim.Render(line)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// formatDuration formats a time.Duration into a parenthesised display string.
// Examples: "(2.3s)", "(1m 30s)"
func formatDuration(d time.Duration) string {
	return "(" + formatSeconds(d.Seconds()) + ")"
}

// formatSeconds formats a duration in seconds into a human-readable string.
// Under 60s: "2.3s" (one decimal place).
// 60s+:      "1m 30s".
func formatSeconds(s float64) string {
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	mins := int(s) / 60
	secs := int(s) % 60
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm %ds", mins, secs)
}
