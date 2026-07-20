package tui_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// Base palette values from DefaultTheme() (theme.go). Tests reference these to
// prove fallback behavior lands on the base palette, token by token.
var (
	baseSubtle    = lipgloss.AdaptiveColor{Light: "#D9D9D9", Dark: "#383838"}
	baseHighlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	baseSpecial   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	baseDim       = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	baseError     = lipgloss.AdaptiveColor{Light: "#FF5F87", Dark: "#FF5F87"}
	baseWarning   = lipgloss.AdaptiveColor{Light: "#FFAF00", Dark: "#FFAF00"}
	baseAdd       = lipgloss.AdaptiveColor{Light: "#23A244", Dark: "#23A244"}
	baseRemove    = lipgloss.AdaptiveColor{Light: "#E05252", Dark: "#E05252"}
)

func writeThemeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write theme file: %v", err)
	}
}

// assertSameTheme compares two themes field-by-field across every color and
// attribute getter, so "falls back to the base palette" means the whole style,
// not just a spot check.
func assertSameTheme(t *testing.T, want, got tui.Theme) {
	t.Helper()
	wv := reflect.ValueOf(want)
	gv := reflect.ValueOf(got)
	for i := 0; i < wv.NumField(); i++ {
		name := wv.Type().Field(i).Name
		ws := wv.Field(i).Interface().(lipgloss.Style)
		gs := gv.Field(i).Interface().(lipgloss.Style)
		if ws.GetForeground() != gs.GetForeground() {
			t.Errorf("%s foreground = %v, want base %v", name, gs.GetForeground(), ws.GetForeground())
		}
		if ws.GetBackground() != gs.GetBackground() {
			t.Errorf("%s background = %v, want base %v", name, gs.GetBackground(), ws.GetBackground())
		}
		if ws.GetBorderTopForeground() != gs.GetBorderTopForeground() {
			t.Errorf("%s border foreground = %v, want base %v", name, gs.GetBorderTopForeground(), ws.GetBorderTopForeground())
		}
		if ws.GetBold() != gs.GetBold() || ws.GetItalic() != gs.GetItalic() || ws.GetFaint() != gs.GetFaint() {
			t.Errorf("%s attributes differ from base theme", name)
		}
	}
}

func TestThemesLoad_MissingFileReturnsBasePalette(t *testing.T) {
	dir := t.TempDir()
	th, err := tui.LoadTheme(dir, "no-such-theme")
	if err != nil {
		t.Fatalf("LoadTheme(missing) returned error: %v", err)
	}
	assertSameTheme(t, tui.DefaultTheme(), th)
}

func TestThemesLoad_BuiltinBaseThemes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"default-dark", "default-light"} {
		th, err := tui.LoadTheme(dir, name)
		if err != nil {
			t.Fatalf("LoadTheme(%q) returned error: %v", name, err)
		}
		assertSameTheme(t, tui.DefaultTheme(), th)
	}
	names := tui.BuiltinThemeNames()
	if len(names) != 2 || names[0] != "default-dark" || names[1] != "default-light" {
		t.Errorf("BuiltinThemeNames() = %v, want [default-dark default-light]", names)
	}
}

