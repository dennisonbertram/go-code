package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// This file implements the theme token schema and JSON theme loader for the
// TUI (epic #810, slice 1). A theme file is a flat JSON object whose keys are
// color tokens; each token is either a single color string applied to both
// light and dark backgrounds, or an adaptive {"light": "...", "dark": "..."}
// object. Colors are "#rgb"/"#rrggbb" hex or ANSI-256 numbers ("0"–"255").
// Every token is optional: omitted, empty, or unparseable values fall back
// individually (per token and per side) to the built-in base palette derived
// from DefaultTheme(), so a theme file can never break rendering.

// ColorSpec is one token value from a theme file. An empty side means "fall
// back to the base palette for that side".
type ColorSpec struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// UnmarshalJSON accepts either a plain string (applied to both backgrounds)
// or an adaptive {"light","dark"} object. Any other shape is treated as
// omitted so a single malformed token cannot fail the whole load.
func (c *ColorSpec) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.Light, c.Dark = s, s
		return nil
	}
	type adaptive ColorSpec
	var a adaptive
	if err := json.Unmarshal(data, &a); err == nil {
		c.Light, c.Dark = a.Light, a.Dark
		return nil
	}
	c.Light, c.Dark = "", ""
	return nil
}

// TokenSet is the theme-file schema: ~17 named color tokens aligned with
// kimi-code's theme set. All tokens are optional.
type TokenSet struct {
	Primary        ColorSpec `json:"primary"`
	Accent         ColorSpec `json:"accent"`
	Text           ColorSpec `json:"text"`
	TextStrong     ColorSpec `json:"textStrong"`
	TextDim        ColorSpec `json:"textDim"`
	TextMuted      ColorSpec `json:"textMuted"`
	Border         ColorSpec `json:"border"`
	BorderFocus    ColorSpec `json:"borderFocus"`
	Success        ColorSpec `json:"success"`
	Warning        ColorSpec `json:"warning"`
	Error          ColorSpec `json:"error"`
	DiffAdd        ColorSpec `json:"diffAdd"`
	DiffRemove     ColorSpec `json:"diffRemove"`
	DiffHunk       ColorSpec `json:"diffHunk"`
	RoleUser       ColorSpec `json:"roleUser"`
	ShellMode      ColorSpec `json:"shellMode"`
	CodeBackground ColorSpec `json:"codeBackground"`
}

// entries lists every token with its schema name. Kept in sync with TokenSet
// by the token-coverage test.
func (s TokenSet) entries() []struct {
	name string
	spec ColorSpec
} {
	return []struct {
		name string
		spec ColorSpec
	}{
		{"primary", s.Primary},
		{"accent", s.Accent},
		{"text", s.Text},
		{"textStrong", s.TextStrong},
		{"textDim", s.TextDim},
		{"textMuted", s.TextMuted},
		{"border", s.Border},
		{"borderFocus", s.BorderFocus},
		{"success", s.Success},
		{"warning", s.Warning},
		{"error", s.Error},
		{"diffAdd", s.DiffAdd},
		{"diffRemove", s.DiffRemove},
		{"diffHunk", s.DiffHunk},
		{"roleUser", s.RoleUser},
		{"shellMode", s.ShellMode},
		{"codeBackground", s.CodeBackground},
	}
}

// builtinThemeNames are the built-in base themes derived from DefaultTheme().
var builtinThemeNames = []string{"default-dark", "default-light"}

// BuiltinThemeNames returns the built-in base theme names. Both resolve to
// DefaultTheme(): the dark/light variants are the two sides of its adaptive
// colors, which the terminal selects per detected background.
func BuiltinThemeNames() []string {
	return append([]string{}, builtinThemeNames...)
}

// tokenBaseColors is the built-in base palette, derived from the colors
// DefaultTheme() uses today (theme.go). Tokens whose base styles carry no
// color have an empty base (renderer default).
var tokenBaseColors = map[string]lipgloss.AdaptiveColor{
	"primary":        {Light: "#874BFD", Dark: "#7D56F4"}, // highlight
	"accent":         {},
	"text":           {},
	"textStrong":     {},
	"textDim":        {Light: "#9B9B9B", Dark: "#5C5C5C"}, // dimColor
	"textMuted":      {Light: "#D9D9D9", Dark: "#383838"}, // subtle
	"border":         {Light: "#D9D9D9", Dark: "#383838"}, // subtle
	"borderFocus":    {Light: "#874BFD", Dark: "#7D56F4"}, // highlight
	"success":        {Light: "#43BF6D", Dark: "#73F59F"}, // special
	"warning":        {Light: "#FFAF00", Dark: "#FFAF00"},
	"error":          {Light: "#FF5F87", Dark: "#FF5F87"},
	"diffAdd":        {Light: "#23A244", Dark: "#23A244"},
	"diffRemove":     {Light: "#E05252", Dark: "#E05252"},
	"diffHunk":       {Light: "#9B9B9B", Dark: "#5C5C5C"}, // dimColor
	"roleUser":       {},
	"shellMode":      {Light: "#FFAF00", Dark: "#FFAF00"},
	"codeBackground": {Light: "#D9D9D9", Dark: "#383838"}, // subtle
}

var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// validColor reports whether s is a usable color: "#rgb"/"#rrggbb" hex or an
// ANSI-256 palette number in "0"–"255". Empty strings are not valid (they
// mean "unset", which falls back rather than overriding).
func validColor(s string) bool {
	if hexColorRe.MatchString(s) {
		return true
	}
	if len(s) < 1 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	return err == nil && n <= 255
}

