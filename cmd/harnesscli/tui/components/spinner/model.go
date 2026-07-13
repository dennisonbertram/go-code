// Package spinner implements the TUI-024 thinking spinner with rotating verbs.
// It provides an immutable BubbleTea-style Model that advances frame-by-frame
// and rotates through a pool of whimsical verbs (e.g. "Thinking", "Reasoning").
package spinner

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// frames are the 6 animation frames for the thinking spinner.
// These are star/asterisk glyphs, not the braille frames in theme.go.
var frames = []string{"✶", "·", "✻", "✽", "✳", "✢"}

// verbRotateEvery controls how many Tick() calls trigger a verb rotation.
const verbRotateEvery = 8

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

// Model is the immutable thinking spinner state.
// All mutation methods return a new Model value — never modify in place.
// This keeps it safe for use in BubbleTea's single-goroutine Update().
type Model struct {
	frame            int        // current frame index [0, len(frames))
	verb             string     // current displayed verb
	action           string     // current activity label (e.g. running tool name); overrides verb when set
	startTime        time.Time  // when spinner started (for duration)
	tokens           int        // token count stored on Stop()
	active           bool       // true while spinner is running
	done             bool       // true after Stop()
	tickCount        int        // total ticks received (used for verb rotation)
	completionFrames int        // ticks remaining to show completion line after Stop()
	rng              *rand.Rand // seeded rng for deterministic testing

	// Seed is the seed used to create rng. Exposed so tests can inspect it.
	Seed int64

	// testVerbs overrides DefaultVerbs when non-nil. For testing only.
	testVerbs []string
}

// New creates a new Model with the given seed. The seed makes verb selection
// deterministic which is essential for snapshot and regression tests.
func New(seed int64) Model {
	return Model{
		Seed: seed,
		rng:  rand.New(rand.NewSource(seed)), //nolint:gosec // not for crypto
	}
}

// verbPool returns the verb pool in effect: testVerbs override if set,
// otherwise DefaultVerbs.
func (m Model) verbPool() []string {
	if m.testVerbs != nil {
		return m.testVerbs
	}
	return DefaultVerbs
}

// Start activates the spinner, records the start time, and picks an initial verb.
// Returns a new Model; the receiver is unchanged.
func (m Model) Start() Model {
	m.active = true
	m.done = false
	m.startTime = time.Now()
	m.frame = 0
	m.tickCount = 0
	m.verb = pickVerb(m.verbPool(), m.rng)
	return m
}

// Tick advances the animation by one frame and potentially rotates the verb.
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
	m.frame = (m.frame + 1) % len(frames)
	// Rotate verb every verbRotateEvery ticks.
	if m.tickCount%verbRotateEvery == 0 {
		m.verb = pickVerb(m.verbPool(), m.rng)
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

// SetAction sets the current activity label (e.g. the name of a running tool
// or a short step description). When non-empty, View() displays it in place
// of the rotating verb so the user sees what is actually happening rather
// than a generic placeholder. Pass "" to fall back to verb rotation.
// Returns a new Model; the receiver is unchanged.
func (m Model) SetAction(action string) Model {
	m.action = action
	return m
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
//   - Active: "✻ Thinking... (esc to interrupt)", or with a known action,
//     "✻ Running bash (esc to interrupt)"; a duration is inserted once
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

	currentFrame := frames[m.frame]

	// Build the base text. When a current action is known, show it instead of
	// the rotating verb so the user sees what is actually happening.
	label := m.verb + "..."
	if m.action != "" {
		label = m.action
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

	style := lipgloss.NewStyle().Faint(true)
	rendered := style.Render(base)

	// Clamp to width using MaxWidth.
	if width < 80 {
		rendered = lipgloss.NewStyle().MaxWidth(width).Render(base)
	}

	return rendered
}

// CompletionLine returns the one-line completion summary shown after the spinner stops.
//
// Format: "✻ Worked for 5s" or "✻ Worked for 1m 30s"
func (m Model) CompletionLine(seconds float64) string {
	glyph := frames[m.frame%len(frames)]
	duration := formatSeconds(seconds)
	line := glyph + " Worked for " + duration
	return lipgloss.NewStyle().Faint(true).Render(line)
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
