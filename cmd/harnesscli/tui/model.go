package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	harnessconfig "go-agent-harness/cmd/harnesscli/config"
	"go-agent-harness/cmd/harnesscli/tui/components/configpanel"
	"go-agent-harness/cmd/harnesscli/tui/components/contextgrid"
	"go-agent-harness/cmd/harnesscli/tui/components/costdisplay"
	"go-agent-harness/cmd/harnesscli/tui/components/helpdialog"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	"go-agent-harness/cmd/harnesscli/tui/components/interruptui"
	"go-agent-harness/cmd/harnesscli/tui/components/layout"
	"go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
	"go-agent-harness/cmd/harnesscli/tui/components/permissionspanel"
	"go-agent-harness/cmd/harnesscli/tui/components/profilepicker"
	"go-agent-harness/cmd/harnesscli/tui/components/sessionpicker"
	"go-agent-harness/cmd/harnesscli/tui/components/slashcomplete"
	"go-agent-harness/cmd/harnesscli/tui/components/spinner"
	"go-agent-harness/cmd/harnesscli/tui/components/statspanel"
	"go-agent-harness/cmd/harnesscli/tui/components/statusbar"
	"go-agent-harness/cmd/harnesscli/tui/components/thinkingbar"
	"go-agent-harness/cmd/harnesscli/tui/components/tooluse"
	"go-agent-harness/cmd/harnesscli/tui/components/transcriptexport"
	"go-agent-harness/cmd/harnesscli/tui/components/viewport"
)

// defaultExportDir returns a runtime-safe directory for transcript exports.
func defaultExportDir() string {
	return transcriptexport.DefaultOutputDir()
}

// defaultSessionConfigDir returns the directory where sessions.json is stored.
// Falls back to the current directory on error.
func defaultSessionConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "harnesscli")
}

type gatewayOption struct {
	ID    string
	Label string
	Desc  string
}

type apiKeyProvider struct {
	Name       string
	Configured bool
	APIKeyEnv  string
}

var gatewayOptions = []gatewayOption{
	{ID: "", Label: "Direct", Desc: "Use each model's native provider"},
	{ID: "openrouter", Label: "OpenRouter", Desc: "Route all models via openrouter.ai"},
}

// statusMsgDuration is how long a transient status message is shown.
const statusMsgDuration = 3 * time.Second

// Model is the root BubbleTea model for the TUI.
type Model struct {
	width  int
	height int
	layout layout.Layout
	theme  Theme
	config TUIConfig
	keys   KeyMap
	ready  bool

	// RunID is the current run being displayed.
	RunID string

	// conversationID is the stable identifier for the current conversation.
	// It is set to the first run's ID when no conversation_id is supplied,
	// and passed on all subsequent runs so the harness links them together.
	conversationID string

	// runActive is true while a run is in flight.
	runActive bool

	// cancelRun holds the cancel func from the SSE bridge; nil when no run is active.
	cancelRun func()

	// sseCh is the channel delivering SSE messages from the active run's bridge.
	// nil when no run is active.
	sseCh <-chan tea.Msg

	// lastEventID is the ID of the most recently delivered SSE event for the
	// active run (format "runID:seq" — see harness.ParseEventID). Used to
	// resume the stream via the Last-Event-ID header if the connection drops
	// mid-run, so the server can skip already-delivered history instead of
	// replaying it (see internal/server/http_runs.go).
	lastEventID string

	// sseReconnectAttempts counts how many automatic SSE reconnect attempts
	// have been made for the current run. Bounded by maxSSEReconnectAttempts;
	// once exhausted the stream is treated as terminally lost.
	sseReconnectAttempts int

	// toolExpanded tracks which tool calls are in the expanded view, keyed by
	// tool call ID. True = expanded, absent/false = collapsed.
	toolExpanded map[string]bool

	// activeToolCallID is the ID of the currently active/selected tool call,
	// used when toggling expansion via Ctrl+O.
	activeToolCallID string

	// toolNames tracks tool names by call ID for lifecycle updates.
	toolNames map[string]string

	// toolArgs tracks argument summaries by call ID for lifecycle updates.
	toolArgs map[string]string

	// toolTimers tracks per-call timing for lifecycle updates.
	toolTimers map[string]tooluse.Timer

	// toolViews tracks the latest render state per tool call so lifecycle
	// updates and ctrl+o can rerender through the shared component path.
	toolViews map[string]tooluse.Model

	// toolLineCounts tracks how many viewport lines belong to the latest
	// rendered block for a tool call when that block is currently at the tail.
	toolLineCounts map[string]int

	// toolLineStarts records the absolute start offset (in viewport lines) of
	// each tool card at the time it was first appended. This allows in-place
	// updates even when the card is no longer at the viewport tail (e.g. after
	// an assistant delta has been appended since the card was started).
	toolLineStarts map[string]int

	// toolLineOrder records the insertion order of call IDs so that when a
	// card's line count changes (delta), the start offsets of all later cards
	// can be shifted accordingly.
	toolLineOrder []string

	// renderedToolCallID is the tool call currently occupying the viewport tail.
	// It allows lifecycle updates to replace the last rendered tool block
	// instead of appending duplicate start/completed rows.
	renderedToolCallID string

	// lastAssistantText accumulates all assistant deltas for the current run.
	lastAssistantText string

	// responseStarted tracks whether the first assistant delta for the current
	// run has been written to the viewport. On the first delta we call
	// the messagebubble renderer and then replace only the active assistant tail.
	responseStarted bool

	// activeAssistantLineCount tracks how many viewport lines belong to the
	// currently streaming assistant bubble.
	activeAssistantLineCount int

	// thinkingText accumulates reasoning deltas for the current turn.
	thinkingText string

	// thinkingBar renders the visible thinking indicator above the input area.
	thinkingBar thinkingbar.Model

	// interruptBanner renders the two-stage interrupt confirmation banner above
	// the input area when the user presses Ctrl+C during an active run.
	interruptBanner interruptui.Model

	// transcript accumulates entries for the current session (used by /export).
	transcript []transcriptexport.TranscriptEntry

	// usageDataPoints accumulates per-day DataPoints for the stats panel.
	// Updated whenever a usage.delta SSE event is received.
	usageDataPoints []statspanel.DataPoint

	// cumulativeCostUSD is the running total cost for the current session.
	cumulativeCostUSD float64

	// totalTokens is the cumulative token count for the context grid.
	totalTokens int

	// overlayActive is true when an overlay (help, context, stats, etc.) is open.
	overlayActive bool

	// activeOverlay identifies which overlay is currently displayed.
	// Valid values: "", "help", "stats", "context".
	activeOverlay string

	// dashboard holds transient state for the multi-run dashboard overlay.
	dashboard dashboardState

	// statusMsg is a transient overlay message shown on the status bar.
	statusMsg string
	// statusMsgExpiry is when statusMsg should be cleared.
	statusMsgExpiry time.Time

	// commandRegistry holds the dispatch table for slash commands.
	commandRegistry *CommandRegistry

	// autocompleteProvider is stored here so it can be re-wired whenever the
	// input component is re-created (e.g. on WindowSizeMsg).
	autocompleteProvider inputarea.AutocompleteProvider

	// slashComplete is the autocomplete dropdown shown when the user types "/".
	slashComplete slashcomplete.Model

	// modelSwitcher is the 2-level model + reasoning overlay.
	modelSwitcher modelswitcher.Model

	// selectedModel is the currently active model ID.
	selectedModel string

	// selectedProvider is the currently active provider name.
	selectedProvider string

	// selectedReasoningEffort is the currently active reasoning effort.
	selectedReasoningEffort string

	// selectedGateway is the active routing gateway ("" = direct, "openrouter" = OpenRouter).
	selectedGateway string
	// gatewaySelected is the cursor index in the gatewayOptions overlay.
	gatewaySelected int

	// apiKeyProviders holds the provider list from the server for the /keys overlay.
	apiKeyProviders []apiKeyProvider
	// apiKeyCursor is the list cursor in the /keys overlay.
	apiKeyCursor int
	// apiKeyInput is the text being typed in the /keys overlay input mode.
	apiKeyInput string
	// apiKeyInputMode is true when the user is typing a key value.
	apiKeyInputMode bool
	// pendingAPIKeys holds keys loaded from config or entered via /keys, replayed on Init().
	pendingAPIKeys map[string]string
	// envAPIKeys holds keys read from the shell environment at startup.
	// These are used only for availability display — not replayed to the server
	// because the server already reads its own environment.
	envAPIKeys map[string]string

	// modelConfigMode is true when the Level-1 config panel is showing.
	modelConfigMode bool
	// modelConfigEntry is the model being configured.
	modelConfigEntry modelswitcher.ModelEntry
	// modelConfigSection is the focused section index (0=gateway, 1=apikey, 2=reasoning).
	modelConfigSection int
	// modelConfigGatewayCursor is the gateway option cursor in the config panel.
	modelConfigGatewayCursor int
	// modelConfigReasoningCursor is the reasoning effort cursor in the config panel.
	modelConfigReasoningCursor int
	// modelConfigKeyInputMode is true when typing a key value in the config panel.
	modelConfigKeyInputMode bool
	// modelConfigKeyInput is the text being typed.
	modelConfigKeyInput string

	// Profile picker component and selection state.
	profilePicker   profilepicker.Model
	selectedProfile string // name of profile selected for next run (not persisted)

	// Session picker component and persistent session store.
	sessionPicker sessionpicker.Model
	sessionStore  *SessionStore

	// permissionsPanel shows the client-local session permission rules.
	permissionsPanel permissionspanel.Model

	// pendingLastMsg holds the most-recently submitted user message (up to 60
	// chars) so RunStartedMsg can record it on the session entry as LastMsg.
	pendingLastMsg string

	// Search overlay state (for /search and /history commands).
	searchResults     []SearchResult
	searchQuery       string
	searchSelectedIdx int
	// searchIsHistory distinguishes /search (transcript) from /history (sessions).
	searchIsHistory bool

	// Components
	statusBar   statusbar.Model
	vp          viewport.Model
	input       inputarea.Model
	helpDialog  helpdialog.Model
	contextGrid contextgrid.Model
	statsPanel  statspanel.Model

	// costDisplay is the /cost overlay showing running token usage and cost.
	costDisplay costdisplay.Model

	// configPanel is the /config overlay: a read-only view of the current
	// session's configuration (base URL, model, workspace, etc.).
	configPanel configpanel.Model

	// spinner drives the persistent in-progress indicator shown while a run
	// is active and the thinking bar has no streamed text to display (e.g.
	// while a tool call is executing). Shows the current action (running
	// tool name, when known) plus a cancel hint.
	spinner spinner.Model

	// historyStore holds the persistent command history across window resizes
	// and is saved to ~/.config/harnesscli/config.json on every submit.
	historyStore inputarea.History

	// askUser holds the state for an in-progress AskUserQuestion interaction.
	// askUser.active is true when the overlay is shown.
	askUser askUserState

	// toolApproval holds the state for an in-progress tool-approval decision.
	// toolApproval.active is true when the overlay is shown.
	toolApproval toolApprovalState

	// planMode tracks whether plan mode is toggled on (ctrl+o when idle).
	planMode bool

	// pluginsDir is the directory from which custom slash-command plugins are loaded.
	// Defaults to ~/.config/harnesscli/plugins when empty.
	pluginsDir string

	// pluginWarnings collects warnings produced when loading and registering plugins
	// (e.g. load errors, name collisions with builtins).
	pluginWarnings []string
}

// New creates a new root Model.
// spinnerSeed returns the spinner verb seed: the configured value when non-zero
// (deterministic, used by tests), otherwise a time-based seed for variety.
func spinnerSeed(cfg TUIConfig) int64 {
	if cfg.SpinnerSeed != 0 {
		return cfg.SpinnerSeed
	}
	return time.Now().UnixNano()
}

func New(cfg TUIConfig) Model {
	m := Model{
		config:          cfg,
		keys:            DefaultKeyMap(),
		theme:           DefaultTheme(),
		contextGrid:     contextgrid.New(),
		statsPanel:      statspanel.New(nil),
		costDisplay:     costdisplay.New(),
		spinner:         spinner.New(spinnerSeed(cfg)),
		thinkingBar:     thinkingbar.New(),
		interruptBanner: interruptui.New(),
		selectedModel:   cfg.Model,
	}
	m.modelSwitcher = modelswitcher.New(cfg.Model)
	// Initialize history store with defaults.
	m.historyStore = inputarea.NewHistory(100)
	// Load starred models, gateway, API keys, and command history from persistent config.
	if persistCfg, err := harnessconfig.Load(); err == nil {
		m.modelSwitcher = m.modelSwitcher.WithStarred(persistCfg.StarredModels)
		m.selectedGateway = persistCfg.Gateway
		m.pendingAPIKeys = persistCfg.APIKeys
		if len(persistCfg.HistoryEntries) > 0 {
			m.historyStore = inputarea.NewHistoryWithEntries(100, persistCfg.HistoryEntries)
		}
	}
	// Load session history from persistent store.
	m.sessionStore = NewSessionStore(defaultSessionConfigDir())
	_ = m.sessionStore.Load() // errors are silently ignored at startup
	m.sessionPicker = sessionpicker.New(sessionEntriesToPicker(m.sessionStore.List()))
	// Initialize permissions panel (client-local, starts with no rules).
	m.permissionsPanel = permissionspanel.New()
	// Bootstrap: read known provider keys from the shell environment so models
	// show as available immediately — without requiring the user to enter them
	// via /keys. These keys are stored separately (envAPIKeys) and are NOT
	// replayed to the server on Init() because the server already reads its own
	// environment variables.
	m.envAPIKeys = make(map[string]string)
	envKeyVars := map[string]string{
		"openrouter": "OPENROUTER_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"anthropic":  "ANTHROPIC_API_KEY",
	}
	for provider, envVar := range envKeyVars {
		if key := os.Getenv(envVar); key != "" {
			m.envAPIKeys[provider] = key
		}
	}
	m.commandRegistry = m.buildCommandRegistry()
	// Set default plugins directory (~/.config/harnesscli/plugins) and load any
	// custom slash-command plugins from it. Errors and collisions are stored as
	// warnings and shown to the user via a status message in Init().
	if m.pluginsDir == "" {
		m.pluginsDir = defaultPluginsDir()
	}
	m.pluginWarnings = LoadAndRegisterPlugins(m.commandRegistry, m.pluginsDir)
	// Wire help dialog with real command list and keybindings derived from the
	// registered commands and the default key map.
	m.helpDialog = buildHelpDialog(m.commandRegistry, m.keys)
	// Wire tab completion: combine slash command provider with file path completer
	// so Tab works for both "/" commands and "@" file paths.
	m = m.WithAutocompleteProvider(buildCombinedProvider(m.commandRegistry))
	// Wire slash-complete dropdown.
	m.slashComplete = buildSlashComplete(m.commandRegistry)
	if cfg.ResumeConversationID != "" {
		m.conversationID = cfg.ResumeConversationID
	}
	return m
}

// defaultPluginsDir returns the default directory for user-defined plugin files.
func defaultPluginsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "harnesscli", "plugins")
}

// buildHelpDialog constructs a helpdialog.Model populated with the commands from
// the registry and keybindings from the key map.
func buildHelpDialog(reg *CommandRegistry, keys KeyMap) helpdialog.Model {
	entries := reg.All()
	cmds := make([]helpdialog.CommandEntry, len(entries))
	for i, e := range entries {
		cmds[i] = helpdialog.CommandEntry{
			Name:        e.Name,
			Description: e.Description,
		}
	}

	kbs := []helpdialog.KeyEntry{
		{Keys: "enter", Description: keys.Submit.Help().Desc},
		{Keys: "shift+enter / ctrl+j", Description: keys.Newline.Help().Desc},
		{Keys: "up / ctrl+p", Description: keys.ScrollUp.Help().Desc},
		{Keys: "down / ctrl+n", Description: keys.ScrollDown.Help().Desc},
		{Keys: "pgup", Description: keys.PageUp.Help().Desc},
		{Keys: "pgdn", Description: keys.PageDown.Help().Desc},
		{Keys: "/", Description: keys.SlashCmd.Help().Desc},
		{Keys: "@", Description: keys.AtMention.Help().Desc},
		{Keys: "? / ctrl+h", Description: keys.Help.Help().Desc},
		{Keys: "ctrl+o", Description: "plan mode / expand active tool"},
		{Keys: "ctrl+e", Description: keys.EditMode.Help().Desc},
		{Keys: "esc", Description: keys.Interrupt.Help().Desc},
		{Keys: "ctrl+s", Description: keys.Copy.Help().Desc},
		{Keys: "ctrl+c", Description: "interrupt run (twice) / quit when idle"},
	}

	about := []string{
		"go-agent-harness",
		"Type /help to see this dialog",
		"Type /stats for usage statistics",
		"Type /context for context window usage",
	}

	return helpdialog.New(cmds, kbs, about)
}

// WithAutocompleteProvider returns a copy of the Model with the given autocomplete
// provider wired into the input area.  The provider is also stored on the Model
// so it can be re-applied whenever the input component is re-created (e.g. on
// every WindowSizeMsg).
func (m Model) WithAutocompleteProvider(fn inputarea.AutocompleteProvider) Model {
	m.autocompleteProvider = fn
	m.input = m.input.SetAutocompleteProvider(fn)
	return m
}

// buildSlashComplete constructs a slashcomplete.Model populated with the
// commands from the registry.
func buildSlashComplete(reg *CommandRegistry) slashcomplete.Model {
	entries := reg.All()
	suggestions := make([]slashcomplete.Suggestion, len(entries))
	for i, e := range entries {
		suggestions[i] = slashcomplete.Suggestion{
			Name:        e.Name,
			Description: e.Description,
		}
	}
	return slashcomplete.New(suggestions)
}

