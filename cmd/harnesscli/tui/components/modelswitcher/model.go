package modelswitcher

import (
	"sort"
	"strings"
)

// ModelEntry holds display information for a single LLM model.
type ModelEntry struct {
	ID            string // e.g. "gpt-4.1-mini"
	DisplayName   string // e.g. "GPT-4.1 Mini"
	Provider      string // provider key for API (e.g. "openai", "anthropic")
	ProviderLabel string // human-readable provider name for display (e.g. "OpenAI")
	ReasoningMode bool   // true for reasoning models (deepseek-reasoner, qwen-qwq-32b, etc.)
	IsCurrent     bool
	// Available indicates whether this model's provider is currently configured with an API key.
	// When false the model is still selectable (the run will fail with a clear backend error),
	// but the view renders it in a muted/greyed style. Zero value (false) means "unknown" —
	// no availability info has been loaded yet — and the view shows no indicator.
	Available bool
}

// DefaultModels is the list of available models shown by New(), grouped by
// provider. This is the offline/client-side fallback shown whenever the live
// GET /v1/models fetch has not yet completed or has failed, so it must be
// kept in sync with catalog/models.json for every "built-in" provider (see
// bugB_catalog_sync_test.go, which fails on drift). Entries below mirror
// catalog/models.json's display_name field exactly for these providers:
// openai, anthropic, gemini, deepseek, xai, groq, qwen, kimi. OpenRouter and
// Together models are always fetched live and are intentionally not
// hardcoded here.
var DefaultModels = []ModelEntry{
	// OpenAI
	{ID: "gpt-4.1", DisplayName: "GPT-4.1", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-4.1-mini", DisplayName: "GPT-4.1 Mini", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-5.1-codex", DisplayName: "GPT-5.1 Codex", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-5.1-codex-mini", DisplayName: "GPT-5.1 Codex Mini", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-5.1-codex-max", DisplayName: "GPT-5.1 Codex Max", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-5.2-codex", DisplayName: "GPT-5.2 Codex", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex", Provider: "openai", ProviderLabel: "OpenAI"},
	{ID: "computer-use-preview", DisplayName: "Computer Use Preview", Provider: "openai", ProviderLabel: "OpenAI"},
	// Anthropic
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Provider: "anthropic", ProviderLabel: "Anthropic"},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", Provider: "anthropic", ProviderLabel: "Anthropic"},
	{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", Provider: "anthropic", ProviderLabel: "Anthropic"},
	// Google
	{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", Provider: "gemini", ProviderLabel: "Google"},
	{ID: "gemini-2.0-flash", DisplayName: "Gemini 2.0 Flash", Provider: "gemini", ProviderLabel: "Google"},
	{ID: "gemini-2.5-flash-preview-04-17", DisplayName: "Gemini 2.5 Flash Preview", Provider: "gemini", ProviderLabel: "Google"},
	// DeepSeek
	{ID: "deepseek-chat", DisplayName: "DeepSeek Chat (V3)", Provider: "deepseek", ProviderLabel: "DeepSeek"},
	{ID: "deepseek-reasoner", DisplayName: "DeepSeek Reasoner (R1)", Provider: "deepseek", ProviderLabel: "DeepSeek", ReasoningMode: true},
	{ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro (direct)", Provider: "deepseek", ProviderLabel: "DeepSeek"},
	{ID: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash (direct)", Provider: "deepseek", ProviderLabel: "DeepSeek"},
	// xAI
	{ID: "grok-3-mini", DisplayName: "Grok 3 Mini", Provider: "xai", ProviderLabel: "xAI"},
	{ID: "grok-4-1-fast-reasoning", DisplayName: "Grok 4.1 Fast Reasoning", Provider: "xai", ProviderLabel: "xAI", ReasoningMode: true},
	// Groq
	{ID: "llama-3.3-70b-versatile", DisplayName: "Llama 3.3 70B Versatile", Provider: "groq", ProviderLabel: "Groq"},
	{ID: "qwen-qwq-32b", DisplayName: "Qwen QwQ 32B", Provider: "groq", ProviderLabel: "Groq", ReasoningMode: true},
	// Qwen
	{ID: "qwen-plus", DisplayName: "Qwen Plus", Provider: "qwen", ProviderLabel: "Qwen"},
	{ID: "qwen-turbo", DisplayName: "Qwen Turbo", Provider: "qwen", ProviderLabel: "Qwen"},
	// Kimi
	{ID: "kimi-k2.5", DisplayName: "Kimi K2.5", Provider: "kimi", ProviderLabel: "Kimi"},
}

// modelDisplayNames maps model ID to human-readable display name.
// Used to enrich server-fetched model entries. Kept in sync with
// DefaultModels (see comment above).
var modelDisplayNames = map[string]string{
	"gpt-4.1":                        "GPT-4.1",
	"gpt-4.1-mini":                   "GPT-4.1 Mini",
	"gpt-5.1-codex":                  "GPT-5.1 Codex",
	"gpt-5.1-codex-mini":             "GPT-5.1 Codex Mini",
	"gpt-5.1-codex-max":              "GPT-5.1 Codex Max",
	"gpt-5.2-codex":                  "GPT-5.2 Codex",
	"gpt-5.3-codex":                  "GPT-5.3 Codex",
	"computer-use-preview":           "Computer Use Preview",
	"claude-sonnet-4-6":              "Claude Sonnet 4.6",
	"claude-opus-4-6":                "Claude Opus 4.6",
	"claude-haiku-4-5-20251001":      "Claude Haiku 4.5",
	"gemini-2.5-flash":               "Gemini 2.5 Flash",
	"gemini-2.0-flash":               "Gemini 2.0 Flash",
	"gemini-2.5-flash-preview-04-17": "Gemini 2.5 Flash Preview",
	"deepseek-chat":                  "DeepSeek Chat (V3)",
	"deepseek-reasoner":              "DeepSeek Reasoner (R1)",
	"deepseek-v4-pro":                "DeepSeek V4 Pro (direct)",
	"deepseek-v4-flash":              "DeepSeek V4 Flash (direct)",
	"grok-3-mini":                    "Grok 3 Mini",
	"grok-4-1-fast-reasoning":        "Grok 4.1 Fast Reasoning",
	"llama-3.3-70b-versatile":        "Llama 3.3 70B Versatile",
	"qwen-qwq-32b":                   "Qwen QwQ 32B",
	"qwen-plus":                      "Qwen Plus",
	"qwen-turbo":                     "Qwen Turbo",
	"kimi-k2.5":                      "Kimi K2.5",
}

// reasoningModelIDs contains model IDs that support reasoning effort selection.
var reasoningModelIDs = map[string]bool{
	"deepseek-reasoner":       true,
	"grok-4-1-fast-reasoning": true,
	"qwen-qwq-32b":            true,
}

// providerLabels maps provider key to human-readable name.
var providerLabels = map[string]string{
	"openai":     "OpenAI",
	"anthropic":  "Anthropic",
	"gemini":     "Google",
	"deepseek":   "DeepSeek",
	"xai":        "xAI",
	"groq":       "Groq",
	"qwen":       "Qwen",
	"kimi":       "Kimi",
	"together":   "Together",
	"openrouter": "OpenRouter",
}

// ServerModelEntry is the minimal shape returned by GET /v1/models.
type ServerModelEntry struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name,omitempty"` // optional: from OpenRouter "name" field
}

// ReasoningEntry holds display information for a single reasoning effort level.
type ReasoningEntry struct {
	ID          string // "", "low", "medium", "high"
	DisplayName string // "Default", "Low", "Medium", "High"
}

// ReasoningLevels is the ordered list of reasoning effort levels.
var ReasoningLevels = []ReasoningEntry{
	{ID: "", DisplayName: "Default"},
	{ID: "low", DisplayName: "Low"},
	{ID: "medium", DisplayName: "Medium"},
	{ID: "high", DisplayName: "High"},
}

// ProviderSummary holds display information for a provider in the Level-0 list.
type ProviderSummary struct {
	Label      string // e.g. "OpenAI"
	ProviderID string // e.g. "openai"
	Count      int    // number of models for this provider
	HasCurrent bool   // true if currentModelID belongs to this provider
	Configured bool   // true if any model in this provider is Available
}

// Model is the model switcher dropdown state.
// All methods return a new Model (value semantics — safe for concurrent use
// when each goroutine holds its own copy).
type Model struct {
	Models   []ModelEntry
	Selected int // index into visibleModels()
	IsOpen   bool
	Width    int

	reasoningMode     bool   // true = Level-1 (reasoning effort) active
	reasoningSelected int    // cursor in ReasoningLevels
	currentReasoning  string // "", "low", "medium", "high"

	// currentModelID is the model ID currently in use (set by New and updated on accept).
	currentModelID string

	// searchQuery is the live filter text typed by the user.
	searchQuery string

	// starred is the set of starred model IDs.
	starred map[string]bool

	// loading is true while fetching the model list from the server.
	loading bool

	// loadError is non-empty if the fetch failed.
	loadError string

	// keyStatus is an optional function that returns true if the given provider key is configured.
	keyStatus func(string) bool

	// availabilityFn is an optional function used by WithAvailability to mark ModelEntry.Available.
	// When non-nil it is retained so that WithModels can re-apply availability to server-fetched
	// model lists.
	availabilityFn func(string) bool

	// availabilitySet is true when WithAvailability has been called at least once.
	// Distinguishes "no availability info" (false) from "all providers unconfigured" (true + fn
	// returns false for every provider). The view only shows the "(unavailable)" indicator when
	// availabilitySet is true.
	availabilitySet bool

	// browseLevel is 0 (provider list) or 1 (models for activeProvider).
	// When searchQuery != "" the UI shows a flat cross-provider list regardless.
	browseLevel int

	// activeProvider is the ProviderLabel selected in level 0 (e.g. "OpenAI").
	// Only meaningful when browseLevel == 1.
	activeProvider string

	// providerCursor is the cursor position in the provider list (level 0).
	providerCursor int

	// MaxHeight limits the rendered output height (lines). When 0 (default),
	// no limit is enforced — all items are rendered.
	MaxHeight int

	// scrollOffset tracks the starting index of the visible window in the
	// model list (level 1 and search views).
	scrollOffset int

	// providerScrollOffset tracks the starting index of the visible window
	// in the provider list (level 0).
	providerScrollOffset int
}

// New constructs a Model pre-loaded with DefaultModels, marking the entry
// whose ID matches currentModelID as IsCurrent. If no match is found, no
// entry is marked (CurrentModel falls back to first).
func New(currentModelID string) Model {
	models := make([]ModelEntry, len(DefaultModels))
	copy(models, DefaultModels)

	// Find the initial selected index.
	selected := 0
	for i := range models {
		if models[i].ID == currentModelID {
			selected = i
		}
	}

	return Model{
		Models:         models,
		Selected:       selected,
		currentModelID: currentModelID,
		starred:        make(map[string]bool),
	}
}

// Open opens the dropdown overlay, starting at level 0 (provider list).
// providerCursor is set to the index of the current model's provider.
// Scroll offsets are reset.
func (m Model) Open() Model {
	m.IsOpen = true
	m.browseLevel = 0
	m.activeProvider = ""
	m.scrollOffset = 0
	m.providerScrollOffset = 0
	// Position providerCursor at the current model's provider.
	provs := m.providers()
	cur := m.CurrentModel()
	for i, p := range provs {
		if p.Label == cur.ProviderLabel {
			m.providerCursor = i
			break
		}
	}
	return m
}

// Close closes the dropdown overlay and resets all level state.
func (m Model) Close() Model {
	m.IsOpen = false
	m.reasoningMode = false
	m.searchQuery = ""
	m.browseLevel = 0
	m.activeProvider = ""
	m.providerCursor = 0
	m.scrollOffset = 0
	m.providerScrollOffset = 0
	return m
}

// WithMaxHeight returns a copy with MaxHeight set to h. When h <= 0, no height
// limit is enforced and all items are rendered.
func (m Model) WithMaxHeight(h int) Model {
	m.MaxHeight = h
	return m
}

// maxVisibleContentRows returns the number of content rows that fit within
// MaxHeight after accounting for title, footer, search bar, scroll indicators,
// and box border chrome.
func (m Model) maxVisibleContentRows() int {
	if m.MaxHeight <= 0 {
		return 1<<31 - 1 // effectively unlimited
	}
	const overhead = 10
	visible := m.MaxHeight - overhead
	if visible < 1 {
		visible = 1
	}
	return visible
}

// adjustModelScroll keeps the model cursor visible within the scroll window.
func (m Model) adjustModelScroll(total int) Model {
	if total == 0 {
		m.scrollOffset = 0
		return m
	}
	maxVisible := m.maxVisibleContentRows()
	m.scrollOffset = adjustScroll(m.scrollOffset, m.Selected, total, maxVisible)
	return m
}

// adjustProviderScroll keeps the provider cursor visible within the scroll window.
func (m Model) adjustProviderScroll(total int) Model {
	if total == 0 {
		m.providerScrollOffset = 0
		return m
	}
	maxVisible := m.maxVisibleContentRows()
	m.providerScrollOffset = adjustScroll(m.providerScrollOffset, m.providerCursor, total, maxVisible)
	return m
}

// effectiveContentRows returns the number of item rows that can actually be
// rendered within maxVisible once room for the "... N more above/below"
// scroll indicator lines is set aside. This is the single source of truth
// for the render-window budget: adjustScroll() (below) and every render loop
// in view.go call scrollWindow(), which is built on this same function, so
// the budget used to decide *when* to scroll and the budget used to decide
// *what* to render can never drift apart again.
//
// Bug history: view.go used to shrink its own rendered window independently
// (by up to two rows) whenever the "above"/"below" indicators were shown,
// but adjustScroll() never learned about that shrinkage. Once both
// indicators were showing at once, the selected row could fall permanently
// outside the window actually rendered, freezing the visible cursor while
// the "... N more below" counter kept changing on every keypress.
//
// The reservation here is deterministic — it depends only on whether the
// list overflows the window at all (total > maxVisible), never on the
// current scroll offset — so it cannot oscillate as the user scrolls. When
// the list fits without scrolling, no rows are reserved (no indicators are
// ever shown). When it doesn't fit, two rows are always reserved, even at
// the very top or bottom of the list where only one indicator actually
// renders; that trades a little density at the extremes for a budget that
// can never point the render window past where the cursor is.
func effectiveContentRows(total, maxVisible int) int {
	if total <= maxVisible {
		return maxVisible
	}
	reserved := maxVisible - 2
	if reserved < 1 {
		reserved = 1
	}
	return reserved
}

// scrollWindow computes the render window for a scrollable list: the
// half-open [start, end) slice of the underlying list to render, and
// whether the "more above" / "more below" indicators should be shown. Every
// render loop in view.go calls this instead of independently shrinking its
// own window, so the rendered window always agrees with the budget
// adjustScroll() used to position scrollOffset.
func scrollWindow(offset, total, maxVisible int) (start, end int, showAbove, showBelow bool) {
	content := effectiveContentRows(total, maxVisible)
	start = offset
	end = start + content
	if end > total {
		end = total
	}
	showAbove = start > 0
	showBelow = end < total
	return start, end, showAbove, showBelow
}

// adjustScroll ensures the selected index is visible within the scroll window.
// Follows the pattern from profilepicker/model.go.
func adjustScroll(offset, selected, total, maxVisible int) int {
	content := effectiveContentRows(total, maxVisible)
	if total <= content {
		return 0
	}
	// Scroll down if selected moved below visible window.
	if selected >= offset+content {
		offset = selected - content + 1
	}
	// Scroll up if selected moved above visible window.
	if selected < offset {
		offset = selected
	}
	// Clamp offset to valid range.
	maxOffset := total - content
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// IsVisible reports whether the dropdown is currently shown.
func (m Model) IsVisible() bool {
	return m.IsOpen
}

// providers returns unique providers sorted alphabetically by Label.
// Each ProviderSummary includes Count, HasCurrent, and Configured.
func (m Model) providers() []ProviderSummary {
	type provData struct {
		providerID string
		count      int
		hasCurrent bool
		configured bool
	}
	// Use a map to collect per-label data, preserve order via a slice.
	seen := make(map[string]*provData)
	var order []string
	for _, e := range m.Models {
		label := e.ProviderLabel
		if label == "" {
			label = e.Provider
		}
		pd, ok := seen[label]
		if !ok {
			pd = &provData{providerID: e.Provider}
			seen[label] = pd
			order = append(order, label)
		}
		pd.count++
		if e.ID == m.currentModelID {
			pd.hasCurrent = true
		}
		if e.Available {
			pd.configured = true
		}
	}
	// Sort alphabetically by label.
	sort.Strings(order)
	result := make([]ProviderSummary, 0, len(order))
	for _, label := range order {
		pd := seen[label]
		result = append(result, ProviderSummary{
			Label:      label,
			ProviderID: pd.providerID,
			Count:      pd.count,
			HasCurrent: pd.hasCurrent,
			Configured: pd.configured,
		})
	}
	return result
}

// FilteredProviders returns providers filtered by searchQuery (case-insensitive match on Label or ProviderID).
func (m Model) FilteredProviders() []ProviderSummary {
	return m.filteredProviders()
}

// filteredProviders returns providers filtered by searchQuery (case-insensitive match on Label or ProviderID).
func (m Model) filteredProviders() []ProviderSummary {
	q := strings.ToLower(m.searchQuery)
	if q == "" {
		return m.providers()
	}
	all := m.providers()
	var result []ProviderSummary
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Label), q) || strings.Contains(strings.ToLower(p.ProviderID), q) {
			result = append(result, p)
		}
	}
	return result
}