func TestThemesLoad_PartialTokensFallBackIndividually(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "mine.json", `{"primary": "#112233", "error": "#FF0000"}`)

	th, err := tui.LoadTheme(dir, "mine")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}

	// Explicit tokens apply to every bound field, on both light and dark sides.
	wantPrimary := lipgloss.AdaptiveColor{Light: "#112233", Dark: "#112233"}
	if got := th.ToolNameStyle.GetForeground(); got != wantPrimary {
		t.Errorf("ToolNameStyle foreground = %v, want %v", got, wantPrimary)
	}
	if got := th.InputPromptStyle.GetForeground(); got != wantPrimary {
		t.Errorf("InputPromptStyle foreground = %v, want %v", got, wantPrimary)
	}
	wantError := lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF0000"}
	if got := th.ErrorStyle.GetForeground(); got != wantError {
		t.Errorf("ErrorStyle foreground = %v, want %v", got, wantError)
	}

	// Omitted tokens keep base-palette values.
	if got := th.WarningStyle.GetForeground(); got != baseWarning {
		t.Errorf("WarningStyle foreground = %v, want base %v", got, baseWarning)
	}
	if got := th.DiffAddStyle.GetForeground(); got != baseAdd {
		t.Errorf("DiffAddStyle foreground = %v, want base %v", got, baseAdd)
	}
	if got := th.ThinkingStyle.GetForeground(); got != baseDim {
		t.Errorf("ThinkingStyle foreground = %v, want base %v", got, baseDim)
	}
	if got := th.StatusBarStyle.GetForeground(); got != (lipgloss.NoColor{}) {
		t.Errorf("StatusBarStyle foreground = %v, want unset (base)", got)
	}
	// Attributes from the base theme survive token overrides.
	if !th.ToolNameStyle.GetBold() {
		t.Error("ToolNameStyle lost its base bold attribute")
	}
	if !th.ErrorStyle.GetBold() {
		t.Error("ErrorStyle lost its base bold attribute")
	}
}

func TestThemesLoad_InvalidColorFallsBackWithoutError(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "bad.json", `{"warning": "not-a-color", "success": "#00FF00", "diffRemove": "#12345"}`)

	th, err := tui.LoadTheme(dir, "bad")
	if err != nil {
		t.Fatalf("LoadTheme with invalid colors returned error: %v", err)
	}
	wantSuccess := lipgloss.AdaptiveColor{Light: "#00FF00", Dark: "#00FF00"}
	if got := th.SuccessStyle.GetForeground(); got != wantSuccess {
		t.Errorf("SuccessStyle foreground = %v, want %v", got, wantSuccess)
	}
	if got := th.WarningStyle.GetForeground(); got != baseWarning {
		t.Errorf("WarningStyle foreground = %v, want base %v (invalid token)", got, baseWarning)
	}
	if got := th.DiffRemoveStyle.GetForeground(); got != baseRemove {
		t.Errorf("DiffRemoveStyle foreground = %v, want base %v (5-digit hex invalid)", got, baseRemove)
	}
}

func TestThemesLoad_AdaptivePerSideFallback(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "adaptive.json", `{"diffAdd": {"dark": "#00FF00", "light": "garbage"}, "diffHunk": {"light": "#ABCDEF"}}`)

	th, err := tui.LoadTheme(dir, "adaptive")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	// Valid side applies, invalid/omitted side falls back to the base palette.
	wantAdd := lipgloss.AdaptiveColor{Light: baseAdd.Light, Dark: "#00FF00"}
	if got := th.DiffAddStyle.GetForeground(); got != wantAdd {
		t.Errorf("DiffAddStyle foreground = %v, want %v", got, wantAdd)
	}
	wantHunk := lipgloss.AdaptiveColor{Light: "#ABCDEF", Dark: baseDim.Dark}
	if got := th.DiffHunkStyle.GetForeground(); got != wantHunk {
		t.Errorf("DiffHunkStyle foreground = %v, want %v", got, wantHunk)
	}
}

func TestThemesLoad_MalformedJSONReturnsBaseAndError(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "broken.json", `{"primary": "#112233",`)

	th, err := tui.LoadTheme(dir, "broken")
	if err == nil {
		t.Fatal("LoadTheme with malformed JSON: expected error, got nil")
	}
	assertSameTheme(t, tui.DefaultTheme(), th)
}

func TestThemesLoad_EmptyObjectAndUnknownKeysAreDefault(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "empty.json", `{}`)
	writeThemeFile(t, dir, "unknown.json", `{"unknownToken": "#123456", "primarye": "#123456"}`)

	for _, name := range []string{"empty", "unknown"} {
		th, err := tui.LoadTheme(dir, name)
		if err != nil {
			t.Fatalf("LoadTheme(%q): %v", name, err)
		}
		assertSameTheme(t, tui.DefaultTheme(), th)
	}
}