// syncSlashComplete updates the dropdown state to match the current input value.
// Opens/filters when input starts with "/"; closes otherwise.
func syncSlashComplete(m slashcomplete.Model, input string) slashcomplete.Model {
	if strings.HasPrefix(input, "/") {
		query := strings.TrimPrefix(input, "/")
		// Strip any trailing space (command fully typed)
		if strings.Contains(query, " ") {
			return m.Close()
		}
		return m.Open().SetQuery(query)
	}
	return m.Close()
}

// buildSlashCommandProvider returns an AutocompleteProvider that completes
// slash commands drawn from the given registry.
func buildSlashCommandProvider(reg *CommandRegistry) inputarea.AutocompleteProvider {
	return func(input string) []string {
		if !strings.HasPrefix(input, "/") {
			return nil
		}
		entries := reg.All()
		var matches []string
		for _, e := range entries {
			full := "/" + e.Name
			if strings.HasPrefix(full, input) {
				matches = append(matches, full)
			}
		}
		return matches
	}
}

// buildCombinedProvider returns an AutocompleteProvider that delegates to the
// slash command provider when input starts with "/" and the file path completer
// when the input contains an "@" token.
func buildCombinedProvider(reg *CommandRegistry) inputarea.AutocompleteProvider {
	slashProvider := buildSlashCommandProvider(reg)
	return func(input string) []string {
		if strings.HasPrefix(input, "/") {
			return slashProvider(input)
		}
		// Delegate to the file path completer when @ is present.
		return FilePathCompleter(input)
	}
}

// RunActive returns true if a run is currently in flight.
func (m Model) RunActive() bool {
	return m.runActive
}

// StatusMsg returns the current transient status message (for testing).
func (m Model) StatusMsg() string {
	return m.statusMsg
}

// OverlayActive returns true when an overlay is currently open (for testing).
func (m Model) OverlayActive() bool {
	return m.overlayActive
}

// ActiveOverlay returns the name of the currently active overlay (for testing).
// Returns "" when no overlay is open. Valid values: "help", "stats", "context".
func (m Model) ActiveOverlay() string {
	return m.activeOverlay
}

// ConversationID returns the current conversation ID (for testing and multi-turn use).
func (m Model) ConversationID() string {
	return m.conversationID
}

// SelectedModel returns the currently active model ID (for testing).
func (m Model) SelectedModel() string {
	return m.selectedModel
}

// SelectedReasoningEffort returns the currently active reasoning effort (for testing).
func (m Model) SelectedReasoningEffort() string {
	return m.selectedReasoningEffort
}

// PluginWarnings returns the warnings collected during plugin loading and
// registration. An empty or nil slice means no issues occurred.
func (m Model) PluginWarnings() []string {
	return m.pluginWarnings
}

// WithPluginsDir returns a copy of the Model with the given plugins directory
// set, loading and registering plugins from that directory. This is intended
// primarily for testing; in production, the directory is set at construction.
// Calling this method re-runs plugin loading and updates the registry, help
// dialog, and slash-complete dropdown.
func (m Model) WithPluginsDir(dir string) Model {
	m.pluginsDir = dir
	m.pluginWarnings = LoadAndRegisterPlugins(m.commandRegistry, dir)
	// Rebuild autocomplete and slash-complete to include newly registered plugins.
	m = m.WithAutocompleteProvider(buildCombinedProvider(m.commandRegistry))
	m.slashComplete = buildSlashComplete(m.commandRegistry)
	m.helpDialog = buildHelpDialog(m.commandRegistry, m.keys)
	return m
}

// SelectedProfile returns the currently selected profile name (for testing).
// Returns "" when no profile is selected.
func (m Model) SelectedProfile() string {
	return m.selectedProfile
}

// SessionStore returns the session store (for testing).
// The returned pointer is live — callers can inspect it after updates.
func (m Model) SessionStore() *SessionStore {
	return m.sessionStore
}

// gatewayIndex returns the index of the gateway option with the given ID,
// or 0 if not found.
func gatewayIndex(id string) int {
	for i, g := range gatewayOptions {
		if g.ID == id {
			return i
		}
	}
	return 0
}

// reasoningLevelIndex returns the index of the reasoning level with the given
// effort ID, or 0 if not found.
func reasoningLevelIndex(effort string) int {
	for i, r := range modelswitcher.ReasoningLevels {
		if r.ID == effort {
			return i
		}
	}
	return 0
}

// providerKeyConfigured returns true if the given provider key has a configured
// API key in the loaded provider list or in pendingAPIKeys (for OpenRouter and
// other keys set via /keys before the server sync completes).
func (m Model) providerKeyConfigured(providerKey string) bool {
	for _, p := range m.apiKeyProviders {
		if p.Name == providerKey && p.Configured {
			return true
		}
	}
	// Fallback: check locally cached keys (set via /keys or loaded from config).
	if key, ok := m.pendingAPIKeys[providerKey]; ok && key != "" {
		return true
	}
	// Fallback: check keys read from the shell environment at startup.
	if key, ok := m.envAPIKeys[providerKey]; ok && key != "" {
		return true
	}
	return false
}

// displayModelName returns the display name for a model ID, or the ID itself
// if not found in DefaultModels.
func displayModelName(id string) string {
	for _, dm := range modelswitcher.DefaultModels {
		if dm.ID == id {
			return dm.DisplayName
		}
	}
	return id
}

// AskUserActive returns true when the AskUserQuestion overlay is active (for testing).
func (m Model) AskUserActive() bool {
	return m.askUser.active
}

// PlanMode returns true when plan mode is toggled on (for testing).
func (m Model) PlanMode() bool {
	return m.planMode
}

// InterruptBannerVisible returns true when the interrupt confirmation banner is
// currently visible (State != Hidden). Used by tests to assert two-stage behavior.
func (m Model) InterruptBannerVisible() bool {
	return m.interruptBanner.IsVisible()
}

// InterruptBannerState returns the current state of the interrupt banner (for testing).
func (m Model) InterruptBannerState() interruptui.State {
	return m.interruptBanner.CurrentState()
}

// AskUserQuestions returns the pending questions for the active AskUserQuestion overlay (for testing).
func (m Model) AskUserQuestions() []AskUserQuestion {
	return m.askUser.questions
}

// AskUserSelectedIdx returns the currently selected option index within the active
// question (for testing).
func (m Model) AskUserSelectedIdx() int {
	return m.askUser.selectedIdx
}

// ToolApprovalActive returns true when the tool-approval overlay is active (for testing).
func (m Model) ToolApprovalActive() bool {
	return m.toolApproval.active
}

// ToolApprovalTool returns the name of the tool pending approval (for testing).
func (m Model) ToolApprovalTool() string {
	return m.toolApproval.tool
}

// ToolApprovalCallID returns the call ID of the tool call pending approval (for testing).
func (m Model) ToolApprovalCallID() string {
	return m.toolApproval.callID
}

// ToolApprovalArguments returns the formatted argument summary for the tool call
// pending approval (for testing).
func (m Model) ToolApprovalArguments() string {
	return m.toolApproval.arguments
}

// LastAssistantText returns the accumulated assistant text for the current run (for testing).
func (m Model) LastAssistantText() string {
	return m.lastAssistantText
}

// Input returns the current value of the input area (for testing).
func (m Model) Input() string {
	return m.input.Value()
}

// ViewportScrollOffset returns the current viewport scroll offset (lines from bottom).
// This is used by tests to assert scrolling behavior.
func (m Model) ViewportScrollOffset() int {
	return m.vp.ScrollOffset()
}

// ViewportAtBottom reports whether the viewport is at the bottom.
// This is used by tests to assert scroll state.
func (m Model) ViewportAtBottom() bool {
	return m.vp.AtBottom()
}

// Transcript returns a copy of the current transcript entries (for testing).
func (m Model) Transcript() []transcriptexport.TranscriptEntry {
	cp := make([]transcriptexport.TranscriptEntry, len(m.transcript))
	copy(cp, m.transcript)
	return cp
}

// WithCancelRun returns a copy of the Model with the given cancel func set.
// This is used to wire up the SSE bridge cancel func before a run starts.
func (m Model) WithCancelRun(cancel func()) Model {
	m.cancelRun = cancel
	return m
}

// pluginCommandResultMsg carries the outcome of an asynchronously-executed
// plugin slash command (bash or prompt handler).
type pluginCommandResultMsg struct {
	Result CommandResult
}

// runPluginCommandCmd runs a plugin command's Handler as a tea.Cmd so a slow
// bash plugin cannot block Update(). Bubble Tea runs the returned closure on
// its own goroutine, so ExecuteBash's up-to-10s blocking call happens off the
// render/input/SSE-polling loop.
func runPluginCommandCmd(entry CommandEntry, cmd Command) tea.Cmd {
	return func() tea.Msg {
		return pluginCommandResultMsg{Result: entry.Handler(cmd)}
	}
}

// statusTickCmd returns a tea.Cmd that fires statusTickMsg after duration d.
func statusTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// spinnerTickCmd returns a tea.Cmd that fires a spinner.SpinnerTickMsg after
// SpinnerInterval. The handler for that message re-issues this command while
// a run is active, forming a self-rescheduling animation loop that stops on
// its own once the run ends.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(SpinnerInterval, func(t time.Time) tea.Msg { return spinner.SpinnerTickMsg{T: t} })
}

// currentSpinnerAction returns a short label describing what is currently
// running, for display in the spinner, or "" when nothing more specific than
// "thinking" is known. Only reports the active tool while it is genuinely
// still running — activeToolCallID lingers after completion so it can be
// expanded/collapsed, but by then there is nothing left to announce.
func (m Model) currentSpinnerAction() string {
	if m.activeToolCallID == "" {
		return ""
	}
	view, ok := m.toolViews[m.activeToolCallID]
	if !ok || view.Status != "running" || view.ToolName == "" {
		return ""
	}
	return "Running " + view.ToolName
}

// StatusTickMsgForTesting returns a statusTickMsg as a tea.Msg for use in
// external test packages that need to drive the auto-dismiss path.
func StatusTickMsgForTesting() tea.Msg { return statusTickMsg{} }

// setStatusMsg sets the transient status message and schedules its auto-dismiss tick.
// The returned tea.Cmd must be appended to the caller's cmds slice.
func (m *Model) setStatusMsg(msg string) tea.Cmd {
	m.statusMsg = msg
	m.statusMsgExpiry = time.Now().Add(statusMsgDuration)
	return statusTickCmd(statusMsgDuration)
}

