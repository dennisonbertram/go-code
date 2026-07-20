package diffview

// Model renders a unified diff view for file changes.
type Model struct {
	// FilePath is the file being diffed.
	FilePath string
	// Diff is the unified diff content.
	Diff string
	// Width is the available rendering width.
	Width int
	// MaxLines controls the maximum number of diff lines rendered. 0 uses the
	// component default.
	MaxLines int
	// Styles overrides the render palette when non-nil (theme injection
	// point, epic #810); nil uses DefaultStyles().
	Styles *Styles
}

// New creates a new diff view model.
func New(filePath, diff string) Model {
	return Model{FilePath: filePath, Diff: diff}
}

// View renders the diff view through the package's shared renderer.
func (m Model) View() string {
	width := m.Width
	if width <= 0 {
		width = defaultWidth
	}
	maxLines := m.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	return View{
		Diff:     m.Diff,
		MaxLines: maxLines,
		Width:    width,
		Styles:   m.Styles,
	}.Render()
}
