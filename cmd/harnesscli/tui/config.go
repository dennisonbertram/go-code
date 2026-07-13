package tui

// TUIConfig holds configuration for the TUI mode.
type TUIConfig struct {
	// BaseURL is the harnessd server URL.
	BaseURL string
	// Model is the LLM model identifier.
	Model string
	// Workspace is the workspace root path.
	Workspace string
	// MaxSteps limits the number of agent steps.
	MaxSteps int
	// Theme selects the color theme.
	Theme string
	// EnableTUI controls whether BubbleTea mode is active (opt-in).
	EnableTUI bool
	// ColorProfile selects terminal color depth: "truecolor", "256", "ansi", "none".
	ColorProfile string
	// AltScreen uses the alternate screen buffer when true.
	AltScreen bool
	// ResumeConversationID, when non-empty, seeds the TUI's conversation ID at
	// startup so the run history is loaded and new prompts continue the
	// existing conversation instead of starting a new one.
	ResumeConversationID string
}

// DefaultTUIConfig returns a TUIConfig with sensible defaults.
func DefaultTUIConfig() TUIConfig {
	return TUIConfig{
		BaseURL:      "http://localhost:8080",
		MaxSteps:     8,
		EnableTUI:    false,
		ColorProfile: "truecolor",
		AltScreen:    true,
	}
}