// modelsForActiveProvider returns models filtered to activeProvider, sorted by DisplayName.
func (m Model) modelsForActiveProvider() []ModelEntry {
	var result []ModelEntry
	for _, e := range m.Models {
		label := e.ProviderLabel
		if label == "" {
			label = e.Provider
		}
		if label == m.activeProvider {
			e.IsCurrent = e.ID == m.currentModelID
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].DisplayName < result[j].DisplayName
	})
	return result
}

// visibleModels returns the filtered + ordered model list.
// - When searchQuery != "": flat cross-provider list filtered by query.
// - When browseLevel == 1: models filtered to activeProvider only.
// - When browseLevel == 0: all models (for Accept() compatibility).
// Starred models appear first in all cases, filtered by searchQuery.
// IsCurrent is set dynamically based on currentModelID.
//
// Search matches across DisplayName, ProviderLabel, Provider key, and model ID.
// Results are ranked: prefix matches before substring matches, with starred
// models first within each tier. This ensures queries like "d" or "de" surface
// DeepSeek models ahead of e.g. Anthropic models that merely contain "d"/"de"
// in their DisplayName.
func (m Model) visibleModels() []ModelEntry {
	q := strings.ToLower(m.searchQuery)

	// When in level 1 with no search: return filtered-to-provider list (starred first).
	if m.browseLevel == 1 && q == "" {
		providerModels := m.modelsForActiveProvider()
		var starred, rest []ModelEntry
		for _, e := range providerModels {
			if m.starred[e.ID] {
				starred = append(starred, e)
			} else {
				rest = append(rest, e)
			}
		}
		return append(starred, rest...)
	}

	// For browseLevel==0 OR search active: use full model list (with search filter when active).
	// Search matches against DisplayName, ProviderLabel, Provider key, and model ID.
	// Rank: prefix matches first (starred, then rest), then substring matches (starred, then rest).
	var prefixStarred, prefixRest, substrStarred, substrRest []ModelEntry
	for _, e := range m.Models {
		e.IsCurrent = e.ID == m.currentModelID
		if q != "" {
			queryFields := []string{
				strings.ToLower(e.DisplayName),
				strings.ToLower(e.ProviderLabel),
				strings.ToLower(e.Provider),
				strings.ToLower(e.ID),
			}
			matched := false
			isPrefix := false
			for _, f := range queryFields {
				if strings.HasPrefix(f, q) {
					matched = true
					isPrefix = true
					break
				}
			}
			if !isPrefix {
				for _, f := range queryFields {
					if strings.Contains(f, q) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
			starred := m.starred[e.ID]
			if isPrefix {
				if starred {
					prefixStarred = append(prefixStarred, e)
				} else {
					prefixRest = append(prefixRest, e)
				}
			} else {
				if starred {
					substrStarred = append(substrStarred, e)
				} else {
					substrRest = append(substrRest, e)
				}
			}
		} else {
			// No search query: preserve original order.
			if m.starred[e.ID] {
				prefixStarred = append(prefixStarred, e) // reuse prefixStarred for starred
			} else {
				prefixRest = append(prefixRest, e) // reuse prefixRest for rest
			}
		}
	}
	// Concatenate in priority order.
	var result []ModelEntry
	result = append(result, prefixStarred...)
	result = append(result, prefixRest...)
	result = append(result, substrStarred...)
	result = append(result, substrRest...)
	return result
}

// ProviderUp moves providerCursor up by one (wraps around).
// The scroll window is adjusted so the cursor remains visible.
func (m Model) ProviderUp() Model {
	provs := m.providers()
	n := len(provs)
	if n == 0 {
		return m
	}
	m.providerCursor = (m.providerCursor - 1 + n) % n
	return m.adjustProviderScroll(n)
}

// ProviderDown moves providerCursor down by one (wraps around).
// The scroll window is adjusted so the cursor remains visible.
func (m Model) ProviderDown() Model {
	provs := m.providers()
	n := len(provs)
	if n == 0 {
		return m
	}
	m.providerCursor = (m.providerCursor + 1) % n
	return m.adjustProviderScroll(n)
}

// DrillIntoProvider sets browseLevel=1 and activeProvider to the currently
// highlighted provider. Selected is reset to the current model if it is in
// this provider, otherwise 0. Scroll offset is reset.
func (m Model) DrillIntoProvider() Model {
	provs := m.providers()
	if len(provs) == 0 {
		return m
	}
	idx := m.providerCursor
	if idx >= len(provs) {
		idx = 0
	}
	m.browseLevel = 1
	m.activeProvider = provs[idx].Label
	m.scrollOffset = 0
	// Pre-select current model if it belongs to this provider.
	m.Selected = 0
	provModels := m.modelsForActiveProvider()
	for i, e := range provModels {
		if e.ID == m.currentModelID {
			m.Selected = i
			break
		}
	}
	return m
}

// ExitToProviderList returns to level 0, keeping providerCursor positioned at
// the activeProvider's index in the provider list. Scroll offset is reset.
func (m Model) ExitToProviderList() Model {
	provs := m.providers()
	// Find and restore cursor position.
	for i, p := range provs {
		if p.Label == m.activeProvider {
			m.providerCursor = i
			break
		}
	}
	m.browseLevel = 0
	m.activeProvider = ""
	m.scrollOffset = 0
	return m
}

// BrowseLevel returns the current browse level (0 = provider list, 1 = model list).
func (m Model) BrowseLevel() int { return m.browseLevel }

// ActiveProvider returns the currently active provider label (only meaningful at level 1).
func (m Model) ActiveProvider() string { return m.activeProvider }

// Providers is the exported version of providers() for use in tests.
func (m Model) Providers() []ProviderSummary { return m.providers() }

// ProviderCursorIndex returns the current provider cursor index.
func (m Model) ProviderCursorIndex() int { return m.providerCursor }

// SelectUp moves the cursor up by one in the visible list, wrapping around to the last entry.
// The scroll window is adjusted so the cursor remains visible.
func (m Model) SelectUp() Model {
	visible := m.visibleModels()
	n := len(visible)
	if n == 0 {
		return m
	}
	m.Selected = (m.Selected - 1 + n) % n
	return m.adjustModelScroll(n)
}

// SelectDown moves the cursor down by one in the visible list, wrapping around to the first entry.
// The scroll window is adjusted so the cursor remains visible.
func (m Model) SelectDown() Model {
	visible := m.visibleModels()
	n := len(visible)
	if n == 0 {
		return m
	}
	m.Selected = (m.Selected + 1) % n
	return m.adjustModelScroll(n)
}

// Accept returns the currently selected ModelEntry from the visible list and whether
// it differs from the IsCurrent entry. The bool is true when the selection has changed
// from the current model. The model itself is not mutated by Accept — callers should
// call Close() on the returned model when appropriate.
func (m Model) Accept() (ModelEntry, bool) {
	visible := m.visibleModels()
	if len(visible) == 0 {
		return ModelEntry{}, false
	}
	idx := m.Selected
	if idx >= len(visible) {
		idx = 0
	}
	entry := visible[idx]
	changed := entry.ID != m.currentModelID
	return entry, changed
}

// CurrentModel returns the entry with IsCurrent==true.
// If no entry is marked current, the first entry is returned.
func (m Model) CurrentModel() ModelEntry {
	for _, e := range m.Models {
		if e.ID == m.currentModelID {
			e.IsCurrent = true
			return e
		}
	}
	if len(m.Models) > 0 {
		return m.Models[0]
	}
	return ModelEntry{}
}

// IsReasoningMode reports whether the Level-1 (reasoning effort) panel is active.
func (m Model) IsReasoningMode() bool {
	return m.reasoningMode
}

// EnterReasoningMode switches to the Level-1 reasoning effort panel.
// The cursor is initialised to the index of the current reasoning level
// (falls back to 0 / "Default" when not found).
func (m Model) EnterReasoningMode() Model {
	m.reasoningMode = true
	// Find current reasoning in ReasoningLevels.
	m.reasoningSelected = 0
	for i, rl := range ReasoningLevels {
		if rl.ID == m.currentReasoning {
			m.reasoningSelected = i
			break
		}
	}
	return m
}

// ExitReasoningMode returns to the Level-0 model list without changing any selection.
func (m Model) ExitReasoningMode() Model {
	m.reasoningMode = false
	return m
}

// ReasoningUp moves the reasoning-level cursor up by one, wrapping around.
func (m Model) ReasoningUp() Model {
	n := len(ReasoningLevels)
	if n == 0 {
		return m
	}
	m.reasoningSelected = (m.reasoningSelected - 1 + n) % n
	return m
}

// ReasoningDown moves the reasoning-level cursor down by one, wrapping around.
func (m Model) ReasoningDown() Model {
	n := len(ReasoningLevels)
	if n == 0 {
		return m
	}
	m.reasoningSelected = (m.reasoningSelected + 1) % n
	return m
}

// AcceptReasoning returns the currently selected ReasoningEntry and whether it
// differs from the current reasoning level.
func (m Model) AcceptReasoning() (ReasoningEntry, bool) {
	if len(ReasoningLevels) == 0 {
		return ReasoningEntry{}, false
	}
	entry := ReasoningLevels[m.reasoningSelected]
	changed := entry.ID != m.currentReasoning
	return entry, changed
}

// WithCurrentReasoning returns a copy with currentReasoning set to effort.
func (m Model) WithCurrentReasoning(effort string) Model {
	m.currentReasoning = effort
	return m
}

// WithKeyStatus returns a copy with keyStatus set to fn. fn is called in View()
// with a provider key string and returns true if that provider has an API key
// configured. If fn is nil, no key status indicators are shown.
func (m Model) WithKeyStatus(fn func(string) bool) Model {
	m.keyStatus = fn
	return m
}

// WithAvailability marks each ModelEntry.Available based on fn(entry.Provider).
// fn receives the provider key (e.g. "openai", "anthropic") and returns true when
// that provider is currently configured. Passing nil fn leaves all models with
// Available=false. The fn is retained so that a subsequent WithModels call can
// re-apply availability to a freshly fetched server model list.
//
// Callers should invoke WithAvailability whenever the provider list is loaded or
// refreshed (typically on ProvidersLoadedMsg). Unavailable models remain selectable;
// the run will fail with a clear backend error rather than being silently hidden.
func (m Model) WithAvailability(fn func(string) bool) Model {
	result := m
	result.availabilityFn = fn
	result.availabilitySet = true

	newModels := make([]ModelEntry, len(m.Models))
	copy(newModels, m.Models)
	for i := range newModels {
		if fn != nil {
			newModels[i].Available = fn(newModels[i].Provider)
		} else {
			newModels[i].Available = false
		}
	}
	result.Models = newModels
	return result
}

// WithModels replaces the model list with server-fetched entries, enriched with display metadata.
// If WithAvailability was previously called, availability is re-applied to the new entries.
func (m Model) WithModels(serverModels []ServerModelEntry) Model {
	entries := make([]ModelEntry, 0, len(serverModels))
	for _, sm := range serverModels {
		// Use provided display name (e.g. from OpenRouter) or fall back to local map.
		displayName := sm.DisplayName
		if displayName == "" {
			displayName = modelDisplayNames[sm.ID]
		}
		if displayName == "" {
			displayName = sm.ID // raw ID as last resort
		}
		providerLabel, ok := providerLabels[sm.Provider]
		if !ok {
			providerLabel = sm.Provider
		}
		available := false
		if m.availabilitySet && m.availabilityFn != nil {
			available = m.availabilityFn(sm.Provider)
		}
		entries = append(entries, ModelEntry{
			ID:            sm.ID,
			DisplayName:   displayName,
			Provider:      sm.Provider,
			ProviderLabel: providerLabel,
			ReasoningMode: reasoningModelIDs[sm.ID],
			Available:     available,
		})
	}
	// Sort by ProviderLabel then DisplayName so provider headers appear once each
	// (OpenRouter returns models in API order, not grouped by provider).
	sort.Slice(entries, func(i, j int) bool {
		pi, pj := entries[i].ProviderLabel, entries[j].ProviderLabel
		if pi != pj {
			return pi < pj
		}
		return entries[i].DisplayName < entries[j].DisplayName
	})

	result := m
	result.Models = entries
	// Re-anchor Selected to same model ID.
	result.Selected = 0
	for i, e := range entries {
		if e.ID == m.currentModelID {
			result.Selected = i
			break
		}
	}
	return result
}

// WithStarred sets the starred model IDs from persistent config.
func (m Model) WithStarred(ids []string) Model {
	result := m
	result.starred = make(map[string]bool, len(ids))
	for _, id := range ids {
		result.starred[id] = true
	}
	return result
}

// StarredIDs returns the current set of starred model IDs (sorted, for persistence).
func (m Model) StarredIDs() []string {
	ids := make([]string, 0, len(m.starred))
	for id := range m.starred {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ToggleStar toggles the star for the currently selected visible model.
// After toggling, the cursor is re-anchored to the same model ID so that
// a re-sort (starred models float to the top) does not cause the cursor to
// jump to a different model.
func (m Model) ToggleStar() Model {
	visible := m.visibleModels()
	if len(visible) == 0 || m.Selected >= len(visible) {
		return m
	}
	// Capture the ID before toggling so we can re-anchor after re-sort.
	starredID := visible[m.Selected].ID

	result := m
	result.starred = make(map[string]bool, len(m.starred)+1)
	for k, v := range m.starred {
		result.starred[k] = v
	}
	if result.starred[starredID] {
		delete(result.starred, starredID)
	} else {
		result.starred[starredID] = true
	}

	// Re-anchor cursor: find the new index of starredID in the re-sorted visible list.
	newVisible := result.visibleModels()
	for i, e := range newVisible {
		if e.ID == starredID {
			result.Selected = i
			break
		}
	}
	return result
}

// HandleSearchKey processes a single keystroke for the search input.
// It appends printable characters to the search query, removes the last
// character on "backspace", and IGNORES "/" (which opens search mode in
// the parent model.go handler — it must never be appended to the query).
// This keeps the "/" swallow inside the component so model.go is untouched.
func (m Model) HandleSearchKey(key string) Model {
	switch key {
	case "/":
		// '/' opens search mode in the parent — swallow it here so it never
		// leaks into the query string.
		return m
	case "backspace", "ctrl+h":
		q := []rune(m.searchQuery)
		if len(q) > 0 {
			q = q[:len(q)-1]
		}
		return m.SetSearch(string(q))
	default:
		// Only append single printable characters.
		runes := []rune(key)
		if len(runes) == 1 {
			r := runes[0]
			if r >= ' ' && r != 127 {
				return m.SetSearch(m.searchQuery + key)
			}
		}
		return m
	}
}

// SetSearch sets the search query and resets Selected and scroll offset to 0.
func (m Model) SetSearch(q string) Model {
	result := m
	result.searchQuery = q
	result.Selected = 0
	result.scrollOffset = 0
	return result
}

// SearchQuery returns the current search query.
func (m Model) SearchQuery() string { return m.searchQuery }

// SetLoading sets the loading state.
func (m Model) SetLoading(v bool) Model {
	result := m
	result.loading = v
	return result
}

// Loading reports whether a server fetch is in progress.
func (m Model) Loading() bool { return m.loading }

// SetLoadError sets an error message (empty string clears it).
func (m Model) SetLoadError(err string) Model {
	result := m
	result.loadError = err
	result.loading = false
	return result
}

// LoadError returns the current load error message.
func (m Model) LoadError() string { return m.loadError }

// IsStarred returns true if the model with the given ID is starred.
func (m Model) IsStarred(id string) bool { return m.starred[id] }

// openRouterSlugs maps native model IDs to their OpenRouter equivalents.
var openRouterSlugs = map[string]string{
	"gpt-4.1":                   "openai/gpt-4.1",
	"gpt-4.1-mini":              "openai/gpt-4.1-mini",
	"claude-sonnet-4-6":         "anthropic/claude-sonnet-4-6",
	"claude-opus-4-6":           "anthropic/claude-opus-4-6",
	"claude-haiku-4-5-20251001": "anthropic/claude-haiku-4-5-20251001",
	"gemini-2.5-flash":          "google/gemini-2.5-flash",
	"gemini-2.0-flash":          "google/gemini-2.0-flash",
	"deepseek-chat":             "deepseek/deepseek-chat",
	"deepseek-reasoner":         "deepseek/deepseek-r1",
	"deepseek-v4":               "deepseek/deepseek-v4-pro",
	"deepseek-v4-flash":         "deepseek/deepseek-v4-flash",
	"grok-3-mini":               "x-ai/grok-3-mini",
	"grok-4-1-fast-reasoning":   "x-ai/grok-4",
	"llama-3.3-70b-versatile":   "meta-llama/llama-3.3-70b-instruct",
	"qwen-qwq-32b":              "qwen/qwq-32b",
	"qwen-plus":                 "qwen/qwen-plus",
	"qwen-turbo":                "qwen/qwen-turbo",
	"kimi-k2.5":                 "moonshotai/kimi-k2.5",
}

// OpenRouterSlug returns the OpenRouter model slug for the given model ID.
// Falls back to the raw ID if no mapping exists.
func OpenRouterSlug(modelID string) string {
	if slug, ok := openRouterSlugs[modelID]; ok {
		return slug
	}
	return modelID
}

// nativeFromOpenRouterSlugs is the inverse of openRouterSlugs: it maps
// OpenRouter-qualified slugs back to native model IDs.
var nativeFromOpenRouterSlugs = initNativeFromOpenRouter()

func initNativeFromOpenRouter() map[string]string {
	result := make(map[string]string, len(openRouterSlugs))
	for native, slug := range openRouterSlugs {
		result[slug] = native
	}
	return result
}

// NativeFromOpenRouterSlug strips the known OpenRouter provider prefix from a
// model slug and returns the native model ID. Falls back to the raw ID when no
// inverse mapping exists or when the slug does not contain a known prefix.
func NativeFromOpenRouterSlug(slug string) string {
	if native, ok := nativeFromOpenRouterSlugs[slug]; ok {
		return native
	}
	// When the slug has a "/" separator but no hard-coded inverse mapping,
	// try stripping the prefix generically (e.g. "openai/gpt-4.1" -> "gpt-4.1").
	if idx := strings.Index(slug, "/"); idx >= 0 {
		return slug[idx+1:]
	}
	return slug
}

// AvailabilityKnown returns true when WithAvailability has been called at least
// once for this model (i.e. provider info has been loaded). When false, all
// ModelEntry.Available fields are zero (false) — do not treat that as
// "confirmed unconfigured".
func (m Model) AvailabilityKnown() bool { return m.availabilitySet }