func TestThemesLoad_ANSIColorNumberAccepted(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "ansi.json", `{"primary": "196", "warning": "999"}`)

	th, err := tui.LoadTheme(dir, "ansi")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	wantPrimary := lipgloss.AdaptiveColor{Light: "196", Dark: "196"}
	if got := th.ToolNameStyle.GetForeground(); got != wantPrimary {
		t.Errorf("ToolNameStyle foreground = %v, want %v", got, wantPrimary)
	}
	if got := th.WarningStyle.GetForeground(); got != baseWarning {
		t.Errorf("WarningStyle foreground = %v, want base %v (out-of-range ANSI)", got, baseWarning)
	}
}

func TestThemesLoad_RejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../evil", "..", "a/b", `a\b`, ""} {
		th, err := tui.LoadTheme(dir, name)
		if err == nil {
			t.Errorf("LoadTheme(%q): expected error, got nil", name)
		}
		assertSameTheme(t, tui.DefaultTheme(), th)
	}
}

func TestThemesList_SortedFileNamesAndBuiltins(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "zebra.json", `{}`)
	writeThemeFile(t, dir, "alpha.json", `{}`)
	writeThemeFile(t, dir, "notes.txt", `{}`)
	writeThemeFile(t, dir, ".hidden.json", `{}`)
	if err := os.Mkdir(filepath.Join(dir, "subdir.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := tui.ListThemes(dir)
	if err != nil {
		t.Fatalf("ListThemes: %v", err)
	}
	want := []string{"default-dark", "default-light", "alpha", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListThemes = %v, want %v", got, want)
	}
}

func TestThemesList_MissingDirReturnsBuiltins(t *testing.T) {
	got, err := tui.ListThemes(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListThemes(missing dir): %v", err)
	}
	want := []string{"default-dark", "default-light"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListThemes = %v, want %v", got, want)
	}
}

// TestThemesLoad_TokenMappingCoversEveryThemeField sets every token to a
// distinctive color and asserts each Theme field resolves to its bound token —
// proving the token→style mapping covers every field of the Theme struct.
func TestThemesLoad_TokenMappingCoversEveryThemeField(t *testing.T) {
	tokenColors := map[string]string{
		"primary": "#100001", "accent": "#100002", "text": "#100003",
		"textStrong": "#100004", "textDim": "#100005", "textMuted": "#100006",
		"border": "#100007", "borderFocus": "#100008", "success": "#100009",
		"warning": "#10000A", "error": "#10000B", "diffAdd": "#10000C",
		"diffRemove": "#10000D", "diffHunk": "#10000E", "roleUser": "#10000F",
		"shellMode": "#100010", "codeBackground": "#100011",
	}
	data, err := json.Marshal(tokenColors)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeThemeFile(t, dir, "full.json", string(data))

	th, err := tui.LoadTheme(dir, "full")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}

	wantForeground := map[string]string{
		"UserMsgStyle": "roleUser", "AssistantMsgStyle": "text",
		"ThinkingStyle": "textDim", "ToolNameStyle": "primary",
		"ToolResultStyle": "textDim", "ToolInputStyle": "textMuted",
		"StatusBarStyle": "accent", "StatusModelStyle": "textStrong",
		"CostStyle": "textDim", "TimingStyle": "textDim",
		"DimStyle": "textDim", "BoldStyle": "textStrong",
		"ItalicStyle": "text", "DiffAddStyle": "diffAdd",
		"DiffRemoveStyle": "diffRemove", "DiffHunkStyle": "diffHunk",
		"ErrorStyle": "error", "WarningStyle": "warning",
		"SuccessStyle": "success", "InputStyle": "text",
		"InputPromptStyle": "primary", "SeparatorStyle": "textDim",
	}
	wantBackground := map[string]string{"CodeStyle": "codeBackground"}
	wantBorder := map[string]string{"BorderStyle": "border"}

	v := reflect.ValueOf(th)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		style := v.Field(i).Interface().(lipgloss.Style)
		switch {
		case wantForeground[name] != "":
			want := lipgloss.AdaptiveColor{Light: tokenColors[wantForeground[name]], Dark: tokenColors[wantForeground[name]]}
			if got := style.GetForeground(); got != want {
				t.Errorf("%s foreground = %v, want token %s = %v", name, got, wantForeground[name], want)
			}
		case wantBackground[name] != "":
			want := lipgloss.AdaptiveColor{Light: tokenColors[wantBackground[name]], Dark: tokenColors[wantBackground[name]]}
			if got := style.GetBackground(); got != want {
				t.Errorf("%s background = %v, want token %s = %v", name, got, wantBackground[name], want)
			}
		case wantBorder[name] != "":
			want := lipgloss.AdaptiveColor{Light: tokenColors[wantBorder[name]], Dark: tokenColors[wantBorder[name]]}
			if got := style.GetBorderTopForeground(); got != want {
				t.Errorf("%s border foreground = %v, want token %s = %v", name, got, wantBorder[name], want)
			}
		default:
			t.Errorf("Theme field %s is not covered by any token binding", name)
		}
	}
}