// DefaultThemesDir returns the directory JSON theme files are loaded from:
// ~/.config/harnesscli/themes (alongside config.json).
func DefaultThemesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "harnesscli", "themes"), nil
}

// LoadTheme resolves the named theme from dir into a complete Theme.
//
// Built-in base themes ("default-dark", "default-light") resolve to
// DefaultTheme(). A missing theme file resolves to the base palette with no
// error. A malformed JSON file returns the base palette plus an error so
// callers can surface the problem while staying renderable. Within a valid
// file, every token falls back individually (and per light/dark side) to the
// base palette when omitted or invalid.
func LoadTheme(dir, name string) (Theme, error) {
	base := DefaultTheme()
	for _, b := range builtinThemeNames {
		if name == b {
			return base, nil
		}
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return base, fmt.Errorf("invalid theme name %q", name)
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return base, nil
		}
		return base, fmt.Errorf("read theme %q: %w", name, err)
	}
	var set TokenSet
	if err := json.Unmarshal(data, &set); err != nil {
		return base, fmt.Errorf("parse theme %q: %w", name, err)
	}
	return resolveTokenSet(set), nil
}

// ListThemes returns the available theme names: the built-in base themes
// followed by every *.json file in dir, filename minus extension, sorted.
// A missing directory yields just the built-ins.
func ListThemes(dir string) ([]string, error) {
	names := BuiltinThemeNames()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return names, nil
		}
		return names, fmt.Errorf("list themes in %s: %w", dir, err)
	}
	var fileNames []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fn := e.Name()
		if strings.HasPrefix(fn, ".") || !strings.HasSuffix(fn, ".json") {
			continue
		}
		fileNames = append(fileNames, strings.TrimSuffix(fn, ".json"))
	}
	sort.Strings(fileNames)
	return append(names, fileNames...), nil
}

// resolveTokenSet overlays each explicitly-set, valid token onto a copy of
// DefaultTheme(). Tokens without a single valid side leave the base style
// untouched, which is what makes fallback per-token and keeps the default
// appearance byte-identical for empty themes.
func resolveTokenSet(set TokenSet) Theme {
	t := DefaultTheme()
	for _, e := range set.entries() {
		c, ok := e.spec.merge(tokenBaseColors[e.name])
		if !ok {
			continue
		}
		applyToken(&t, e.name, c)
	}
	return t
}

// merge combines a token value with its base color per side: valid sides
// override, invalid or empty sides fall back. ok is false when neither side
// held a valid color.
func (c ColorSpec) merge(base lipgloss.AdaptiveColor) (lipgloss.AdaptiveColor, bool) {
	out := base
	ok := false
	if validColor(c.Light) {
		out.Light = c.Light
		ok = true
	}
	if validColor(c.Dark) {
		out.Dark = c.Dark
		ok = true
	}
	return out, ok
}

// applyToken binds one resolved token color to its Theme style fields. Every
// Theme field is bound to exactly one token; the field-coverage test enforces
// this. borderFocus and shellMode are parsed and resolved but not yet bound —
// they are consumed by component-level styling in a later slice of #810.
func applyToken(t *Theme, name string, c lipgloss.AdaptiveColor) {
	switch name {
	case "primary":
		t.ToolNameStyle = t.ToolNameStyle.Foreground(c)
		t.InputPromptStyle = t.InputPromptStyle.Foreground(c)
	case "accent":
		t.StatusBarStyle = t.StatusBarStyle.Foreground(c)
	case "text":
		t.AssistantMsgStyle = t.AssistantMsgStyle.Foreground(c)
		t.InputStyle = t.InputStyle.Foreground(c)
		t.ItalicStyle = t.ItalicStyle.Foreground(c)
	case "textStrong":
		t.BoldStyle = t.BoldStyle.Foreground(c)
		t.StatusModelStyle = t.StatusModelStyle.Foreground(c)
	case "textDim":
		t.ThinkingStyle = t.ThinkingStyle.Foreground(c)
		t.ToolResultStyle = t.ToolResultStyle.Foreground(c)
		t.CostStyle = t.CostStyle.Foreground(c)
		t.TimingStyle = t.TimingStyle.Foreground(c)
		t.SeparatorStyle = t.SeparatorStyle.Foreground(c)
		t.DimStyle = t.DimStyle.Foreground(c)
	case "textMuted":
		t.ToolInputStyle = t.ToolInputStyle.Foreground(c)
	case "border":
		t.BorderStyle = t.BorderStyle.BorderForeground(c)
	case "success":
		t.SuccessStyle = t.SuccessStyle.Foreground(c)
	case "warning":
		t.WarningStyle = t.WarningStyle.Foreground(c)
	case "error":
		t.ErrorStyle = t.ErrorStyle.Foreground(c)
	case "diffAdd":
		t.DiffAddStyle = t.DiffAddStyle.Foreground(c)
	case "diffRemove":
		t.DiffRemoveStyle = t.DiffRemoveStyle.Foreground(c)
	case "diffHunk":
		t.DiffHunkStyle = t.DiffHunkStyle.Foreground(c)
	case "roleUser":
		t.UserMsgStyle = t.UserMsgStyle.Foreground(c)
	case "codeBackground":
		t.CodeStyle = t.CodeStyle.Background(c)
	}
}
