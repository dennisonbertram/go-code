package statusbar

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Model is the status bar component displayed at the bottom of the TUI.
type Model struct {
	width    int
	model    string
	title    string // optional session title (see /title)
	workdir  string
	branch   string
	permMode string // "", "plan", "accept-edits", "auto-approve"
	mcpFails int
	running  bool
	costUSD  float64
	ctxUsed  int
	ctxTotal int
	styles   *Styles // nil = DefaultStyles()
}

// Styles groups the styles used to render status bar segments. The zero
// value is not useful; obtain defaults via DefaultStyles() and override
// individual fields.
type Styles struct {
	Dim  lipgloss.Style // separators, branch, workdir, cost, context meter
	Bold lipgloss.Style // model name, session title
	Warn lipgloss.Style // permission mode, MCP failures, high context usage
}

// DefaultStyles returns the styles the status bar used before theming:
// faint dim, bold, amber (#FFAF00) warnings.
func DefaultStyles() Styles {
	return Styles{
		Dim:  lipgloss.NewStyle().Faint(true),
		Bold: lipgloss.NewStyle().Bold(true),
		Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAF00")),
	}
}

// New creates a new status bar model for the given terminal width.
func New(width int) Model {
	return Model{width: width}
}

// SetStyles replaces the styles used to render the status bar (theme
// injection point, epic #810).
func (m *Model) SetStyles(s Styles) { m.styles = &s }

func (m Model) stylesOrDefault() Styles {
	if m.styles == nil {
		return DefaultStyles()
	}
	return *m.styles
}

func (m *Model) SetModel(name string)    { m.model = name }
func (m *Model) SetTitle(title string)   { m.title = title }
func (m *Model) SetWorkdir(path string)  { m.workdir = path }
func (m *Model) SetBranch(branch string) { m.branch = branch }
func (m *Model) SetPermMode(mode string) { m.permMode = mode }
func (m *Model) SetMCPFailures(n int)    { m.mcpFails = n }
func (m *Model) SetRunning(r bool)       { m.running = r }
func (m *Model) SetCost(usd float64)     { m.costUSD = usd }
func (m *Model) SetWidth(w int)          { m.width = w }

// SetContext records the current context-window usage so the status bar can
// render a compact meter (e.g. "◫ 28%/200K"). Both used and total must be
// positive for the segment to render; otherwise it is omitted.
func (m *Model) SetContext(used, total int) {
	m.ctxUsed = used
	m.ctxTotal = total
}

// View renders the status bar as a single line, degrading gracefully at any width.
func (m Model) View() string {
	styles := m.stylesOrDefault()
	w := m.width
	if w <= 0 {
		w = 80
	}

	// At very narrow widths (< 10) return a minimal placeholder.
	if w < 10 {
		return lipgloss.NewStyle().MaxWidth(w).Render(truncate("...", w))
	}

	sep := styles.Dim.Render(" ~ ")
	sepLen := 3 // plain rune width of " ~ "

	// Segments in priority order: model > title > context > running > cost > perm > branch > workdir > mcpFails
	type segment struct {
		text     string
		priority int // lower = higher priority (kept last when trimming)
	}
	var segs []segment

	if m.model != "" {
		segs = append(segs, segment{styles.Bold.Render(truncate(m.model, 24)), 1})
	}
	if m.title != "" {
		segs = append(segs, segment{styles.Bold.Render(truncate(m.title, 24)), 2})
	}
	if m.ctxUsed > 0 && m.ctxTotal > 0 {
		pct := int(float64(m.ctxUsed)/float64(m.ctxTotal)*100 + 0.5)
		text := fmt.Sprintf("◫ %d%%/%s", pct, formatCompactTokens(m.ctxTotal))
		style := styles.Dim
		if pct >= 80 {
			style = styles.Warn
		}
		segs = append(segs, segment{style.Render(text), 3})
	}
	if m.running {
		segs = append(segs, segment{styles.Dim.Render("..."), 4})
	}
	if m.costUSD > 0 {
		segs = append(segs, segment{styles.Dim.Render(fmt.Sprintf("$%.4f", m.costUSD)), 5})
	}
	if m.permMode != "" && m.permMode != "default" {
		segs = append(segs, segment{styles.Warn.Render("[" + m.permMode + "]"), 6})
	}
	if m.branch != "" {
		segs = append(segs, segment{styles.Dim.Render("(" + m.branch + ")"), 7})
	}
	if m.workdir != "" {
		dir := shortenPath(m.workdir, 20)
		segs = append(segs, segment{styles.Dim.Render(dir), 8})
	}
	if m.mcpFails > 0 {
		segs = append(segs, segment{styles.Warn.Render(fmt.Sprintf("%d MCP fail", m.mcpFails)), 9})
	}

	// Build line progressively, dropping lowest-priority segments when over width.
	for len(segs) > 0 {
		var texts []string
		for _, s := range segs {
			texts = append(texts, s.text)
		}
		line := strings.Join(texts, sep)
		// Measure plain rune width: count runes plus separators.
		plainLen := runeLen(line)
		// Account for sep overhead: (n-1) separators each sepLen wide.
		_ = plainLen
		// Use lipgloss MaxWidth to measure and trim.
		rendered := lipgloss.NewStyle().MaxWidth(w).Render(line)
		// If it fits within width (lipgloss handles ANSI lengths), we're done.
		if visibleWidth(rendered) <= w {
			return rendered
		}
		// Drop lowest priority segment (last in slice since segs is in priority order).
		segs = segs[:len(segs)-1]
		_ = sepLen
	}

	// Fallback: empty bar (all segments dropped).
	return lipgloss.NewStyle().Width(w).Render("")
}

// runeLen returns the number of runes in s (ignoring ANSI escape sequences).
func runeLen(s string) int {
	return len([]rune(stripANSI(s)))
}

// visibleWidth returns the display width of s after stripping ANSI sequences.
func visibleWidth(s string) int {
	return len([]rune(stripANSI(s)))
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "..."
}

// formatCompactTokens renders a token count in a compact human-readable form,
// e.g. 200000 -> "200K", 1500000 -> "1.5M", 900 -> "900".
func formatCompactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return trimTrailingZero(float64(n)/1_000_000) + "M"
	case n >= 1_000:
		return trimTrailingZero(float64(n)/1_000) + "K"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// trimTrailingZero formats v with one decimal place, dropping the decimal
// entirely when it is ".0" (e.g. 200.0 -> "200", 1.5 -> "1.5").
func trimTrailingZero(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

func shortenPath(path string, max int) string {
	if len(path) <= max {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		return ".../" + parts[len(parts)-1]
	}
	return truncate(path, max)
}