func renderedBlockLines(rendered string) []string {
	if rendered == "" {
		return nil
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (m Model) renderMessageBubble(role messagebubble.Role, content string) []string {
	bubble := messagebubble.New(role, content)
	bubble.Width = m.width
	return renderedBlockLines(bubble.View())
}

func (m *Model) appendMessageBubble(role messagebubble.Role, content string) {
	lines := m.renderMessageBubble(role, content)
	if len(lines) == 0 {
		return
	}
	m.renderedToolCallID = ""
	m.vp.AppendLines(lines)
}

func (m *Model) appendToolUseView(view tooluse.Model) {
	view.Width = m.width
	lines := renderedBlockLines(view.View())
	if len(lines) == 0 {
		return
	}

	callID := view.CallID
	prevCount, known := m.toolLineCounts[callID]

	if known && prevCount > 0 {
		// Card already placed in the viewport. Try fast-path first: if it's
		// still at the tail, use ReplaceTailLines. Otherwise use ReplaceLineRange
		// to update in-place even though other content was appended after it.
		if m.renderedToolCallID == callID {
			// Fast path: card is at the tail.
			m.vp.ReplaceTailLines(prevCount, lines)
		} else {
			// In-place update: replace the card at its recorded start offset.
			start, hasStart := m.toolLineStarts[callID]
			if hasStart {
				lineDelta := len(lines) - prevCount
				m.vp.ReplaceLineRange(start, prevCount, lines)
				// Shift stored start offsets of all cards that were appended
				// after this one (they are now at a different position).
				if lineDelta != 0 {
					found := false
					for _, id := range m.toolLineOrder {
						if found {
							if s, ok := m.toolLineStarts[id]; ok {
								m.toolLineStarts[id] = s + lineDelta
							}
						}
						if id == callID {
							found = true
						}
					}
				}
			} else {
				// No recorded start — fall back to append (first time we see
				// a callID that arrived before we started tracking).
				start := m.vp.LineCount()
				m.toolLineStarts[callID] = start
				m.toolLineOrder = append(m.toolLineOrder, callID)
				m.vp.AppendLines(lines)
			}
		}
	} else {
		// New card: record its start offset and append.
		start := m.vp.LineCount()
		m.toolLineStarts[callID] = start
		m.toolLineOrder = append(m.toolLineOrder, callID)
		m.vp.AppendLines(lines)
	}

	m.toolViews[callID] = view
	m.toolLineCounts[callID] = len(lines)
	m.renderedToolCallID = callID
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return compact.String()
}

// summarizeToolArgs returns a concise, human-readable summary of the tool arguments
// suitable for display in a collapsed card. It never includes raw file content.
//
//   - write/edit/create/str_replace tools: "<path> (N lines)" using the content field.
//   - read/view tools: "<path>" using the path/file_path/filename field.
//   - bash/shell tools: the command string, single-line, capped to 60 chars.
//   - fallback: a compact JSON snippet capped to ~60 chars with an ellipsis.
func summarizeToolArgs(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	lowerName := strings.ToLower(toolName)

	// Bash/shell: show the command, not the full JSON.
	if strings.EqualFold(toolName, "bash") || lowerName == "shell" || lowerName == "run_shell_cmd" {
		var params map[string]any
		if err := json.Unmarshal(input, &params); err == nil {
			for _, key := range []string{"command", "cmd"} {
				if v, ok := params[key].(string); ok && v != "" {
					// Single-line, capped.
					cmd := strings.SplitN(strings.TrimSpace(v), "\n", 2)[0]
					if len([]rune(cmd)) > 60 {
						cmd = string([]rune(cmd)[:57]) + "…"
					}
					return cmd
				}
			}
		}
		// Try plain string input.
		var s string
		if err := json.Unmarshal(input, &s); err == nil && s != "" {
			cmd := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
			if len([]rune(cmd)) > 60 {
				cmd = string([]rune(cmd)[:57]) + "…"
			}
			return cmd
		}
	}

	// Write/edit/create/str_replace tools: path + line count of content.
	isWriteLike := lowerName == "write_file" || lowerName == "write" ||
		lowerName == "edit_file" || lowerName == "edit" ||
		lowerName == "create_file" || lowerName == "create" ||
		lowerName == "str_replace_based_edit_tool" || lowerName == "str_replace_editor" ||
		strings.HasPrefix(lowerName, "write_") || strings.HasPrefix(lowerName, "edit_") ||
		strings.HasPrefix(lowerName, "create_")
	if isWriteLike {
		var params map[string]any
		if err := json.Unmarshal(input, &params); err == nil {
			path := extractStringField(params, "path", "file_path", "filename", "file")
			content := extractStringField(params, "content", "new_content", "new_str")
			if path != "" {
				if content != "" {
					lineCount := strings.Count(content, "\n")
					if lineCount == 0 && content != "" {
						lineCount = 1
					}
					return fmt.Sprintf("%s (%d lines)", path, lineCount)
				}
				return path
			}
		}
	}

	// Read/view tools: just the path.
	isReadLike := lowerName == "read_file" || lowerName == "read" ||
		lowerName == "view_file" || lowerName == "view" ||
		lowerName == "cat_file" || lowerName == "cat" ||
		strings.HasPrefix(lowerName, "read_") || strings.HasPrefix(lowerName, "view_")
	if isReadLike {
		var params map[string]any
		if err := json.Unmarshal(input, &params); err == nil {
			if path := extractStringField(params, "path", "file_path", "filename", "file"); path != "" {
				return path
			}
		}
		// Try plain string input (path as string).
		var s string
		if err := json.Unmarshal(input, &s); err == nil && s != "" {
			return s
		}
	}

	// Fallback: compact JSON snippet capped at 60 chars.
	compact := compactJSON(input)
	if len([]rune(compact)) > 60 {
		compact = string([]rune(compact)[:57]) + "…"
	}
	return compact
}

// extractStringField returns the first non-empty string value found for any of
// the given keys in params.
func extractStringField(params map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := params[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func formatToolParamValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

func parseToolParams(raw json.RawMessage) []tooluse.Param {
	if len(raw) == 0 {
		return nil
	}
	var paramsMap map[string]any
	if err := json.Unmarshal(raw, &paramsMap); err != nil || len(paramsMap) == 0 {
		return nil
	}
	keys := make([]string, 0, len(paramsMap))
	for key := range paramsMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]tooluse.Param, 0, len(keys))
	for _, key := range keys {
		params = append(params, tooluse.Param{
			Key:   key,
			Value: formatToolParamValue(paramsMap[key]),
		})
	}
	return params
}

func extractToolCommand(toolName string, raw json.RawMessage, fallback string) string {
	if !strings.EqualFold(toolName, "bash") {
		return ""
	}
	var command string
	if err := json.Unmarshal(raw, &command); err == nil {
		return command
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err == nil {
		if command, ok := params["command"].(string); ok && command != "" {
			return command
		}
		if command, ok := params["cmd"].(string); ok && command != "" {
			return command
		}
	}
	return strings.Trim(fallback, "\"")
}

func (m *Model) ensureToolStateMaps() {
	if m.toolNames == nil {
		m.toolNames = make(map[string]string)
	}
	if m.toolArgs == nil {
		m.toolArgs = make(map[string]string)
	}
	if m.toolTimers == nil {
		m.toolTimers = make(map[string]tooluse.Timer)
	}
	if m.toolViews == nil {
		m.toolViews = make(map[string]tooluse.Model)
	}
	if m.toolLineCounts == nil {
		m.toolLineCounts = make(map[string]int)
	}
	if m.toolLineStarts == nil {
		m.toolLineStarts = make(map[string]int)
	}
}

func normalizeThinkingLabel(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if collapsed == "" {
		return ""
	}
	return "Thinking: " + collapsed
}

// defaultContextWindowTokens is the fallback context window size used when
// the model's actual window size has not been reported yet. Mirrors
// contextgrid's own fallback so the status bar and the /context overlay agree.
const defaultContextWindowTokens = 200000

// contextWindowTotal returns the total context window size to report
// alongside token usage, falling back to defaultContextWindowTokens when the
// context grid has not been given a concrete window size yet.
func (m Model) contextWindowTotal() int {
	if m.contextGrid.TotalTokens > 0 {
		return m.contextGrid.TotalTokens
	}
	return defaultContextWindowTokens
}

func (m *Model) clearThinkingBar() {
	m.thinkingText = ""
	m.thinkingBar = thinkingbar.New()
}

func (m *Model) appendThinkingDelta(delta string) {
	m.thinkingText += delta
	label := normalizeThinkingLabel(m.thinkingText)
	if label == "" {
		return
	}
	m.thinkingBar = thinkingbar.Model{
		Active: true,
		Label:  label,
	}
}

// interruptActiveToolCall finalizes the currently active tool call's timer and
// view when a run is cancelled mid-tool-call. Without this, a tool call that
// was still "running" at cancel time keeps its timer running and its view
// stuck rendering "running..." forever, since no further ToolResult/ToolError
// event will ever arrive for it.
func (m *Model) interruptActiveToolCall() {
	if m.activeToolCallID == "" {
		return
	}
	callID := m.activeToolCallID
	view, ok := m.toolViews[callID]
	if !ok || view.Status != "running" {
		return
	}
	timer := m.toolTimers[callID]
	if timer.IsRunning() {
		timer = timer.Stop()
		m.toolTimers[callID] = timer
	}
	view.Status = "error"
	view.ErrorText = "Interrupted"
	view.Timer = timer
	m.toolViews[callID] = view
	m.appendToolUseView(view)
}

// ActiveToolCallStatus returns the Status of the currently active tool call's
// view, or "" when there is no active tool call (for testing).
func (m Model) ActiveToolCallStatus() string {
	if m.activeToolCallID == "" {
		return ""
	}
	return m.toolViews[m.activeToolCallID].Status
}

func (m *Model) rerenderActiveToolView() {
	if m.activeToolCallID == "" || m.renderedToolCallID != m.activeToolCallID {
		return
	}
	view, ok := m.toolViews[m.activeToolCallID]
	if !ok {
		return
	}
	view.Expanded = m.toolExpanded != nil && m.toolExpanded[m.activeToolCallID]
	m.appendToolUseView(view)
}

// unwrapToolInput normalizes tool-call arguments that arrive double-encoded.
// The harness emits tool arguments as a JSON string whose contents are
// themselves JSON (e.g. "{\"command\":\"ls -l\"}"). Left as-is, the argument
// summarizers see a string instead of an object and fall back to dumping the
// raw inner JSON. When the input is a JSON string that itself contains a JSON
// object/array, return that inner JSON; otherwise return the input unchanged.
func unwrapToolInput(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return raw
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return raw
	}
	inner := strings.TrimSpace(s)
	if len(inner) > 0 && (inner[0] == '{' || inner[0] == '[') && json.Valid([]byte(inner)) {
		return json.RawMessage(inner)
	}
	return raw
}

func (m *Model) handleToolStart(callID, name string, input json.RawMessage) {
	m.ensureToolStateMaps()
	m.clearThinkingBar()
	// Tool arguments may arrive double-encoded (a JSON string of JSON); unwrap
	// once so the summarizer, params, and command extraction all see the object.
	input = unwrapToolInput(input)
	// Use a concise summary for the collapsed Args line so that large content
	// fields (e.g. file bodies) never appear in the collapsed card. The full
	// parameters are still available in Params for the expanded view.
	args := callID
	if summary := summarizeToolArgs(name, input); summary != "" {
		args = summary
	} else if compact := compactJSON(input); compact != "" {
		args = compact
	}
	timer := tooluse.NewTimer().Start()
	view := tooluse.Model{
		CallID:   callID,
		ToolName: name,
		Status:   "running",
		Args:     args,
		Params:   parseToolParams(input),
		Command:  extractToolCommand(name, input, args),
		Timer:    timer,
	}
	m.toolNames[callID] = name
	m.toolArgs[callID] = args
	m.toolTimers[callID] = timer
	m.toolViews[callID] = view
	m.activeToolCallID = callID
	m.appendToolUseView(view)
}

func (m *Model) handleToolChunk(callID, chunk string) {
	m.ensureToolStateMaps()
	view, ok := m.toolViews[callID]
	if !ok {
		view = tooluse.Model{
			CallID:   callID,
			ToolName: m.toolNames[callID],
			Status:   "running",
			Args:     m.toolArgs[callID],
			Timer:    m.toolTimers[callID],
		}
	}
	if view.Args == "" {
		view.Args = callID
	}
	view.Status = "running"
	view.Expanded = m.toolExpanded != nil && m.toolExpanded[callID]
	view.Result += chunk
	m.toolViews[callID] = view
	m.activeToolCallID = callID
	m.appendToolUseView(view)
}

func (m *Model) handleToolResult(callID, output string, durationMS int64) {
	m.ensureToolStateMaps()
	view, ok := m.toolViews[callID]
	if !ok {
		view = tooluse.Model{
			CallID:   callID,
			ToolName: m.toolNames[callID],
			Args:     m.toolArgs[callID],
		}
	}
	if view.ToolName == "" {
		view.ToolName = m.toolNames[callID]
	}
	if view.Args == "" {
		view.Args = m.toolArgs[callID]
		if view.Args == "" {
			view.Args = callID
		}
	}
	timer := m.toolTimers[callID]
	if timer.IsRunning() {
		timer = timer.Stop()
		m.toolTimers[callID] = timer
	}
	view.Status = "completed"
	view.Expanded = m.toolExpanded != nil && m.toolExpanded[callID]
	view.Timer = timer
	if output != "" {
		view.Result = output
	}
	if durationMS > 0 {
		view.Duration = tooluse.FormatDuration(time.Duration(durationMS) * time.Millisecond)
	}
	if view.Command == "" {
		view.Command = extractToolCommand(view.ToolName, nil, view.Args)
	}
	m.toolViews[callID] = view
	m.activeToolCallID = callID
	m.appendToolUseView(view)
}

func (m *Model) handleToolError(callID, errText string, durationMS int64) {
	m.ensureToolStateMaps()
	view, ok := m.toolViews[callID]
	if !ok {
		view = tooluse.Model{
			CallID:   callID,
			ToolName: m.toolNames[callID],
			Args:     m.toolArgs[callID],
		}
	}
	if view.ToolName == "" {
		view.ToolName = m.toolNames[callID]
	}
	if view.Args == "" {
		view.Args = m.toolArgs[callID]
		if view.Args == "" {
			view.Args = callID
		}
	}
	timer := m.toolTimers[callID]
	if timer.IsRunning() {
		timer = timer.Stop()
		m.toolTimers[callID] = timer
	}
	view.Status = "error"
	view.ErrorText = errText
	view.Timer = timer
	if durationMS > 0 {
		view.Duration = tooluse.FormatDuration(time.Duration(durationMS) * time.Millisecond)
	}
	m.toolViews[callID] = view
	m.activeToolCallID = callID
	m.appendToolUseView(view)
}

func (m *Model) renderActiveAssistantBubble() {
	m.clearThinkingBar()
	lines := m.renderMessageBubble(messagebubble.RoleAssistant, m.lastAssistantText)
	if !m.responseStarted {
		m.renderedToolCallID = ""
		m.vp.AppendLines(lines)
		m.activeAssistantLineCount = len(lines)
		m.responseStarted = true
		return
	}
	m.renderedToolCallID = ""
	m.vp.ReplaceTailLines(m.activeAssistantLineCount, lines)
	m.activeAssistantLineCount = len(lines)
}

func executeClearCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.vp = viewport.New(m.width, m.layout.ViewportHeight)
	m.transcript = nil
	m.slashComplete = m.slashComplete.Close()
	m.lastAssistantText = ""
	m.responseStarted = false
	m.activeAssistantLineCount = 0
	m.toolLineStarts = make(map[string]int)
	m.toolLineOrder = nil
	m.clearThinkingBar()
	return []tea.Cmd{m.setStatusMsg("Conversation cleared")}, false
}

func executeHelpCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.helpDialog = m.helpDialog.Open()
	m.overlayActive = true
	m.activeOverlay = "help"
	return nil, false
}

func executeContextCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.overlayActive = true
	m.activeOverlay = "context"
	return []tea.Cmd{func() tea.Msg { return OverlayOpenMsg{Kind: "context"} }}, false
}

func executeStatsCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.overlayActive = true
	m.activeOverlay = "stats"
	return []tea.Cmd{func() tea.Msg { return OverlayOpenMsg{Kind: "stats"} }}, false
}

// costSnapshotFromModel builds a costdisplay.CostSnapshot from the model's
// current cumulative cost/token state. The model only tracks a combined
// running token total (not a separate input/output split), so the total is
// surfaced as OutputTokens rather than fabricating a breakdown.
func costSnapshotFromModel(m *Model) costdisplay.CostSnapshot {
	return costdisplay.CostSnapshot{
		OutputTokens: m.totalTokens,
		TotalCostUSD: m.cumulativeCostUSD,
		Model:        m.selectedModel,
	}
}

// executeCostCommand toggles the /cost overlay showing running token usage
// and cost.
func executeCostCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	if m.overlayActive && m.activeOverlay == "cost" {
		m.costDisplay = m.costDisplay.Hide()
		m.overlayActive = false
		m.activeOverlay = ""
		return nil, false
	}
	m.costDisplay = m.costDisplay.Update(costSnapshotFromModel(m)).Show()
	m.overlayActive = true
	m.activeOverlay = "cost"
	return []tea.Cmd{func() tea.Msg { return OverlayOpenMsg{Kind: "cost"} }}, false
}

// configEntriesFromModel builds the read-only config entries shown by the
// /config overlay from the current session's configuration.
func configEntriesFromModel(m *Model) []configpanel.ConfigEntry {
	gateway := m.selectedGateway
	if gateway == "" {
		gateway = "direct"
	}
	reasoning := m.selectedReasoningEffort
	if reasoning == "" {
		reasoning = "default"
	}
	model := m.selectedModel
	if model == "" {
		model = m.config.Model
	}
	return []configpanel.ConfigEntry{
		{Key: "base_url", Value: m.config.BaseURL, Description: "harnessd server URL", ReadOnly: true},
		{Key: "model", Value: model, Description: "Active LLM model", ReadOnly: true},
		{Key: "workspace", Value: m.config.Workspace, Description: "Workspace root path", ReadOnly: true},
		{Key: "max_steps", Value: strconv.Itoa(m.config.MaxSteps), Description: "Max agent steps per run", ReadOnly: true},
		{Key: "theme", Value: m.config.Theme, Description: "Color theme", ReadOnly: true},
		{Key: "color_profile", Value: m.config.ColorProfile, Description: "Terminal color depth", ReadOnly: true},
		{Key: "gateway", Value: gateway, Description: "Model routing gateway", ReadOnly: true},
		{Key: "reasoning_effort", Value: reasoning, Description: "Reasoning effort level", ReadOnly: true},
	}
}

// executeConfigCommand opens the /config overlay: a read-only view of the
// current session configuration.
func executeConfigCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.configPanel = configpanel.New(configEntriesFromModel(m)).Open()
	m.overlayActive = true
	m.activeOverlay = "config"
	return []tea.Cmd{func() tea.Msg { return OverlayOpenMsg{Kind: "config"} }}, false
}

func executeQuitCommand(_ *Model, _ Command) ([]tea.Cmd, bool) {
	return nil, true
}

func executeExportCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	snapshot := make([]transcriptexport.TranscriptEntry, len(m.transcript))
	copy(snapshot, m.transcript)
	exporter := transcriptexport.NewExporter(defaultExportDir())
	return []tea.Cmd{
		func() tea.Msg {
			path, err := exporter.Export(snapshot)
			if err != nil {
				return ExportTranscriptMsg{FilePath: ""}
			}
			return ExportTranscriptMsg{FilePath: path}
		},
	}, false
}

func executeModelCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	currentStarred := m.modelSwitcher.StarredIDs()
	m.modelSwitcher = modelswitcher.New(m.selectedModel).Open()
	m.modelSwitcher = m.modelSwitcher.WithCurrentReasoning(m.selectedReasoningEffort)
	m.modelSwitcher = m.modelSwitcher.WithStarred(currentStarred)
	m.modelSwitcher = m.modelSwitcher.SetLoading(true)
	m.modelConfigMode = false
	m.overlayActive = true
	m.activeOverlay = "model"

	var cmds []tea.Cmd
	if m.selectedGateway == "openrouter" {
		// External API — sends the OpenRouter provider key, never the
		// harnessd key (see fetchOpenRouterModelsCmd's doc comment).
		orKey := m.pendingAPIKeys["openrouter"]
		cmds = append(cmds, fetchOpenRouterModelsCmd(orKey))
	} else {
		cmds = append(cmds, fetchModelsCmd(m.config.BaseURL, m.config.APIKey))
	}
	cmds = append(cmds, fetchProvidersCmd(m.config.BaseURL, m.config.APIKey))
	return cmds, false
}

func executeKeysCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.overlayActive = true
	m.activeOverlay = "apikeys"
	m.apiKeyCursor = 0
	m.apiKeyInput = ""
	m.apiKeyInputMode = false
	return []tea.Cmd{fetchProvidersCmd(m.config.BaseURL, m.config.APIKey)}, false
}

func executeSubagentsCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return []tea.Cmd{
		m.setStatusMsg("Loading subagents..."),
		loadSubagentsCmd(m.config.BaseURL, m.config.APIKey),
	}, false
}

// executeHooksCommand fetches the /v1/hooks listing and renders it into the
// viewport when HooksLoadedMsg arrives (epic #737).
func executeHooksCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return []tea.Cmd{
		m.setStatusMsg("Loading hooks..."),
		loadHooksCmd(m.config.BaseURL, m.config.APIKey),
	}, false
}

// formatHooksLines renders the /v1/hooks listing as plain viewport lines:
// a loaded-hooks table (name, event, kind, source, matcher) followed by a
// skipped section with reasons. The empty state says no hooks loaded.
func formatHooksLines(msg HooksLoadedMsg) []string {
	lines := []string{}
	if len(msg.Hooks) == 0 {
		lines = append(lines, "No hooks loaded.")
	} else {
		lines = append(lines, "Loaded hooks:")
		for _, h := range msg.Hooks {
			entry := fmt.Sprintf("  %-24s %-14s %-8s %s", h.Name, h.Event, h.Kind, h.Source)
			if h.Matcher != "" {
				entry += fmt.Sprintf("  matcher=%s", h.Matcher)
			}
			lines = append(lines, entry)
		}
	}
	if len(msg.Skipped) > 0 {
		lines = append(lines, "Skipped hook files:")
		for _, s := range msg.Skipped {
			lines = append(lines, fmt.Sprintf("  %s (%s)", s.File, s.Reason))
		}
	}
	return lines
}

func executeProfilesCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.overlayActive = true
	m.activeOverlay = "profiles"
	return []tea.Cmd{
		m.setStatusMsg("Loading profiles..."),
		loadProfilesCmd(m.config.BaseURL, m.config.APIKey),
	}, false
}

func executeSessionsCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	// Refresh the picker with the latest store entries before opening.
	m.sessionPicker = sessionpicker.New(sessionEntriesToPicker(m.sessionStore.List())).Open()
	m.sessionPicker.Width = m.width
	m.overlayActive = true
	m.activeOverlay = "sessions"
	return nil, false
}

func executeNewSessionCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	m.conversationID = ""
	m.vp = viewport.New(m.width, m.layout.ViewportHeight)
	m.transcript = nil
	m.lastAssistantText = ""
	m.responseStarted = false
	m.activeAssistantLineCount = 0
	m.clearThinkingBar()
	return []tea.Cmd{m.setStatusMsg("New session started")}, false
}

func executeSearchCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	if len(cmd.Args) == 0 {
		return []tea.Cmd{m.setStatusMsg("Usage: /search <query>")}, false
	}
	query := strings.Join(cmd.Args, " ")
	results := SearchTranscript(m.transcript, query)
	m.searchResults = results
	m.searchQuery = query
	m.searchSelectedIdx = 0
	m.searchIsHistory = false
	m.overlayActive = true
	m.activeOverlay = "search"
	return nil, false
}

func executeHistoryCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	if len(cmd.Args) == 0 {
		return []tea.Cmd{m.setStatusMsg("Usage: /history <query>")}, false
	}
	query := strings.Join(cmd.Args, " ")
	results := searchSessions(m.sessionStore, query)
	m.searchResults = results
	m.searchQuery = query
	m.searchSelectedIdx = 0
	m.searchIsHistory = true
	m.overlayActive = true
	m.activeOverlay = "search"
	return nil, false
}

func executeAttachCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return []tea.Cmd{m.setStatusMsg("Attach files by typing @path in your prompt")}, false
}

func executeRunsCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return []tea.Cmd{
		m.setStatusMsg("Loading runs..."),
		fetchRunsCmd(m.config.BaseURL, m.config.APIKey),
	}, false
}

func executeDashboardCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return m.dashboardOpenCmds(), false
}

func executeCancelCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	runID := ""
	if len(cmd.Args) > 0 {
		runID = cmd.Args[0]
	} else if m.runActive && m.RunID != "" {
		runID = m.RunID
	}
	if strings.TrimSpace(runID) == "" {
		return []tea.Cmd{m.setStatusMsg("Usage: /cancel <run-id>")}, false
	}
	return []tea.Cmd{
		m.setStatusMsg("Cancelling " + runID + "..."),
		cancelRunCmd(m.config.BaseURL, runID, m.config.APIKey),
	}, false
}

func executeReplayCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	if len(cmd.Args) == 0 || strings.TrimSpace(cmd.Args[0]) == "" {
		return []tea.Cmd{m.setStatusMsg("Usage: /replay <run-id-or-rollout-path>")}, false
	}
	target := cmd.Args[0]
	return []tea.Cmd{
		m.setStatusMsg("Replaying " + target + "..."),
		replayRunCmd(m.config.BaseURL, target, m.config.APIKey),
	}, false
}

func executeResumeCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	if len(cmd.Args) < 2 {
		return []tea.Cmd{m.setStatusMsg("Usage: /resume <run-id> <prompt>")}, false
	}
	runID := strings.TrimSpace(cmd.Args[0])
	prompt := strings.TrimSpace(strings.Join(cmd.Args[1:], " "))
	if runID == "" || prompt == "" {
		return []tea.Cmd{m.setStatusMsg("Usage: /resume <run-id> <prompt>")}, false
	}
	expandedPrompt, err := ExpandAtPaths(prompt)
	if err != nil {
		return []tea.Cmd{m.setStatusMsg(fmt.Sprintf("file expand error: %s", err))}, false
	}
	m.pendingLastMsg = truncateStr(prompt, 60)
	m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
		Role:      "user",
		Content:   prompt,
		Timestamp: time.Now(),
	})
	m.appendMessageBubble(messagebubble.RoleUser, prompt)
	return []tea.Cmd{
		m.setStatusMsg("Continuing " + runID + "..."),
		continueRunCmd(m.config.BaseURL, runID, expandedPrompt, m.config.APIKey),
	}, false
}

func executeDoctorCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	return []tea.Cmd{m.setStatusMsg("Run: go test ./cmd/harnesscli && bash -n scripts/go-code.sh")}, false
}

func executePermissionsCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	// Open the panel with an empty rule set — there is no /v1/permissions server
	// route, so this is a client-local panel. The truthful empty state is shown
	// when no rules have been accumulated locally (the normal case on startup).
	m.permissionsPanel = m.permissionsPanel.Open(nil)
	m.permissionsPanel.Width = m.width
	m.permissionsPanel.Height = m.layout.ViewportHeight
	m.overlayActive = true
	m.activeOverlay = "permissions"
	return nil, false
}

// searchPageSize is the maximum number of results shown at once in the overlay.
const searchPageSize = 20