// TestThemesLoad_BasePaletteDerivation pins the built-in base themes to the
// colors DefaultTheme() uses today, so the fallback palette can never drift
// from the compiled default.
func TestThemesLoad_BasePaletteDerivation(t *testing.T) {
	th := tui.DefaultTheme()
	checks := map[string]lipgloss.TerminalColor{
		"ToolNameStyle":   th.ToolNameStyle.GetForeground(),
		"ToolInputStyle":  th.ToolInputStyle.GetForeground(),
		"ThinkingStyle":   th.ThinkingStyle.GetForeground(),
		"SuccessStyle":    th.SuccessStyle.GetForeground(),
		"ErrorStyle":      th.ErrorStyle.GetForeground(),
		"WarningStyle":    th.WarningStyle.GetForeground(),
		"DiffAddStyle":    th.DiffAddStyle.GetForeground(),
		"DiffRemoveStyle": th.DiffRemoveStyle.GetForeground(),
	}
	want := map[string]lipgloss.TerminalColor{
		"ToolNameStyle":   baseHighlight,
		"ToolInputStyle":  baseSubtle,
		"ThinkingStyle":   baseDim,
		"SuccessStyle":    baseSpecial,
		"ErrorStyle":      baseError,
		"WarningStyle":    baseWarning,
		"DiffAddStyle":    baseAdd,
		"DiffRemoveStyle": baseRemove,
	}
	for field, got := range checks {
		if got != want[field] {
			t.Errorf("DefaultTheme().%s color = %v, want %v (test palette drifted from theme.go)", field, got, want[field])
		}
	}
}

func TestThemesDefaultDir(t *testing.T) {
	dir, err := tui.DefaultThemesDir()
	if err != nil {
		t.Fatalf("DefaultThemesDir: %v", err)
	}
	wantSuffix := filepath.Join(".config", "harnesscli", "themes")
	if len(dir) < len(wantSuffix) || dir[len(dir)-len(wantSuffix):] != wantSuffix {
		t.Errorf("DefaultThemesDir = %q, want suffix %q", dir, wantSuffix)
	}
}

// TestThemesTokenSetSchema pins the documented token list: every TokenSet
// field must carry one of the schema's json tags, so renaming or dropping a
// token cannot silently break existing theme files.
func TestThemesTokenSetSchema(t *testing.T) {
	want := []string{
		"primary", "accent", "text", "textStrong", "textDim", "textMuted",
		"border", "borderFocus", "success", "warning", "error",
		"diffAdd", "diffRemove", "diffHunk", "roleUser", "shellMode",
		"codeBackground",
	}
	typ := reflect.TypeOf(tui.TokenSet{})
	if typ.NumField() != len(want) {
		t.Fatalf("TokenSet has %d fields, schema documents %d tokens", typ.NumField(), len(want))
	}
	for i, tag := range want {
		if got := typ.Field(i).Tag.Get("json"); got != tag {
			t.Errorf("TokenSet field %d json tag = %q, want %q", i, got, tag)
		}
	}
}