// viewSearchOverlay renders the search results overlay.
func (m Model) viewSearchOverlay() string {
	var sb strings.Builder
	total := len(m.searchResults)
	prefix := "Search"
	if m.searchIsHistory {
		prefix = "History"
	}
	title := prefix + ": " + m.searchQuery
	if total > 0 {
		title += " (" + searchResultCountLabel(total) + ")"
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	sb.WriteString(titleStyle.Render(title))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	if total == 0 {
		noMatchStyle := lipgloss.NewStyle().Faint(true).Padding(1, 2)
		sb.WriteString(noMatchStyle.Render("No matches found for '" + m.searchQuery + "'"))
		sb.WriteString("\n")
		hintStyle := lipgloss.NewStyle().Faint(true).Padding(0, 2)
		sb.WriteString(hintStyle.Render("Press Esc to close"))
		return sb.String()
	}

	selectedStyle := lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#e5e7eb", Dark: "#374151"})
	roleStyle := lipgloss.NewStyle().Bold(true).Width(12)
	faintStyle := lipgloss.NewStyle().Faint(true)

	// Compute the scroll window: at most searchPageSize results visible at a time.
	// The window is anchored so that searchSelectedIdx stays in view.
	windowStart := (m.searchSelectedIdx / searchPageSize) * searchPageSize
	windowEnd := windowStart + searchPageSize
	if windowEnd > total {
		windowEnd = total
	}

	if windowStart > 0 {
		aboveCount := windowStart
		sb.WriteString(faintStyle.Render("... "+strings.TrimSpace(searchResultCountLabel(aboveCount))+" more above") + "\n")
	}

	for i := windowStart; i < windowEnd; i++ {
		r := m.searchResults[i]
		role := r.Role
		snippet := HighlightMatch(r.Snippet, m.searchQuery)
		ts := r.Timestamp.Format("15:04:05")
		line := roleStyle.Render("["+role+"]") + " " + snippet + " " + faintStyle.Render(ts)
		if i == m.searchSelectedIdx {
			line = selectedStyle.Render(line)
		}
		sb.WriteString(line + "\n")
	}

	if windowEnd < total {
		belowCount := total - windowEnd
		sb.WriteString(faintStyle.Render("... "+strings.TrimSpace(searchResultCountLabel(belowCount))+" more below") + "\n")
	}

	sb.WriteString("\n")
	hintStyle := lipgloss.NewStyle().Faint(true)
	sb.WriteString(hintStyle.Render("↑↓ navigate  Enter jump to match  Esc close"))
	return sb.String()
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for provider, providerKey := range m.pendingAPIKeys {
		cmds = append(cmds, setProviderKeyCmd(m.config.BaseURL, provider, providerKey, m.config.APIKey))
	}
	// Schedule a status message when plugin warnings were collected at startup.
	if len(m.pluginWarnings) > 0 {
		msg := fmt.Sprintf("%d plugin(s) had errors loading", len(m.pluginWarnings))
		cmds = append(cmds, func() tea.Msg {
			return pluginWarningMsg{text: msg}
		})
	}
	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

// pluginWarningMsg is a tea.Msg that triggers a status-bar notification for
// plugin load errors discovered at startup.
type pluginWarningMsg struct {
	text string
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Clear expired status message.
	if m.statusMsg != "" && !m.statusMsgExpiry.IsZero() && time.Now().After(m.statusMsgExpiry) {
		m.statusMsg = ""
		m.statusMsgExpiry = time.Time{}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout = layout.Compute(msg.Width, msg.Height)
		alreadyReady := m.ready
		m.ready = true

		// Initialize/resize components
		m.statusBar = statusbar.New(msg.Width)
		m.statusBar.SetModel(m.statusBarModelLabel())
		m.statusBar.SetCost(m.cumulativeCostUSD)
		m.statusBar.SetContext(m.totalTokens, m.contextWindowTotal())
		m.interruptBanner.Width = msg.Width
		// Preserve conversation history across window resizes (#664). On the
		// first WindowSizeMsg the viewport has no content yet, so create it; on
		// subsequent resizes, resize the existing viewport in place instead of
		// replacing it with an empty one (which discarded all messages/cards).
		if alreadyReady {
			m.vp.SetSize(msg.Width, m.layout.ViewportHeight)
		} else {
			m.vp = viewport.New(msg.Width, m.layout.ViewportHeight)
			// On resume, fetch the prior conversation only after the viewport
			// exists (first init runs exactly once, since m.ready is now true),
			// so the rendered history cannot be wiped by this viewport creation.
			if m.config.ResumeConversationID != "" {
				cmds = append(cmds, fetchConversationMessagesCmd(m.config.BaseURL, m.conversationID, m.config.APIKey))
			}
		}
		// Preserve current history across window resizes: on subsequent resizes
		// (alreadyReady == true), sync historyStore from the live input state so
		// any commands typed since startup are preserved. On the first WindowSizeMsg
		// we keep historyStore as-is (loaded from config in New()).
		if alreadyReady {
			m.historyStore = m.input.HistoryState()
		}
		m.input = inputarea.NewWithHistory(msg.Width, m.historyStore)
		// Re-wire autocomplete provider each time the input is re-created.
		if m.autocompleteProvider != nil {
			m.input = m.input.SetAutocompleteProvider(m.autocompleteProvider)
		}
		// Update profile picker width on resize.
		m.profilePicker.Width = msg.Width

	case tea.KeyMsg:
		// Ask-user overlay has the highest key priority — check it first.
		if m.askUser.active {
			newState, cmd := m.handleAskUserKey(msg)
			m.askUser = newState
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		// Tool-approval overlay takes the same key priority as ask-user.
		if m.toolApproval.active {
			newState, cmd := m.handleToolApprovalKey(msg)
			m.toolApproval = newState
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			// Two-stage Ctrl+C interrupt when a run is active.
			if m.runActive {
				if !m.interruptBanner.IsVisible() {
					// First Ctrl+C: show the confirmation banner, do NOT cancel yet.
					m.interruptBanner = m.interruptBanner.Show()
					m.interruptBanner.Width = m.width
					cmds = append(cmds, m.setStatusMsg("Press ctrl+c again to interrupt (esc to keep going)"))
					return m, tea.Batch(cmds...)
				}
				// Second Ctrl+C (banner already showing): confirm the interrupt and
				// cancel the run both server-side and locally.
				m.interruptBanner = m.interruptBanner.Hide()
				if m.RunID != "" {
					cmds = append(cmds, cancelRunCmd(m.config.BaseURL, m.RunID, m.config.APIKey))
				}
				if m.cancelRun != nil {
					m.cancelRun()
					m.cancelRun = nil
				}
				m.interruptActiveToolCall()
				m.runActive = false
				cmds = append(cmds, m.setStatusMsg("Run interrupted — press ctrl+c again to quit"))
				return m, tea.Batch(cmds...)
			}
			// No active run: hide banner if somehow visible, then quit.
			m.interruptBanner = m.interruptBanner.Hide()
			return m, tea.Quit
		case key.Matches(msg, m.keys.Copy):
			ok := CopyToClipboard(m.lastAssistantText)
			if ok {
				cmds = append(cmds, m.setStatusMsg("Copied!"))
			} else {
				cmds = append(cmds, m.setStatusMsg("Copy unavailable"))
			}
		case key.Matches(msg, m.keys.Interrupt):
			if m.overlayActive && m.activeOverlay == "dashboard" && m.dashboard.peekID != "" {
				if m.dashboard.stopPeek != nil {
					m.dashboard.stopPeek()
				}
				m.dashboard.peekID, m.dashboard.peekCh, m.dashboard.stopPeek = "", nil, nil
				return m, tea.Batch(cmds...)
			}
			// If the interrupt banner is visible, Escape dismisses it without
			// cancelling the run (user changed their mind).
			if m.interruptBanner.IsVisible() {
				m.interruptBanner = m.interruptBanner.Hide()
				cmds = append(cmds, m.setStatusMsg("Interrupt cancelled"))
				return m, tea.Batch(cmds...)
			}
			// Highest priority: if the slash-complete dropdown is open, Escape
			// closes ONLY the dropdown and retains the typed input. A second
			// Escape then falls through to the priority chain below (clear input).
			if m.slashComplete.IsActive() {
				m.slashComplete = m.slashComplete.Close()
				return m, tea.Batch(cmds...)
			}
			// Otherwise ensure the dropdown is closed and continue the chain.
			m.slashComplete = m.slashComplete.Close()
			// Multi-priority Escape semantics (highest to lowest):
			// 0. apikeys overlay → back from input or close
			// 1. model overlay  → back/close (2-level)
			// 2. overlayActive  → close overlay
			// 3. runActive      → cancel run
			// 4. input has text → clear input
			// 5. otherwise      → no-op
			if m.overlayActive && m.activeOverlay == "apikeys" {
				if m.apiKeyInputMode {
					m.apiKeyInputMode = false
					m.apiKeyInput = ""
				} else {
					m.overlayActive = false
					m.activeOverlay = ""
				}
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "provider" {
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "model" {
				// Config panel key input mode → exit key input (keep config panel open).
				if m.modelConfigMode && m.modelConfigKeyInputMode {
					m.modelConfigKeyInputMode = false
					m.modelConfigKeyInput = ""
					return m, tea.Batch(cmds...)
				}
				// Config panel → back to Level-0 model list.
				if m.modelConfigMode {
					m.modelConfigMode = false
					return m, tea.Batch(cmds...)
				}
				// Escape with active search (any level, including right
				// after "/" before any character has been typed): clear
				// search, return to browse level state.
				if m.modelSwitcher.SearchActive() {
					m.modelSwitcher = m.modelSwitcher.SetSearch("")
					return m, tea.Batch(cmds...)
				}
				// Escape at level 1 (model list for a provider): go back to provider list.
				if m.modelSwitcher.BrowseLevel() == 1 {
					m.modelSwitcher = m.modelSwitcher.ExitToProviderList()
					return m, tea.Batch(cmds...)
				}
				// Escape at level 0 (provider list) with no search: close overlay entirely.
				m.modelSwitcher = m.modelSwitcher.Close()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "profiles" {
				m.profilePicker = m.profilePicker.Close()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "permissions" {
				m.permissionsPanel = m.permissionsPanel.Close()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "cost" {
				m.costDisplay = m.costDisplay.Hide()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "config" {
				m.configPanel = m.configPanel.Close()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "sessions" {
				m.sessionPicker = m.sessionPicker.Close()
				m.overlayActive = false
				m.activeOverlay = ""
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "search" {
				m.overlayActive = false
				m.activeOverlay = ""
				m.searchResults = nil
				m.searchQuery = ""
				m.searchSelectedIdx = 0
				return m, tea.Batch(cmds...)
			}
			if m.activeOverlay == "dashboard" {
				m.closeDashboard()
				return m, tea.Batch(cmds...)
			}
			if m.overlayActive {
				m.overlayActive = false
				m.activeOverlay = ""
				m.helpDialog = m.helpDialog.Close()
				cmds = append(cmds, func() tea.Msg { return EscapeMsg{} })
				return m, tea.Batch(cmds...)
			}
			if m.runActive && m.cancelRun != nil {
				m.cancelRun()
				m.runActive = false
				m.cancelRun = nil
				m.interruptActiveToolCall()
				cmds = append(cmds, m.setStatusMsg("Interrupted"))
				return m, tea.Batch(cmds...)
			}
			if m.input.Value() != "" {
				// Clear input directly via Clear() — no fragile key simulation.
				m.input = m.input.Clear()
				cmds = append(cmds, m.setStatusMsg("Input cleared"))
				return m, tea.Batch(cmds...)
			}
			// No-op.
			return m, tea.Batch(cmds...)
		case key.Matches(msg, m.keys.ExpandTool):
			// Dual-purpose ctrl+o with precedence (highest first):
			// 1. Active tool call present → expand/collapse it (UNCHANGED behavior).
			// 2. Idle (no run, no active tool) → toggle plan mode.
			// This ordering ensures a later tool-card expansion ticket can add
			// completed-card expansion between (1) and (2) without conflict.
			if m.activeToolCallID != "" {
				// Highest priority: expand/collapse the active tool call.
				if m.toolExpanded == nil {
					m.toolExpanded = make(map[string]bool)
				}
				m.toolExpanded[m.activeToolCallID] = !m.toolExpanded[m.activeToolCallID]
				m.rerenderActiveToolView()
			} else if !m.runActive {
				// Idle (no run active, no active tool): toggle plan mode.
				m.planMode = !m.planMode
				if m.planMode {
					cmds = append(cmds, m.setStatusMsg("Plan mode: ON"))
				} else {
					cmds = append(cmds, m.setStatusMsg("Plan mode: OFF"))
				}
			}
		case key.Matches(msg, m.keys.Submit):
			// When the search overlay is active, Enter dismisses the overlay and
			// attempts to scroll the viewport to the selected result's position.
			if m.overlayActive && m.activeOverlay == "search" {
				if len(m.searchResults) > 0 && m.searchSelectedIdx >= 0 && m.searchSelectedIdx < len(m.searchResults) {
					// Best-effort scroll: use a heuristic of ~4 lines per message bubble.
					// We scroll to the bottom first, then scroll up by the number of
					// lines from the end that the target entry occupies.
					entryIndex := m.searchResults[m.searchSelectedIdx].EntryIndex
					totalEntries := len(m.transcript)
					linesPerMessage := 4
					// Entries after the target (from bottom).
					entriesFromBottom := totalEntries - entryIndex - 1
					if entriesFromBottom < 0 {
						entriesFromBottom = 0
					}
					m.vp.ScrollToBottom()
					m.vp.ScrollUp(entriesFromBottom * linesPerMessage)
				}
				m.overlayActive = false
				m.activeOverlay = ""
				m.searchResults = nil
				m.searchQuery = ""
				m.searchSelectedIdx = 0
				return m, tea.Batch(cmds...)
			}
			// When the profiles overlay is active, Enter confirms selection via the picker.
			if m.overlayActive && m.activeOverlay == "profiles" {
				var ppCmd tea.Cmd
				m.profilePicker, ppCmd = m.profilePicker.Update(msg)
				if ppCmd != nil {
					cmds = append(cmds, ppCmd)
				}
				return m, tea.Batch(cmds...)
			}
			// When the apikeys overlay is active, Enter enters input mode or confirms.
			if m.overlayActive && m.activeOverlay == "apikeys" {
				if m.apiKeyInputMode && m.apiKeyInput != "" {
					provider := m.apiKeyProviders[m.apiKeyCursor].Name
					apiKey := m.apiKeyInput
					m.apiKeyInputMode = false
					m.apiKeyInput = ""
					cmds = append(cmds, setProviderKeyCmd(m.config.BaseURL, provider, apiKey, m.config.APIKey))
				} else if !m.apiKeyInputMode && len(m.apiKeyProviders) > 0 {
					m.apiKeyInputMode = true
				}
				return m, tea.Batch(cmds...)
			}
			// When the provider overlay is active, Enter confirms the selection.
			if m.overlayActive && m.activeOverlay == "provider" {
				chosen := gatewayOptions[m.gatewaySelected]
				m.overlayActive = false
				m.activeOverlay = ""
				gateway := chosen.ID
				cmds = append(cmds, func() tea.Msg {
					return GatewaySelectedMsg{Gateway: gateway}
				})
				return m, tea.Batch(cmds...)
			}
			// When the model overlay is active, Enter navigates or confirms.
			if m.overlayActive && m.activeOverlay == "model" {
				// Config panel is active.
				if m.modelConfigMode {
					if m.modelConfigKeyInputMode {
						// Confirm key entry.
						if m.modelConfigKeyInput != "" {
							provider := m.modelConfigEntry.Provider
							key := m.modelConfigKeyInput
							m.modelConfigKeyInputMode = false
							m.modelConfigKeyInput = ""
							cmds = append(cmds, setProviderKeyCmd(m.config.BaseURL, provider, key, m.config.APIKey))
						}
						return m, tea.Batch(cmds...)
					}
					// Enter in config panel (not in key input) → confirm and close.
					gateway := gatewayOptions[m.modelConfigGatewayCursor].ID
					reasoningEffort := ""
					if m.modelConfigEntry.ReasoningMode {
						reasoningEffort = modelswitcher.ReasoningLevels[m.modelConfigReasoningCursor].ID
					}
					m.modelSwitcher = m.modelSwitcher.Close()
					m.overlayActive = false
					m.activeOverlay = ""
					m.modelConfigMode = false
					modelID := m.modelConfigEntry.ID
					modelProvider := m.modelConfigEntry.Provider
					cmds = append(cmds, func() tea.Msg {
						return ModelSelectedMsg{ModelID: modelID, Provider: modelProvider, ReasoningEffort: reasoningEffort}
					})
					cmds = append(cmds, func() tea.Msg {
						return GatewaySelectedMsg{Gateway: gateway}
					})
					return m, tea.Batch(cmds...)
				}
				// Level 0 (provider list) with no active search: Enter drills into the selected provider.
				if m.modelSwitcher.BrowseLevel() == 0 && !m.modelSwitcher.SearchActive() {
					m.modelSwitcher = m.modelSwitcher.DrillIntoProvider()
					return m, tea.Batch(cmds...)
				}
				// Level 1 or search: Enter selects the model. Check availability before config panel.
				entry, _ := m.modelSwitcher.Accept()
				// Only redirect to /keys when availability info is loaded AND the
				// provider is confirmed unconfigured (#315 Gap 1).
				if m.modelSwitcher.AvailabilityKnown() && !entry.Available {
					// Model's provider is not configured — open /keys overlay
					// pre-positioned on the relevant provider.
					m.modelSwitcher = m.modelSwitcher.Close()
					m.modelConfigMode = false
					m.activeOverlay = "apikeys"
					// overlayActive stays true (already set by the outer "model" case).
					m.apiKeyInput = ""
					m.apiKeyInputMode = false
					// Pre-position cursor on the provider for this model.
					if idx := m.providerIndexInAPIKeyList(entry.Provider); idx >= 0 {
						m.apiKeyCursor = idx
					} else {
						m.apiKeyCursor = 0
					}
					return m, tea.Batch(cmds...)
				}
				// Provider is configured (or availability not yet known) — enter the config panel normally.
				m.modelConfigEntry = entry
				m.modelConfigMode = true
				m.modelConfigSection = 0
				m.modelConfigGatewayCursor = gatewayIndex(m.selectedGateway)
				m.modelConfigReasoningCursor = reasoningLevelIndex(m.selectedReasoningEffort)
				return m, tea.Batch(cmds...)
			}
			// When the dropdown is active, Enter accepts the selected suggestion
			// instead of submitting the input as a message.
			if m.slashComplete.IsActive() {
				newModel, accepted := m.slashComplete.Accept()
				m.slashComplete = newModel
				if accepted != "" {
					m.input = m.input.SetValue(accepted)
					// If the accepted value is a complete slash command (no additional
					// arguments needed), execute it immediately so the user doesn't
					// have to press Enter a second time (BUG-1).
					trimmed := strings.TrimSpace(accepted)
					if strings.HasPrefix(trimmed, "/") {
						cmdName := strings.TrimPrefix(trimmed, "/")
						if m.commandRegistry.IsRegistered(cmdName) {
							m.input = m.input.SetValue("")
							m.slashComplete = m.slashComplete.Close()
							cmds = append(cmds, func() tea.Msg {
								return inputarea.CommandSubmittedMsg{Value: trimmed}
							})
							return m, tea.Batch(cmds...)
						}
					}
					return m, tea.Batch(cmds...)
				}
				// No suggestion to accept. If the user typed a slash command (e.g.
				// an unrecognized one), don't silently swallow Enter: dispatch it so
				// the dispatcher reports an "Unknown command" hint, and clear the
				// input. This avoids the dead-end where /notacommand + Enter does
				// nothing and leaves stale text in the input.
				m.slashComplete = m.slashComplete.Close()
				raw := strings.TrimSpace(m.input.Value())
				if parsed, ok := ParseCommand(raw); ok {
					if _, found := m.commandRegistry.Lookup(parsed.Name); !found {
						m.input = m.input.Clear()
						cmds = append(cmds, m.setStatusMsg(UnknownResult(parsed.Name).Hint))
					}
				}
				return m, tea.Batch(cmds...)
			}
			// No active dropdown — pass Enter to the input area normally.
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		case m.overlayActive && m.activeOverlay == "help":
			// BUG-4/BUG-3: Route keyboard input to the help dialog when it is open.
			// #670: also wire Up/Down (and vim j/k) to scroll the dialog content.
			switch {
			case msg.Type == tea.KeyTab || msg.Type == tea.KeyRight || msg.String() == "l":
				m.helpDialog = m.helpDialog.NextTab()
			case msg.Type == tea.KeyShiftTab || msg.Type == tea.KeyLeft || msg.String() == "h":
				m.helpDialog = m.helpDialog.PrevTab()
			case msg.Type == tea.KeyDown || msg.String() == "j":
				m.helpDialog = m.helpDialog.ScrollDown(1)
			case msg.Type == tea.KeyUp || msg.String() == "k":
				m.helpDialog = m.helpDialog.ScrollUp(1)
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "stats":
			// BUG-5: Route keyboard input to the stats panel when it is open.
			switch msg.String() {
			case "r":
				m.statsPanel = m.statsPanel.TogglePeriod()
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "model" && m.modelConfigMode && m.modelConfigKeyInputMode:
			// Character input in config panel key input mode.
			switch {
			case msg.Type == tea.KeyCtrlU:
				m.modelConfigKeyInput = ""
			case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyDelete:
				if len(m.modelConfigKeyInput) > 0 {
					m.modelConfigKeyInput = m.modelConfigKeyInput[:len(m.modelConfigKeyInput)-1]
				}
			case msg.Type == tea.KeyRunes:
				m.modelConfigKeyInput += string(msg.Runes)
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "model" && m.modelConfigMode && !m.modelConfigKeyInputMode:
			// Navigation in config panel (not in key input mode).
			// Determine maximum section: 0=gateway, 1=apikey, 2=reasoning (only if reasoning model).
			maxSection := 1
			if m.modelConfigEntry.ReasoningMode {
				maxSection = 2
			}
			isDown := msg.String() == "j" || msg.Type == tea.KeyDown
			isUp := msg.String() == "k" || msg.Type == tea.KeyUp
			isLeft := msg.String() == "h" || msg.Type == tea.KeyLeft
			isRight := msg.String() == "l" || msg.Type == tea.KeyRight
			switch {
			case m.modelConfigSection == 2 && m.modelConfigEntry.ReasoningMode && isDown:
				// Down in reasoning section: navigate reasoning cursor.
				n := len(modelswitcher.ReasoningLevels)
				if n > 0 {
					m.modelConfigReasoningCursor = (m.modelConfigReasoningCursor + 1) % n
				}
			case m.modelConfigSection == 2 && m.modelConfigEntry.ReasoningMode && isUp:
				// Up in reasoning section: navigate reasoning cursor.
				n := len(modelswitcher.ReasoningLevels)
				if n > 0 {
					m.modelConfigReasoningCursor = (m.modelConfigReasoningCursor - 1 + n) % n
				}
			case isDown:
				// Move to next section.
				m.modelConfigSection = (m.modelConfigSection + 1) % (maxSection + 1)
			case isUp:
				// Move to previous section.
				m.modelConfigSection = (m.modelConfigSection - 1 + maxSection + 1) % (maxSection + 1)
			case isLeft && m.modelConfigSection == 0:
				// Left in gateway section: move cursor left.
				if m.modelConfigGatewayCursor > 0 {
					m.modelConfigGatewayCursor--
				}
			case isRight && m.modelConfigSection == 0:
				// Right in gateway section: move cursor right.
				if m.modelConfigGatewayCursor < len(gatewayOptions)-1 {
					m.modelConfigGatewayCursor++
				}
			case msg.String() == "K" || (m.modelConfigSection == 1 && msg.Type == tea.KeyEnter):
				// Enter key input mode for apikey section.
				if m.modelConfigSection == 1 {
					m.modelConfigKeyInputMode = true
				}
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "apikeys" && m.apiKeyInputMode:
			// Character input in apikeys input mode.
			switch {
			case msg.Type == tea.KeyCtrlU:
				m.apiKeyInput = ""
			case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyDelete:
				if len(m.apiKeyInput) > 0 {
					m.apiKeyInput = m.apiKeyInput[:len(m.apiKeyInput)-1]
				}
			case msg.Type == tea.KeyRunes:
				m.apiKeyInput += string(msg.Runes)
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "apikeys" && !m.apiKeyInputMode && (msg.String() == "j" || msg.String() == "k" || msg.String() == "up" || msg.String() == "down" || msg.Type == tea.KeyUp || msg.Type == tea.KeyDown):
			// Navigation in apikeys list mode.
			switch {
			case msg.String() == "up" || msg.String() == "k" || msg.Type == tea.KeyUp:
				if len(m.apiKeyProviders) > 0 {
					m.apiKeyCursor = (m.apiKeyCursor - 1 + len(m.apiKeyProviders)) % len(m.apiKeyProviders)
				}
			case msg.String() == "down" || msg.String() == "j" || msg.Type == tea.KeyDown:
				if len(m.apiKeyProviders) > 0 {
					m.apiKeyCursor = (m.apiKeyCursor + 1) % len(m.apiKeyProviders)
				}
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "provider" && (msg.String() == "j" || msg.String() == "k"):
			// vim-style j/k navigation in the provider overlay.
			if msg.String() == "k" {
				m.gatewaySelected = (m.gatewaySelected - 1 + len(gatewayOptions)) % len(gatewayOptions)
			} else {
				m.gatewaySelected = (m.gatewaySelected + 1) % len(gatewayOptions)
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "profiles":
			// Route all keys to the profile picker component.
			var ppCmd tea.Cmd
			m.profilePicker, ppCmd = m.profilePicker.Update(msg)
			if ppCmd != nil {
				cmds = append(cmds, ppCmd)
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "sessions":
			// Route all keys to the session picker component.
			// Enter triggers sessionpicker.SessionSelectedMsg which we translate
			// to SessionPickerSelectedMsg in the block below.
			// 'd' triggers sessionpicker.SessionDeletedMsg which we pass through.
			var spCmd tea.Cmd
			m.sessionPicker, spCmd = m.sessionPicker.Update(msg)
			if spCmd != nil {
				// The session picker emits sessionpicker.SessionSelectedMsg on Enter
				// and sessionpicker.SessionDeletedMsg on 'd'.
				// Unwrap both to our own message types so the switch-case below handles them.
				cmds = append(cmds, func() tea.Msg {
					raw := spCmd()
					if sel, ok := raw.(sessionpicker.SessionSelectedMsg); ok {
						return SessionPickerSelectedMsg{SessionID: sel.Entry.ID}
					}
					if del, ok := raw.(sessionpicker.SessionDeletedMsg); ok {
						return SessionDeletedMsg{ID: del.ID}
					}
					return raw
				})
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "permissions":
			// Route Up/Down/k/j to SelectUp/SelectDown; t/Enter to Toggle; d to Remove.
			switch {
			case msg.Type == tea.KeyUp || msg.String() == "k":
				m.permissionsPanel = m.permissionsPanel.SelectUp()
			case msg.Type == tea.KeyDown || msg.String() == "j":
				m.permissionsPanel = m.permissionsPanel.SelectDown()
			case msg.String() == "t" || msg.Type == tea.KeyEnter || msg.String() == " ":
				m.permissionsPanel = m.permissionsPanel.ToggleSelected()
			case msg.String() == "d":
				m.permissionsPanel = m.permissionsPanel.RemoveSelected()
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "dashboard":
			if msg.String() == "p" || msg.Type == tea.KeyEnter {
				if run, ok := m.dashboardSelected(); ok {
					if m.dashboard.stopPeek != nil {
						m.dashboard.stopPeek()
					}
					m.dashboard.peekID = run.displayID()
					m.dashboard.peek = nil
					m.dashboard.peekCh, m.dashboard.stopPeek = startSSEForRun(m.config.BaseURL, run.displayID(), m.config.APIKey)
					cmds = append(cmds, pollSSECmd(m.dashboard.peekCh))
				}
				return m, tea.Batch(cmds...)
			}
			if len(m.dashboard.runs) > 0 {
				switch {
				case msg.Type == tea.KeyUp || msg.String() == "k":
					m.dashboard.cursor = (m.dashboard.cursor - 1 + len(m.dashboard.runs)) % len(m.dashboard.runs)
				case msg.Type == tea.KeyDown || msg.String() == "j":
					m.dashboard.cursor = (m.dashboard.cursor + 1) % len(m.dashboard.runs)
				}
			}
			return m, tea.Batch(cmds...)
		case m.overlayActive && m.activeOverlay == "search" && (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown || msg.String() == "k" || msg.String() == "j"):
			// Navigate search results with Up/Down or vim k/j.
			isUp := msg.Type == tea.KeyUp || msg.String() == "k"
			if len(m.searchResults) > 0 {
				if isUp {
					m.searchSelectedIdx = (m.searchSelectedIdx - 1 + len(m.searchResults)) % len(m.searchResults)
				} else {
					m.searchSelectedIdx = (m.searchSelectedIdx + 1) % len(m.searchResults)
				}
			}
			return m, tea.Batch(cmds...)
		case key.Matches(msg, m.keys.ScrollUp):
			// When the provider overlay is active, Up/Down navigates the gateway list.
			if m.overlayActive && m.activeOverlay == "provider" {
				m.gatewaySelected = (m.gatewaySelected - 1 + len(gatewayOptions)) % len(gatewayOptions)
				return m, tea.Batch(cmds...)
			}
			// When the model overlay is active, Up navigates based on browse level and search state.
			if m.overlayActive && m.activeOverlay == "model" && !m.modelConfigMode {
				if m.modelSwitcher.BrowseLevel() == 0 && !m.modelSwitcher.SearchActive() {
					m.modelSwitcher = m.modelSwitcher.ProviderUp()
				} else {
					m.modelSwitcher = m.modelSwitcher.SelectUp()
				}
				return m, tea.Batch(cmds...)
			}
			// When the dropdown is active, Up navigates the dropdown.
			if m.slashComplete.IsActive() {
				m.slashComplete = m.slashComplete.Up()
				return m, tea.Batch(cmds...)
			}
			// When no overlay or dropdown is active, Up navigates input history.
			// Always send canonical KeyUp so inputarea handles ctrl+p identically to Up.
			if !m.overlayActive {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyUp})
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
			m.vp.ScrollUp(1)
		case key.Matches(msg, m.keys.ScrollDown):
			// When the provider overlay is active, Down navigates the gateway list.
			if m.overlayActive && m.activeOverlay == "provider" {
				m.gatewaySelected = (m.gatewaySelected + 1) % len(gatewayOptions)
				return m, tea.Batch(cmds...)
			}
			// When the model overlay is active, Down navigates based on browse level and search state.
			if m.overlayActive && m.activeOverlay == "model" && !m.modelConfigMode {
				if m.modelSwitcher.BrowseLevel() == 0 && !m.modelSwitcher.SearchActive() {
					m.modelSwitcher = m.modelSwitcher.ProviderDown()
				} else {
					m.modelSwitcher = m.modelSwitcher.SelectDown()
				}
				return m, tea.Batch(cmds...)
			}
			// When the dropdown is active, Down navigates the dropdown.
			if m.slashComplete.IsActive() {
				m.slashComplete = m.slashComplete.Down()
				return m, tea.Batch(cmds...)
			}
			// When no overlay or dropdown is active, Down navigates input history.
			// Always send canonical KeyDown so inputarea handles ctrl+n identically to Down.
			if !m.overlayActive {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyDown})
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
			m.vp.ScrollDown(1)
		case key.Matches(msg, m.keys.PageUp):
			m.vp.ScrollUp(m.vp.Height() / 2)
		case key.Matches(msg, m.keys.PageDown):
			m.vp.ScrollDown(m.vp.Height() / 2)
		case key.Matches(msg, m.keys.GotoTop):
			m.vp.ScrollUp(m.vp.LineCount())
		case key.Matches(msg, m.keys.GotoBottom):
			m.vp.ScrollToBottom()

		// #668 (1): ctrl+u clears the main input when no overlay is active.
		// The apikeys and model-config overlay arms above already matched earlier
		// in their own overlay-specific case branches, so this arm only fires
		// when no overlay is open.
		case msg.Type == tea.KeyCtrlU && !m.overlayActive:
			m.input = m.input.Clear()
			cmds = append(cmds, m.setStatusMsg("Input cleared"))
			return m, tea.Batch(cmds...)

		// #668 (2): "?" opens help when input is empty; falls through to input
		// when input is non-empty.  ctrl+h (also bound to m.keys.Help) always
		// opens help regardless of input content.
		case key.Matches(msg, m.keys.Help) && !m.overlayActive:
			// "?" with non-empty input: do NOT consume — let it type into the input.
			if msg.Type == tea.KeyRunes && string(msg.Runes) == "?" && m.input.Value() != "" {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.slashComplete = syncSlashComplete(m.slashComplete, m.input.Value())
				return m, tea.Batch(cmds...)
			}
			// ctrl+h, or "?" with empty input: open help overlay.
			executeHelpCommand(&m, Command{})
			return m, tea.Batch(cmds...)

		// #668 (3): "@" inserts into the input when no overlay is active.
		// The combined autocomplete provider (already wired) will offer file-path
		// completions when the user subsequently presses Tab.
		case key.Matches(msg, m.keys.AtMention) && !m.overlayActive:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Sync the slash-complete dropdown (will be a no-op for "@").
			m.slashComplete = syncSlashComplete(m.slashComplete, m.input.Value())
			return m, tea.Batch(cmds...)

		// #668 (4): ctrl+e launches $EDITOR when set; shows a status message when
		// $EDITOR is unset so the key is never a silent no-op.
		case msg.Type == tea.KeyCtrlE && !m.overlayActive:
			editor := os.Getenv("EDITOR")
			if editor == "" {
				cmds = append(cmds, m.setStatusMsg("$EDITOR not set"))
				return m, tea.Batch(cmds...)
			}
			// $EDITOR is set: write current input to a temp file and open it.
			// Load the edited content back when the editor exits.
			currentInput := m.input.Value()
			tmpFile, tmpErr := writeTempEditorFile(currentInput)
			if tmpErr != nil {
				cmds = append(cmds, m.setStatusMsg("editor: could not create temp file"))
				return m, tea.Batch(cmds...)
			}
			return m, tea.ExecProcess(
				editorExecCommand(editor, tmpFile),
				func(err error) tea.Msg {
					return editorDoneMsg{tmpFile: tmpFile, err: err}
				},
			)

		case key.Matches(msg, m.keys.Dashboard) && !m.overlayActive:
			cmds = append(cmds, m.dashboardOpenCmds()...)
			return m, tea.Batch(cmds...)

		default:
			// When model overlay is open (not config panel), intercept keys for navigation, search, and star.
			if m.overlayActive && m.activeOverlay == "model" && !m.modelConfigMode {
				switch msg.Type {
				case tea.KeyBackspace, tea.KeyDelete:
					q := m.modelSwitcher.SearchQuery()
					if len(q) > 0 {
						runes := []rune(q)
						m.modelSwitcher = m.modelSwitcher.SetSearch(string(runes[:len(runes)-1]))
					}
					return m, tea.Batch(cmds...)
				case tea.KeyRunes:
					// "/" explicitly enters search mode (matches what the
					// level-0 and level-1 footers advertise). It never
					// leaks into the query itself — HandleSearchKey also
					// swallows it (fixes #667) — but unlike before, it now
					// actually does something: it activates SearchActive()
					// immediately so the very next keystroke (even "s" or
					// "j"/"k") is treated as a literal query character
					// instead of a browse-level shortcut (fixes BUG C:
					// typing "sonnet" no longer stars the highlighted
					// model on the leading "s").
					if msg.String() == "/" {
						m.modelSwitcher = m.modelSwitcher.EnterSearch()
						return m, tea.Batch(cmds...)
					}
					// j/k vim-style navigation only when search is not active.
					if !m.modelSwitcher.SearchActive() {
						if msg.String() == "k" {
							if m.modelSwitcher.BrowseLevel() == 0 {
								m.modelSwitcher = m.modelSwitcher.ProviderUp()
							} else {
								m.modelSwitcher = m.modelSwitcher.SelectUp()
							}
							return m, tea.Batch(cmds...)
						}
						if msg.String() == "j" {
							if m.modelSwitcher.BrowseLevel() == 0 {
								m.modelSwitcher = m.modelSwitcher.ProviderDown()
							} else {
								m.modelSwitcher = m.modelSwitcher.SelectDown()
							}
							return m, tea.Batch(cmds...)
						}
					}
					// 's' toggles star only when browsing at level 1 with no active search.
					// Once search mode is active (explicitly via "/", or because a
					// query is already non-empty), 's' is a literal character so users
					// can type queries containing "s" (e.g. "sonnet", "deeps").
					if msg.String() == "s" && m.modelSwitcher.BrowseLevel() == 1 && !m.modelSwitcher.SearchActive() {
						m.modelSwitcher = m.modelSwitcher.ToggleStar()
						// Persist to config.
						if persistCfg, err := harnessconfig.Load(); err == nil {
							persistCfg.StarredModels = m.modelSwitcher.StarredIDs()
							_ = harnessconfig.Save(persistCfg)
						}
						return m, tea.Batch(cmds...)
					}
					// All other printable characters accumulate into search query.
					// Route through HandleSearchKey so the component's "/" swallow
					// is respected (fixes #667: "/" must not leak into search).
					m.modelSwitcher = m.modelSwitcher.HandleSearchKey(msg.String())
					return m, tea.Batch(cmds...)
				}
				return m, tea.Batch(cmds...)
			}
			// Route to input area
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			// Sync autocomplete dropdown with current input value.
			m.slashComplete = syncSlashComplete(m.slashComplete, m.input.Value())
		}

	case inputarea.CommandSubmittedMsg:
		// Whitespace-only submissions are never valid — neither a slash command
		// nor a meaningful user message — so bail out before touching history,
		// the dropdown, or the transcript.
		if strings.TrimSpace(msg.Value) == "" {
			return m, tea.Batch(cmds...)
		}
		// Persist command history to config on every submit.
		// The input has already pushed the entry into its history at this point.
		m.historyStore = m.input.HistoryState()
		if persistCfg, err := harnessconfig.Load(); err == nil {
			persistCfg.HistoryEntries = m.historyStore.Entries()
			_ = harnessconfig.Save(persistCfg)
		}
		// Close the dropdown whenever a command is submitted.
		m.slashComplete = m.slashComplete.Close()
		// Check if it's a slash command; dispatch if so.
		if cmd, ok := ParseCommand(msg.Value); ok {
			// Plugin commands (bash or prompt handlers loaded from disk) only set
			// CommandEntry.Handler, never Execute — built-in commands always set
			// Execute. Dispatch those asynchronously so a slow bash plugin (up to
			// its 10s timeout) can't block Update() and freeze rendering, input,
			// and SSE polling for the duration of the command.
			if entry, found := m.commandRegistry.Lookup(cmd.Name); found && entry.Execute == nil && entry.Handler != nil {
				cmds = append(cmds, m.setStatusMsg("Running /"+cmd.Name+"..."), runPluginCommandCmd(entry, cmd))
				return m, tea.Batch(cmds...)
			}
			result := m.commandRegistry.Dispatch(cmd)
			switch result.Status {
			case CmdOK:
				entry, found := m.commandRegistry.Lookup(cmd.Name)
				if !found {
					cmds = append(cmds, m.setStatusMsg(UnknownResult(cmd.Name).Hint))
					return m, tea.Batch(cmds...)
				}
				if entry.Execute != nil {
					execCmds, quit := entry.Execute(&m, cmd)
					cmds = append(cmds, execCmds...)
					if quit {
						return m, tea.Quit
					}
				}
				if result.Output != "" {
					m.vp.AppendLine(result.Output)
					m.vp.AppendLine("")
				}
			case CmdError:
				cmds = append(cmds, m.setStatusMsg(result.Output))
			case CmdUnknown:
				cmds = append(cmds, m.setStatusMsg(result.Hint))
			}
			return m, tea.Batch(cmds...)
		}
		// Normal user message: expand any @path tokens before sending.
		expandedValue, expandErr := ExpandAtPaths(msg.Value)
		if expandErr != nil {
			cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("file expand error: %s", expandErr)))
			// Restore the input so the user can fix the path and re-submit.
			m.input = m.input.SetValue(msg.Value)
			return m, tea.Batch(cmds...)
		}
		// Reset assistant text accumulator for the new user turn.
		m.lastAssistantText = ""
		m.responseStarted = false
		m.activeAssistantLineCount = 0
		m.clearThinkingBar()
		// Capture a preview of the user message so RunStartedMsg can record it as
		// LastMsg on the session entry.
		m.pendingLastMsg = truncateStr(msg.Value, 60)
		// Record in transcript (use original value for display; expanded for the API).
		m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
			Role:      "user",
			Content:   msg.Value,
			Timestamp: time.Now(),
		})
		// Add user message via the message bubble component path (show original).
		m.appendMessageBubble(messagebubble.RoleUser, msg.Value)
		// Fire off the run against the harness API with the expanded prompt.
		effModel, effProvider := m.effectiveModelAndProvider()
		cmds = append(cmds, startRunCmd(m.config.BaseURL, expandedValue, m.conversationID, effModel, effProvider, m.selectedReasoningEffort, m.selectedProfile, m.config.Workspace, m.config.APIKey))

	case AssistantDeltaMsg:
		m.lastAssistantText += msg.Delta
		m.renderActiveAssistantBubble()

	case ThinkingDeltaMsg:
		m.appendThinkingDelta(msg.Delta)

	case ToolStartMsg:
		m.handleToolStart(msg.CallID, msg.Name, msg.Input)

	case ToolResultMsg:
		m.handleToolResult(msg.CallID, msg.Output, 0)

	case ToolErrorMsg:
		errText := "tool failed"
		if msg.Err != nil {
			errText = msg.Err.Error()
		}
		m.handleToolError(msg.CallID, errText, 0)

	case ToolCallChunkMsg:
		m.handleToolChunk(msg.CallID, msg.Chunk)

	case RunStartedMsg:
		m.RunID = msg.RunID
		m.runActive = true
		m.clearThinkingBar()
		m.spinner = spinner.New(spinnerSeed(m.config)).Start()
		cmds = append(cmds, spinnerTickCmd())
		// The harness auto-assigns conversation_id = run_id when none is
		// supplied. Record this as the conversationID for subsequent turns so
		// that follow-up messages are linked to the same conversation.
		if m.conversationID == "" {
			m.conversationID = msg.RunID
		}
		// Track the session in the persistent store so it appears in /sessions.
		if m.sessionStore != nil {
			pending := m.pendingLastMsg
			existing, ok := m.sessionStore.Get(m.conversationID)
			if !ok {
				m.sessionStore.Add(StoredSessionEntry{
					ID:        m.conversationID,
					StartedAt: time.Now(),
					Model:     m.selectedModel,
					LastMsg:   pending,
				})
			} else {
				m.sessionStore.Update(m.conversationID, func(e *StoredSessionEntry) {
					e.TurnCount = existing.TurnCount + 1
					if m.selectedModel != "" {
						e.Model = m.selectedModel
					}
					if pending != "" {
						e.LastMsg = pending
					}
				})
			}
			_ = m.sessionStore.Save()
		}
		// Clear pending message preview now that it has been recorded.
		m.pendingLastMsg = ""
		// Start the SSE bridge for this run only if no cancel func is already
		// set (e.g. injected by tests via WithCancelRun). This avoids overwriting
		// a test-supplied cancel with a real HTTP bridge.
		if m.cancelRun == nil {
			m.lastEventID = ""
			m.sseReconnectAttempts = 0
			ch, cancel := startSSEForRun(m.config.BaseURL, msg.RunID, m.config.APIKey)
			m.sseCh = ch
			m.cancelRun = cancel
			cmds = append(cmds, pollSSECmd(m.sseCh))
		}

	case RunCompletedMsg:
		m.runActive = false
		m.cancelRun = nil
		m.clearThinkingBar()

	case RunFailedMsg:
		m.runActive = false
		m.cancelRun = nil
		m.sseCh = nil
		m.activeAssistantLineCount = 0
		m.responseStarted = false
		m.clearThinkingBar()
		errMsg := "run failed"
		if msg.Error != "" {
			errMsg = msg.Error
		}
		m.vp.AppendLine("✗ " + errMsg)
		m.vp.AppendLine("")

	case OverlayOpenMsg:
		m.overlayActive = true
		if msg.Kind != "" {
			m.activeOverlay = msg.Kind
		}
		// Reset help dialog state when (re-)opening via message, matching the
		// /help command handler which calls helpDialog.Open() (resets tab+scroll).
		if msg.Kind == "help" {
			m.helpDialog = m.helpDialog.Open()
		}

	case OverlayCloseMsg:
		if m.activeOverlay == "dashboard" {
			m.closeDashboard()
			break
		}
		m.overlayActive = false
		m.activeOverlay = ""
		m.helpDialog = m.helpDialog.Close()

	case ClearMsg:
		m.vp = viewport.New(m.width, m.layout.ViewportHeight)
		m.transcript = nil
		m.toolLineStarts = make(map[string]int)
		m.toolLineOrder = nil
		m.clearThinkingBar()

	case ExportTranscriptMsg:
		if msg.FilePath != "" {
			cmds = append(cmds, m.setStatusMsg("Transcript saved to "+msg.FilePath))
		} else {
			cmds = append(cmds, m.setStatusMsg("Export failed"))
		}

	case pluginCommandResultMsg:
		switch msg.Result.Status {
		case CmdOK:
			if msg.Result.Output != "" {
				m.vp.AppendLine(msg.Result.Output)
				m.vp.AppendLine("")
			}
		case CmdError:
			cmds = append(cmds, m.setStatusMsg(msg.Result.Output))
		case CmdUnknown:
			cmds = append(cmds, m.setStatusMsg(msg.Result.Hint))
		}

	case editorDoneMsg:
		// #668 (4): editor process exited. Load the edited content back into the
		// input if the editor succeeded and produced a non-empty result.
		if msg.err != nil || msg.tmpFile == "" {
			// Editor exited with an error — surface it but don't clobber input.
			cmds = append(cmds, m.setStatusMsg("Editor exited with error"))
			return m, tea.Batch(cmds...)
		}
		content, readErr := os.ReadFile(msg.tmpFile)
		_ = os.Remove(msg.tmpFile) // best-effort cleanup
		if readErr != nil {
			cmds = append(cmds, m.setStatusMsg("editor: could not read temp file"))
			return m, tea.Batch(cmds...)
		}
		// Trim trailing newline that most editors append.
		edited := strings.TrimRight(string(content), "\n")
		m.input = m.input.SetValue(edited)

	case SubagentsLoadedMsg:
		for _, line := range formatSubagentsLines(msg.Subagents) {
			m.vp.AppendLine(line)
		}
		m.vp.AppendLine("")
		cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("Loaded %d subagent(s)", len(msg.Subagents))))

	case SubagentsLoadFailedMsg:
		cmds = append(cmds, m.setStatusMsg("Load subagents failed: "+msg.Err))

	case HooksLoadedMsg:
		for _, line := range formatHooksLines(msg) {
			m.vp.AppendLine(line)
		}
		m.vp.AppendLine("")
		cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("Loaded %d hook(s), %d skipped", len(msg.Hooks), len(msg.Skipped))))

	case HooksLoadFailedMsg:
		cmds = append(cmds, m.setStatusMsg("Load hooks failed: "+msg.Err))

	case RunsFetchedMsg:
		if msg.Err != "" {
			cmds = append(cmds, m.setStatusMsg("Load runs failed: "+msg.Err))
			return m, tea.Batch(cmds...)
		}
		for _, line := range formatTUIRunLines(msg.Runs) {
			m.vp.AppendLine(line)
		}
		m.vp.AppendLine("")
		cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("Loaded %d run(s)", len(msg.Runs))))

	case DashboardRunsLoadedMsg:
		if msg.Err != "" {
			cmds = append(cmds, m.setStatusMsg("Dashboard load failed: "+msg.Err))
			break
		}
		m.dashboard.runs = msg.Runs
		if m.dashboard.cursor >= len(msg.Runs) {
			m.dashboard.cursor = len(msg.Runs) - 1
			if m.dashboard.cursor < 0 {
				m.dashboard.cursor = 0
			}
		}

	case dashboardPollTickMsg:
		if m.overlayActive && m.activeOverlay == "dashboard" {
			cmds = append(cmds, loadDashboardRunsCmd(m.config.BaseURL, m.config.APIKey), dashboardPollCmd())
		}

	case RunControlResultMsg:
		if msg.Err != "" {
			cmds = append(cmds, m.setStatusMsg(msg.Kind+" failed: "+msg.Err))
			return m, tea.Batch(cmds...)
		}
		switch msg.Kind {
		case "cancel":
			cmds = append(cmds, m.setStatusMsg("Run "+msg.RunID+" cancelling"))
		case "replay":
			m.vp.AppendLine("Replay result")
			for _, line := range strings.Split(msg.Output, "\n") {
				if strings.TrimSpace(line) != "" {
					m.vp.AppendLine(line)
				}
			}
			m.vp.AppendLine("")
			cmds = append(cmds, m.setStatusMsg("Replay finished for "+msg.RunID))
		default:
			if msg.Output != "" {
				m.vp.AppendLine(msg.Output)
				m.vp.AppendLine("")
			}
			cmds = append(cmds, m.setStatusMsg("Run command finished"))
		}

	case SSEEventMsg:
		if m.dashboard.peekID != "" {
			m.dashboard.peek = append(m.dashboard.peek, msg.EventType)
			if m.dashboard.peekCh != nil {
				cmds = append(cmds, pollSSECmd(m.dashboard.peekCh))
			}
			return m, tea.Batch(cmds...)
		}
		// Track the most recent event ID so a mid-run reconnect (see the
		// SSEDoneMsg case below) can resume exactly here via Last-Event-ID.
		if msg.ID != "" {
			m.lastEventID = msg.ID
		}
		// Route event to viewport based on type.
		switch msg.EventType {
		case "assistant.message.delta":
			var p struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil && p.Content != "" {
				// Accumulate and re-render the assistant message through the
				// glamour-backed message bubble. Re-rendering the full
				// accumulated text each delta (rather than appending raw chunks
				// with AppendChunk) is what enables markdown rendering on the
				// live stream and avoids chunk-boundary line corruption.
				m.lastAssistantText += p.Content
				m.renderActiveAssistantBubble()
			}
		case "assistant.thinking.delta":
			var p struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil && p.Content != "" {
				m.appendThinkingDelta(p.Content)
			}
		case "tool.call.started":
			var p struct {
				Tool      string          `json:"tool"`
				CallID    string          `json:"call_id"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil {
				m.handleToolStart(p.CallID, p.Tool, p.Arguments)
			}
		case "tool.output.delta":
			var p struct {
				CallID  string `json:"call_id"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil && p.CallID != "" && p.Content != "" {
				m.handleToolChunk(p.CallID, p.Content)
			}
		case "tool.call.completed":
			var p struct {
				Tool       string `json:"tool"`
				CallID     string `json:"call_id"`
				Output     string `json:"output"`
				Error      string `json:"error"`
				DurationMS int64  `json:"duration_ms"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil {
				m.toolNames[p.CallID] = p.Tool
				if p.Error != "" {
					m.handleToolError(p.CallID, p.Error, p.DurationMS)
				} else {
					m.handleToolResult(p.CallID, p.Output, p.DurationMS)
				}
			}
		case "tool.approval_required":
			var p struct {
				CallID     string          `json:"call_id"`
				Tool       string          `json:"tool"`
				Arguments  json.RawMessage `json:"arguments"`
				DeadlineAt string          `json:"deadline_at"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil {
				var deadline time.Time
				if p.DeadlineAt != "" {
					deadline, _ = time.Parse(time.RFC3339, p.DeadlineAt)
				}
				m.toolApproval = toolApprovalState{
					active:     true,
					runID:      m.RunID,
					callID:     p.CallID,
					tool:       p.Tool,
					arguments:  formatToolApprovalArguments(p.Arguments),
					deadlineAt: deadline,
				}
			}
		case "tool.approval_granted", "tool.approval_denied":
			// The decision has already been recorded server-side (e.g. from
			// another client); make sure the overlay does not linger.
			m.toolApproval = toolApprovalState{}
		case "usage.delta":
			var p struct {
				CumulativeUsage struct {
					TotalTokens int `json:"total_tokens"`
				} `json:"cumulative_usage"`
				CumulativeCostUSD float64 `json:"cumulative_cost_usd"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil {
				m.cumulativeCostUSD = p.CumulativeCostUSD
				m.statusBar.SetCost(m.cumulativeCostUSD)
				m.totalTokens = p.CumulativeUsage.TotalTokens
				// Update today's data point for the stats panel.
				m.usageDataPoints = upsertTodayDataPoint(m.usageDataPoints, 1, p.CumulativeCostUSD)
				m.statsPanel = statspanel.New(m.usageDataPoints)
				// Update context grid token count.
				m.contextGrid.UsedTokens = m.totalTokens
				m.statusBar.SetContext(m.totalTokens, m.contextWindowTotal())
				// Keep the /cost overlay's snapshot current even while it is open.
				m.costDisplay = m.costDisplay.Update(costSnapshotFromModel(&m))
			}
		case "run.waiting_for_user":
			// Extract run_id from the event payload, then fetch pending questions.
			var p struct {
				RunID  string `json:"run_id"`
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal(msg.Raw, &p); err == nil && p.RunID != "" {
				m.askUser = askUserState{active: true, runID: p.RunID, callID: p.CallID}
				cmds = append(cmds, fetchAskUserPendingCmd(m.config.BaseURL, p.RunID, m.config.APIKey))
			}
		case "run.resumed":
			// Dismiss the ask-user overlay when the run resumes.
			m.askUser = askUserState{}
		}
		// Continue polling the SSE channel.
		if m.sseCh != nil {
			cmds = append(cmds, pollSSECmd(m.sseCh))
		}

	case SSEErrorMsg:
		m.vp.AppendLine("⚠ stream error: " + msg.Err.Error())
		if m.sseCh != nil {
			cmds = append(cmds, pollSSECmd(m.sseCh))
		}

	case SSEDoneMsg:
		// "bridge.fatal" marks a permanent failure (401/403/404 — see
		// isNonRetryableSSEStatus in bridge.go) that a reconnect cannot fix;
		// it is treated as terminal here specifically so it is NOT retried
		// and does NOT get the generic "could not be re-established" message
		// below (the SSEErrorMsg delivered immediately before this already
		// carried a single, specific, actionable explanation).
		isTerminal := msg.EventType == "run.completed" || msg.EventType == "run.failed" || msg.EventType == "bridge.fatal"

		// The connection ended without a genuine run.completed/run.failed
		// event — e.g. the server dropped the TCP connection mid-burst. The
		// server supports resuming via Last-Event-ID (internal/server/
		// http_runs.go), so reconnect instead of treating the run as
		// finished, as long as the run is still active and we have not
		// exhausted our bounded retry budget.
		if !isTerminal && m.runActive && m.sseReconnectAttempts < maxSSEReconnectAttempts {
			m.sseReconnectAttempts++
			if m.cancelRun != nil {
				m.cancelRun()
				m.cancelRun = nil
			}
			m.sseCh = nil
			cmds = append(cmds, reconnectSSECmd(m.config.BaseURL, m.RunID, m.lastEventID, m.config.APIKey, m.sseReconnectAttempts))
			return m, tea.Batch(cmds...)
		}

		if !isTerminal && m.sseReconnectAttempts >= maxSSEReconnectAttempts {
			m.vp.AppendLine(fmt.Sprintf("⚠ SSE stream lost and could not be re-established after %d attempt(s)", m.sseReconnectAttempts))
			m.vp.AppendLine("")
		}
		m.sseReconnectAttempts = 0
		m.runActive = false
		m.sseCh = nil
		m.responseStarted = false
		m.activeAssistantLineCount = 0
		m.clearThinkingBar()
		if m.cancelRun != nil {
			m.cancelRun()
			m.cancelRun = nil
		}
		// Record completed assistant response in transcript.
		if m.lastAssistantText != "" {
			m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
				Role:      "assistant",
				Content:   m.lastAssistantText,
				Timestamp: time.Now(),
			})
		}
		if msg.EventType == "run.failed" {
			for _, line := range formatRunError(msg.Error) {
				m.vp.AppendLine(line)
			}
		}
		m.vp.AppendLine("")

	case SSEReconnectedMsg:
		// A backed-off reconnect attempt (see reconnectSSECmd) has
		// established a new connection. Only adopt it if the run is still
		// active — it may have been cancelled or already finished while the
		// reconnect backoff was pending, in which case the freshly-opened
		// connection must be closed immediately rather than resurrecting a
		// dead run.
		if !m.runActive {
			if msg.Cancel != nil {
				msg.Cancel()
			}
			return m, nil
		}
		m.sseCh = msg.Ch
		m.cancelRun = msg.Cancel
		cmds = append(cmds, pollSSECmd(m.sseCh))

	case SSEDropMsg:
		// Dropped message — continue polling.
		if m.sseCh != nil {
			cmds = append(cmds, pollSSECmd(m.sseCh))
		}

	case AskUserPendingMsg:
		// Pending questions fetched — populate the overlay and start deadline timer.
		// Accept both when already activated by run.waiting_for_user and when
		// delivered directly (e.g. in tests or future code paths).
		if (!m.askUser.active || m.askUser.runID == msg.RunID) && len(msg.Questions) > 0 {
			m.askUser = askUserState{
				active:      true,
				runID:       msg.RunID,
				callID:      msg.CallID,
				questions:   msg.Questions,
				deadlineAt:  msg.DeadlineAt,
				selectedIdx: 0,
				qIdx:        0,
			}
			if !msg.DeadlineAt.IsZero() {
				cmds = append(cmds, askUserDeadlineCmd(msg.RunID, msg.CallID, msg.DeadlineAt))
			}
		}

	case AskUserSubmittedMsg:
		// Answer accepted by the server — overlay already dismissed on Enter.
		_ = msg

	case AskUserSubmitErrorMsg:
		// Submission failed — show error in status bar.
		cmds = append(cmds, m.setStatusMsg("ask user: "+msg.Err))

	case ToolApprovalDecidedMsg:
		// Decision accepted by the server — overlay already dismissed on keypress.
		_ = msg

	case ToolApprovalErrorMsg:
		// Approve/deny request failed — show error in status bar rather than
		// leaving the run hanging silently.
		cmds = append(cmds, m.setStatusMsg("tool approval: "+msg.Err))

	case AskUserTimeoutMsg:
		// Deadline passed — dismiss overlay only if this timeout matches the
		// *current* callID. A stale timer from an earlier question must not
		// dismiss a newer question overlay.
		if m.askUser.active && msg.RunID == m.askUser.runID && msg.CallID == m.askUser.callID {
			m.askUser = askUserState{}
			m.vp.AppendLine("⚠ question timed out — run may continue or fail")
		}

	case askUserFetchErrorMsg:
		// Failed to fetch pending input — show error and clear overlay.
		m.askUser = askUserState{}
		cmds = append(cmds, m.setStatusMsg("fetch input failed: "+msg.err))

	case ModelsFetchedMsg:
		currentStarred := m.modelSwitcher.StarredIDs()
		m.modelSwitcher = m.modelSwitcher.WithModels(msg.Models).SetLoading(false)
		m.modelSwitcher = m.modelSwitcher.WithStarred(currentStarred)
		// For OpenRouter models, availability depends solely on the OpenRouter API key.
		if msg.Source == "openrouter" {
			orKeySet := m.providerKeyConfigured("openrouter")
			m.modelSwitcher = m.modelSwitcher.WithKeyStatus(func(_ string) bool {
				return orKeySet
			})
			m.modelSwitcher = m.modelSwitcher.WithAvailability(func(_ string) bool {
				return orKeySet
			})
		} else {
			m.modelSwitcher = m.modelSwitcher.WithKeyStatus(m.providerKeyConfigured)
			m.modelSwitcher = m.modelSwitcher.WithAvailability(m.providerKeyConfigured)
		}

	case ModelsFetchErrorMsg:
		m.modelSwitcher = m.modelSwitcher.SetLoadError("Error loading models: " + msg.Err)

	case ModelSelectedMsg:
		// Preserve starred models when model is selected.
		currentStarred := m.modelSwitcher.StarredIDs()
		m.selectedModel = msg.ModelID
		m.selectedProvider = msg.Provider
		m.selectedReasoningEffort = msg.ReasoningEffort
		m.modelSwitcher = modelswitcher.New(msg.ModelID)
		m.modelSwitcher = m.modelSwitcher.WithCurrentReasoning(msg.ReasoningEffort)
		m.modelSwitcher = m.modelSwitcher.WithStarred(currentStarred)
		m.statusBar.SetModel(m.statusBarModelLabel())
		// Keep the /cost overlay's model label current even if it is already
		// open and no usage.delta event arrives before the next render.
		m.costDisplay = m.costDisplay.Update(costSnapshotFromModel(&m))
		label := displayModelName(msg.ModelID)
		if msg.ReasoningEffort != "" {
			label += " (" + msg.ReasoningEffort + ")"
		}
		// Gap 3 (#315): codex models use the OpenAI API key; surface a clear
		// instruction when the model is selected but OpenAI is not configured.
		if isCodexModel(msg.ModelID) && !m.providerKeyConfigured(msg.Provider) {
			cmds = append(cmds, m.setStatusMsg(
				"Codex uses your OpenAI API key. Set OPENAI_API_KEY or enter it via /keys.",
			))
		} else {
			cmds = append(cmds, m.setStatusMsg("Model: "+label))
		}

	case GatewaySelectedMsg:
		m.selectedGateway = msg.Gateway
		hcfg, _ := harnessconfig.Load()
		hcfg.Gateway = msg.Gateway
		_ = harnessconfig.Save(hcfg)
		m.statusBar.SetModel(m.statusBarModelLabel())
		label := "Gateway: Direct"
		if msg.Gateway == "openrouter" {
			label = "Gateway: OpenRouter"
		}
		cmds = append(cmds, m.setStatusMsg(label))

	case ProvidersLoadedMsg:
		providers := make([]apiKeyProvider, len(msg.Providers))
		for i, p := range msg.Providers {
			providers[i] = apiKeyProvider{
				Name:       p.Name,
				Configured: p.Configured,
				APIKeyEnv:  p.APIKeyEnv,
			}
		}
		m.apiKeyProviders = providers
		// Wire key status to the model switcher for the Level-0 indicator dots.
		m.modelSwitcher = m.modelSwitcher.WithKeyStatus(m.providerKeyConfigured)
		// Wire availability so the model switcher renders unavailable models as dimmed/greyed.
		m.modelSwitcher = m.modelSwitcher.WithAvailability(m.providerKeyConfigured)
		// Gap 2 (#315): when ALL providers are unconfigured, show an empty-state hint.
		if len(providers) > 0 {
			allUnconfigured := true
			for _, p := range providers {
				if p.Configured {
					allUnconfigured = false
					break
				}
			}
			if allUnconfigured {
				cmds = append(cmds, m.setStatusMsg("No providers configured — press / then keys to add API keys"))
			}
		}

	case APIKeySetMsg:
		// Save to persistent config.
		hcfg, _ := harnessconfig.Load()
		if hcfg.APIKeys == nil {
			hcfg.APIKeys = make(map[string]string)
		}
		hcfg.APIKeys[msg.Provider] = msg.Key
		_ = harnessconfig.Save(hcfg)
		// Refresh provider list.
		cmds = append(cmds, fetchProvidersCmd(m.config.BaseURL, m.config.APIKey))
		cmds = append(cmds, m.setStatusMsg("Key saved for "+msg.Provider))

	case statusTickMsg:
		// Only clear if the message hasn't been replaced with a newer one.
		if m.statusMsg != "" && time.Now().After(m.statusMsgExpiry) {
			m.statusMsg = ""
			m.statusMsgExpiry = time.Time{}
		}

	case spinner.SpinnerTickMsg:
		// Only keep animating (and rescheduling) while a run is active; this
		// lets the loop stop on its own once the run ends.
		if m.runActive {
			m.spinner = m.spinner.Tick().SetAction(m.currentSpinnerAction())
			cmds = append(cmds, spinnerTickCmd())
		}

	case pluginWarningMsg:
		// Show a transient notice that some plugins failed to load.
		cmds = append(cmds, m.setStatusMsg(msg.text))

	case ProfilesLoadedMsg:
		if msg.Err != nil {
			m.overlayActive = false
			m.activeOverlay = ""
			cmds = append(cmds, m.setStatusMsg("Load profiles failed: "+msg.Err.Error()))
			return m, tea.Batch(cmds...)
		}
		entries := make([]profilepicker.ProfileEntry, len(msg.Entries))
		for i, e := range msg.Entries {
			entries[i] = profilepicker.ProfileEntry{
				Name:        e.Name,
				Description: e.Description,
				Model:       e.Model,
				ToolCount:   e.ToolCount,
				SourceTier:  e.SourceTier,
			}
		}
		m.profilePicker = profilepicker.New(entries).Open()
		m.profilePicker.Width = m.width

	case profilepicker.ProfileSelectedMsg:
		m.selectedProfile = msg.Entry.Name
		m.profilePicker = m.profilePicker.Close()
		m.overlayActive = false
		m.activeOverlay = ""
		cmds = append(cmds, m.setStatusMsg("Profile: "+msg.Entry.Name))

	case SessionPickerSelectedMsg:
		m.conversationID = msg.SessionID
		m.sessionPicker = m.sessionPicker.Close()
		m.overlayActive = false
		m.activeOverlay = ""
		// Clear the viewport and transcript so stale messages from the previous
		// conversation are not shown alongside the resumed session.
		m.vp = viewport.New(m.width, m.layout.ViewportHeight)
		m.transcript = nil
		m.lastAssistantText = ""
		m.responseStarted = false
		m.activeAssistantLineCount = 0
		// Show a system message so the user knows the session switch happened.
		m.vp.AppendLine("↩ Resumed session " + msg.SessionID + ". Previous messages are on the server.")
		m.vp.AppendLine("")
		cmds = append(cmds, m.setStatusMsg("Switched to session "+msg.SessionID[:min8(msg.SessionID)]))

	case SessionDeletedMsg:
		// Remove from the persistent store and refresh the picker list.
		if m.sessionStore != nil {
			m.sessionStore.Delete(msg.ID)
			_ = m.sessionStore.Save()
			m.sessionPicker = m.sessionPicker.SetEntries(sessionEntriesToPicker(m.sessionStore.List()))
		}

	case TranscriptEntryMsg:
		// Test-only message: inject a transcript entry directly without a full run.
		m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: time.Now(),
		})

	case ConversationHistoryMsg:
		for _, entry := range msg.Messages {
			switch entry.Role {
			case "user":
				m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
					Role:      "user",
					Content:   entry.Content,
					Timestamp: time.Now(),
				})
				m.appendMessageBubble(messagebubble.RoleUser, entry.Content)
			case "assistant":
				if entry.Content == "" {
					continue
				}
				m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
					Role:      "assistant",
					Content:   entry.Content,
					Timestamp: time.Now(),
				})
				m.appendMessageBubble(messagebubble.RoleAssistant, entry.Content)
			}
		}
		shortID := msg.ConversationID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("Resumed conversation %s (%d messages)", shortID, len(msg.Messages))))

	case ConversationHistoryErrorMsg:
		cmds = append(cmds, m.setStatusMsg(fmt.Sprintf("Could not load conversation %s: %s", msg.ConversationID, msg.Err)))
	}

	return m, tea.Batch(cmds...)
}

// truncateStr returns s truncated to at most n runes.
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// min8 returns the minimum of 8 and the length of s, used for truncating IDs.
func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}

// View implements tea.Model -- composes all components.
func (m Model) View() string {
	if !m.ready {
		return "Initializing...\n"
	}

	sep := m.renderSeparator()

	// Render the status bar, optionally with a transient status message overlay.
	statusBarView := m.statusBar.View()
	if m.statusMsg != "" && !time.Now().After(m.statusMsgExpiry) {
		statusBarView = m.statusMsg
	}

	// Render viewport OR active overlay.
	var mainContent string
	if m.askUser.active {
		// Ask-user overlay takes priority over all other content.
		overlayLines := m.renderAskUserOverlay()
		if len(overlayLines) > 0 {
			mainContent = m.vp.View() + "\n" + strings.Join(overlayLines, "\n")
		} else {
			mainContent = m.vp.View()
		}
	} else if m.toolApproval.active {
		// Tool-approval overlay takes the same priority as ask-user.
		overlayLines := m.renderToolApprovalOverlay()
		if len(overlayLines) > 0 {
			mainContent = m.vp.View() + "\n" + strings.Join(overlayLines, "\n")
		} else {
			mainContent = m.vp.View()
		}
	} else if m.overlayActive {
		switch m.activeOverlay {
		case "help":
			mainContent = m.helpDialog.View(m.width, m.layout.ViewportHeight)
		case "stats":
			// Box the stats overlay for consistent chrome (#666).
			mainContent = boxOverlay(m.statsPanel.SetWidth(m.width-2).View(), m.width)
		case "context":
			// Box the context overlay for consistent chrome (#666).
			cg := m.contextGrid
			cg.Width = m.width - 2
			raw := cg.View()
			if raw == "" {
				raw = "Context grid not available"
			}
			mainContent = boxOverlay(raw, m.width)
		case "cost":
			cd := m.costDisplay
			cd.Width = m.width - 2
			raw := cd.View()
			if raw == "" {
				raw = "No cost data yet"
			}
			mainContent = boxOverlay(raw, m.width)
		case "config":
			mainContent = m.configPanel.View(m.width, m.layout.ViewportHeight)
		case "model":
			if m.modelConfigMode {
				mainContent = m.viewModelConfigPanel()
			} else {
				mainContent = m.modelSwitcher.WithMaxHeight(m.layout.ViewportHeight).View(m.width)
			}
		case "provider":
			mainContent = m.viewProviderOverlay()
		case "apikeys":
			mainContent = m.viewAPIKeysOverlay()
		case "profiles":
			m.profilePicker.Width = m.width
			mainContent = m.profilePicker.View()
			if mainContent == "" {
				mainContent = m.vp.View()
			}
		case "sessions":
			m.sessionPicker.Width = m.width
			mainContent = m.sessionPicker.View(m.width)
			if mainContent == "" {
				mainContent = m.vp.View()
			}
		case "search":
			mainContent = m.viewSearchOverlay()
		case "permissions":
			m.permissionsPanel.Width = m.width
			m.permissionsPanel.Height = m.layout.ViewportHeight
			raw := m.permissionsPanel.View()
			mainContent = boxOverlay(raw, m.width)
		case "dashboard":
			mainContent = boxOverlay(m.dashboardView(), m.width)
		default:
			// Unknown overlay kind — fall back to viewport.
			mainContent = m.vp.View()
		}
	} else {
		if m.selectedModel == "" && m.vp.IsEmpty() && !m.thinkingBar.Active {
			// Welcome hint for first-time users who have no model configured.
			hintStyle := lipgloss.NewStyle().Faint(true)
			mainContent = lipgloss.Place(
				m.width, m.layout.ViewportHeight,
				lipgloss.Center, lipgloss.Center,
				hintStyle.Render("Type /model to select a model  •  Type /help for all commands"),
			)
		} else {
			mainContent = m.vp.View()
		}
	}

	// Stack: main content / separator / thinking / interrupt banner / autocomplete dropdown / input / separator / status bar
	inputView := m.input.View()
	dropdownView := m.slashComplete.View(m.width)
	thinkingView := m.thinkingBar.View()
	bannerView := m.interruptBanner.View()

	sections := []string{
		mainContent,
		sep,
	}
	if thinkingView != "" {
		sections = append(sections, thinkingView)
	} else if m.runActive {
		// The thinking bar is empty while a tool call is executing (or
		// between streamed thinking deltas); fill that gap with the spinner
		// so the run always has a persistent, animated indicator plus a
		// cancel hint.
		if spinnerView := m.spinner.View(m.width); spinnerView != "" {
			sections = append(sections, spinnerView)
		}
	}
	if bannerView != "" {
		sections = append(sections, bannerView)
	}
	if dropdownView != "" {
		sections = append(sections, dropdownView)
	}
	sections = append(sections, inputView, sep, statusBarView)

	return strings.Join(sections, "\n")
}

func (m Model) renderSeparator() string {
	if m.width <= 0 {
		return ""
	}
	return layout.NewSeparator(m.width, false).Render()
}

// buildCommandRegistry returns the authoritative built-in slash command registry.
func (m *Model) buildCommandRegistry() *CommandRegistry {
	return NewCommandRegistry()
}

// upsertTodayDataPoint updates (or inserts) a DataPoint for today in the given
// slice.  count is added to the existing count and cost replaces the cost
// (since usage.delta carries cumulative values).
func upsertTodayDataPoint(pts []statspanel.DataPoint, count int, cost float64) []statspanel.DataPoint {
	today := time.Now()
	todayKey := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	for i := range pts {
		dp := pts[i]
		k := time.Date(dp.Date.Year(), dp.Date.Month(), dp.Date.Day(), 0, 0, 0, 0, time.UTC)
		if k.Equal(todayKey) {
			pts[i].Count += count
			pts[i].Cost = cost
			return pts
		}
	}
	return append(pts, statspanel.DataPoint{
		Date:  todayKey,
		Count: count,
		Cost:  cost,
	})
}

func formatTUIRunLines(runs []tuiRunRecord) []string {
	if len(runs) == 0 {
		return []string{"Runs", "No runs found."}
	}
	lines := []string{"Runs", "ID                        STATUS              MODEL                PROMPT"}
	for _, run := range runs {
		id := run.displayID()
		model := run.Model
		if model == "" {
			model = "(default)"
		}
		prompt := run.Prompt
		if len([]rune(prompt)) > 40 {
			runes := []rune(prompt)
			prompt = string(runes[:37]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%-24s  %-18s  %-20s %s", id, run.Status, model, prompt))
	}
	return lines
}

func formatSubagentsLines(items []RemoteSubagent) []string {
	if len(items) == 0 {
		return []string{"No managed subagents."}
	}

	lines := make([]string, 0, len(items)*2)
	for _, item := range items {
		summary := fmt.Sprintf("%s [%s] %s (%s)", item.ID, item.Status, item.Isolation, item.CleanupPolicy)
		if item.WorkspaceCleaned {
			summary += " cleaned"
		}
		lines = append(lines, summary)

		details := make([]string, 0, 3)
		if item.BranchName != "" {
			details = append(details, "branch="+item.BranchName)
		}
		if item.BaseRef != "" {
			details = append(details, "base="+item.BaseRef)
		}
		if item.WorkspacePath != "" {
			details = append(details, "path="+item.WorkspacePath)
		}
		if len(details) > 0 {
			lines = append(lines, "  "+strings.Join(details, " "))
		}
	}

	return lines
}

// effectiveModelAndProvider returns the model ID and provider to use for run requests,
// accounting for the selected gateway.
//
// When the gateway is "openrouter" the model is mapped to its OpenRouter slug and
// the provider is forced to "openrouter".
//
// When the gateway is direct (non-openrouter) and the selected model is an
// OpenRouter-qualified slug (e.g. "deepseek/deepseek-v4-flash"), the slug is
// normalised back to a native provider model ID via NativeFromOpenRouterSlug.
func (m Model) effectiveModelAndProvider() (model, provider string) {
	if m.selectedGateway == "openrouter" {
		return modelswitcher.OpenRouterSlug(m.selectedModel), "openrouter"
	}
	modelID := m.selectedModel
	// When the model list was sourced from OpenRouter, selectedModel may be
	// an OpenRouter slug (e.g. "deepseek/deepseek-v4-flash"). For a direct
	// provider call we must send the native model ID instead.
	if strings.Contains(modelID, "/") {
		modelID = modelswitcher.NativeFromOpenRouterSlug(modelID)
	}
	return modelID, m.selectedProvider
}

// statusBarModelLabel returns the status bar label for the currently selected model,
// including reasoning effort suffix and gateway indicator if applicable.
func (m Model) statusBarModelLabel() string {
	label := displayModelName(m.selectedModel)
	if m.selectedReasoningEffort != "" {
		label += " (" + m.selectedReasoningEffort + ")"
	}
	if m.selectedGateway == "openrouter" {
		label += " " + string('↗') + "OR"
	}
	return label
}

// viewProviderOverlay renders the gateway selection overlay.
func (m Model) viewProviderOverlay() string {
	width := 60
	title := "Routing Gateway"

	var rows []string
	for i, opt := range gatewayOptions {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.gatewaySelected {
			cursor = string('▶') + " "
			style = style.Foreground(lipgloss.Color("220")).Bold(true)
		}
		label := style.Render(fmt.Sprintf("%s%-12s %s", cursor, opt.Label, opt.Desc))
		rows = append(rows, label)
	}

	footer := lipgloss.NewStyle().Faint(true).Render(string('↑') + "/" + string('↓') + " navigate  enter confirm  esc close")

	content := strings.Join(rows, "\n") + "\n\n" + footer

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Render(title),
			"",
			content,
		))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// SelectedGateway returns the currently active routing gateway (for testing).
func (m Model) SelectedGateway() string { return m.selectedGateway }

// viewAPIKeysOverlay renders the API key management overlay.
func (m Model) viewAPIKeysOverlay() string {
	width := 54

	if m.apiKeyInputMode && len(m.apiKeyProviders) > 0 {
		p := m.apiKeyProviders[m.apiKeyCursor]
		title := "API Keys > " + p.Name

		envLine := lipgloss.NewStyle().Faint(true).Render(p.APIKeyEnv)
		inputLine := "> " + m.apiKeyInput + "\u258c" // block cursor
		footer := lipgloss.NewStyle().Faint(true).Render("enter confirm  ctrl+u clear  esc back")

		content := envLine + "\n\n" + inputLine + "\n\n" + footer

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2).
			Width(width).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Render(title),
				"",
				content,
			))

		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}

	// List mode.
	title := "API Keys"

	configuredStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	unsetStyle := lipgloss.NewStyle().Faint(true)

	var rows []string
	for i, p := range m.apiKeyProviders {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.apiKeyCursor {
			cursor = string('\u25b6') + " "
			style = style.Foreground(lipgloss.Color("220")).Bold(true)
		}
		status := unsetStyle.Render("\u25cb unset")
		if p.Configured {
			status = configuredStyle.Render("\u25cf set")
		}
		label := style.Render(fmt.Sprintf("%s%-14s %-24s", cursor, p.Name, p.APIKeyEnv))
		rows = append(rows, label+" "+status)
	}

	if len(rows) == 0 {
		rows = append(rows, "  No providers available")
	}

	footer := lipgloss.NewStyle().Faint(true).Render(string('\u2191') + "/" + string('\u2193') + " navigate  enter edit  esc close")

	content := strings.Join(rows, "\n") + "\n\n" + footer

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(width).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Render(title),
			"",
			content,
		))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// viewModelConfigPanel renders the Level-1 model configuration panel.
// It shows model name, provider, gateway selection, API key status, and
// optionally reasoning effort selection (for reasoning models).
func (m Model) viewModelConfigPanel() string {
	width := 54
	const borderAndPad = 8 // border 2 + padding 2*2 each side

	focusedSectionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	dimStyle := lipgloss.NewStyle().Faint(true)
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	configuredStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	unconfiguredStyle := lipgloss.NewStyle().Faint(true)

	entry := m.modelConfigEntry

	// Model name and provider header.
	title := lipgloss.NewStyle().Bold(true).Render(entry.DisplayName)
	providerLine := dimStyle.Render(entry.ProviderLabel)

	var sections []string

	// --- Gateway section ---
	isFocusedGateway := m.modelConfigSection == 0
	var gwLabel string
	if isFocusedGateway {
		gwLabel = focusedSectionStyle.Render("Gateway")
	} else {
		gwLabel = "Gateway"
	}

	var gwRows []string
	for i, opt := range gatewayOptions {
		isSelected := i == m.modelConfigGatewayCursor
		var rowStyle lipgloss.Style
		var cursor string
		if isSelected {
			cursor = cursorStyle.Render("▶") + " "
			rowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
		} else {
			cursor = "  "
			rowStyle = dimStyle
		}
		row := cursor + rowStyle.Render(fmt.Sprintf("%-12s %s", opt.Label, opt.Desc))
		gwRows = append(gwRows, row)
	}
	gatewaySection := gwLabel + "\n" + strings.Join(gwRows, "\n")
	sections = append(sections, gatewaySection)

	// --- API Key section ---
	isFocusedKey := m.modelConfigSection == 1
	var keyLabel string
	if isFocusedKey {
		keyLabel = focusedSectionStyle.Render("API Key")
	} else {
		keyLabel = "API Key"
	}

	keyConfigured := m.providerKeyConfigured(entry.Provider)
	var keyStatusStr string
	if keyConfigured {
		keyStatusStr = configuredStyle.Render("● configured")
	} else {
		keyStatusStr = unconfiguredStyle.Render("○ not set")
	}

	var keyContent string
	if m.modelConfigKeyInputMode {
		keyContent = keyLabel + "    " + keyStatusStr + "\n" +
			"> " + m.modelConfigKeyInput + "\u258c" + "\n" +
			dimStyle.Render("enter confirm  ctrl+u clear  esc back")
	} else {
		var keyHint string
		if isFocusedKey {
			keyHint = dimStyle.Render("  (K to update)")
		}
		keyContent = keyLabel + "    " + keyStatusStr + keyHint
	}
	sections = append(sections, keyContent)

	// --- Reasoning section (reasoning models only) ---
	if entry.ReasoningMode {
		isFocusedReasoning := m.modelConfigSection == 2
		var reasoningLabel string
		if isFocusedReasoning {
			reasoningLabel = focusedSectionStyle.Render("Reasoning Effort")
		} else {
			reasoningLabel = "Reasoning Effort"
		}

		var reasoningRows []string
		for i, rl := range modelswitcher.ReasoningLevels {
			isSelected := i == m.modelConfigReasoningCursor
			var row string
			if isSelected {
				row = cursorStyle.Render("▶") + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render(rl.DisplayName)
			} else {
				row = "  " + dimStyle.Render(rl.DisplayName)
			}
			reasoningRows = append(reasoningRows, row)
		}
		reasoningSection := reasoningLabel + "\n" + strings.Join(reasoningRows, "\n")
		sections = append(sections, reasoningSection)
	}

	// --- Footer ---
	var footer string
	if !m.modelConfigKeyInputMode {
		footer = dimStyle.Render("↑/↓ sections  ←/→ gateway  enter confirm  esc back")
	}

	var innerContent string
	if footer != "" {
		innerContent = title + "\n" + providerLine + "\n\n" +
			strings.Join(sections, "\n\n") + "\n\n" + footer
	} else {
		innerContent = title + "\n" + providerLine + "\n\n" +
			strings.Join(sections, "\n\n")
	}

	_ = borderAndPad

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(width).
		Render(innerContent)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// ModelConfigMode returns true when the Level-1 model config panel is active (for testing).
func (m Model) ModelConfigMode() bool { return m.modelConfigMode }

// ModelConfigEntry returns the model entry being configured (for testing).
func (m Model) ModelConfigEntry() modelswitcher.ModelEntry { return m.modelConfigEntry }

// ModelConfigSection returns the focused section index in the config panel (for testing).
func (m Model) ModelConfigSection() int { return m.modelConfigSection }

// ModelConfigGatewayCursor returns the gateway cursor in the config panel (for testing).
func (m Model) ModelConfigGatewayCursor() int { return m.modelConfigGatewayCursor }

// ModelConfigReasoningCursor returns the reasoning cursor in the config panel (for testing).
func (m Model) ModelConfigReasoningCursor() int { return m.modelConfigReasoningCursor }

// ModelConfigKeyInputMode returns true when the config panel key input is active (for testing).
func (m Model) ModelConfigKeyInputMode() bool { return m.modelConfigKeyInputMode }

// ModelConfigKeyInput returns the current key input text in the config panel (for testing).
func (m Model) ModelConfigKeyInput() string { return m.modelConfigKeyInput }

// APIKeyInputMode returns true when the /keys overlay is in input mode (for testing).
func (m Model) APIKeyInputMode() bool { return m.apiKeyInputMode }

// APIKeyInput returns the current input text in the /keys overlay (for testing).
func (m Model) APIKeyInput() string { return m.apiKeyInput }

// APIKeyProviders returns the provider list in the /keys overlay (for testing).
func (m Model) APIKeyProviders() []apiKeyProvider { return m.apiKeyProviders }

// APIKeyCursor returns the current cursor position in the /keys overlay provider list (for testing).
func (m Model) APIKeyCursor() int { return m.apiKeyCursor }

// ModelSwitcher returns the current modelswitcher Model (for testing).
func (m Model) ModelSwitcher() modelswitcher.Model { return m.modelSwitcher }

// StatusBarView returns the raw status bar view, bypassing any transient status
// message overlay. This is used by tests to verify that the status bar correctly
// stores and renders model name and cost independent of status messages.
func (m Model) StatusBarView() string { return m.statusBar.View() }

// providerIndexInAPIKeyList returns the index of the given provider name in the
// apiKeyProviders list, or -1 if not found.
func (m Model) providerIndexInAPIKeyList(providerName string) int {
	for i, p := range m.apiKeyProviders {
		if p.Name == providerName {
			return i
		}
	}
	return -1
}

// isCodexModel returns true when the modelID refers to a Codex-family model
// (i.e. its ID contains "codex").
func isCodexModel(modelID string) bool {
	return strings.Contains(strings.ToLower(modelID), "codex")
}

// HelpDialogActiveTab returns the currently active tab index in the help dialog (for testing).
func (m Model) HelpDialogActiveTab() int { return int(m.helpDialog.ActiveTab()) }

// HelpDialogScrollOffset returns the current scroll offset of the help dialog (for testing).
func (m Model) HelpDialogScrollOffset() int { return m.helpDialog.ScrollOffset() }

// boxOverlay wraps overlay content in a RoundedBorder lipgloss box so that
// /stats and /context get consistent chrome matching /help, /model etc.
func boxOverlay(content string, width int) string {
	if width <= 2 {
		return content
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Width(width - 2).
		Render(content)
}

// StatsPanelActivePeriod returns the currently active period in the stats panel (for testing).
func (m Model) StatsPanelActivePeriod() int { return int(m.statsPanel.ActivePeriod()) }

// editorDoneMsg is sent when the external editor process launched by ctrl+e exits.
type editorDoneMsg struct {
	// tmpFile is the path of the temp file that was edited.
	tmpFile string
	// err is non-nil when the editor process itself failed.
	err error
}

// writeTempEditorFile creates a temp file seeded with content and returns its path.
func writeTempEditorFile(content string) (string, error) {
	f, err := os.CreateTemp("", "harnesscli-edit-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// editorExecCommand returns an *exec.Cmd that opens file in the given editor.
func editorExecCommand(editor, file string) *exec.Cmd {
	cmd := exec.Command(editor, file)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}
