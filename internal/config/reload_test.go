package config_test

import (
	"reflect"
	"strings"
	"testing"

	"go-agent-harness/internal/config"
)

// TestReloadDiffModelOnlyChange verifies that changing only the model is
// reported as a hot-swappable change that does not require a restart.
func TestReloadDiffModelOnlyChange(t *testing.T) {
	oldCfg := config.Defaults()
	newCfg := oldCfg
	newCfg.Model = "gpt-4o"

	report := config.ReloadDiff(oldCfg, newCfg)

	if !report.Changed() {
		t.Fatal("ReloadDiff reported no changes for a model change")
	}
	if report.NeedsRestart() {
		t.Fatalf("model change must not require restart, got RestartRequired=%v", report.RestartRequired)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "model" {
		t.Errorf("Applied: got %v, want [model]", report.Applied)
	}
}

// TestReloadDiffAddrChangeRequiresRestart verifies that changing the listen
// address is reported as restart-only and never as applied.
func TestReloadDiffAddrChangeRequiresRestart(t *testing.T) {
	oldCfg := config.Defaults()
	newCfg := oldCfg
	newCfg.Addr = ":9090"

	report := config.ReloadDiff(oldCfg, newCfg)

	if !report.NeedsRestart() {
		t.Fatal("addr change must be reported as requiring restart")
	}
	if len(report.RestartRequired) != 1 || report.RestartRequired[0] != "addr" {
		t.Errorf("RestartRequired: got %v, want [addr]", report.RestartRequired)
	}
	if len(report.Applied) != 0 {
		t.Errorf("Applied: got %v, want empty for a restart-only change", report.Applied)
	}
}

// TestReloadDiffMemoryFieldSplit verifies the memory section split: the
// persistence backend selector (db_driver) is restart-only because store
// handles are opened once at startup, while the runtime enable toggle is
// hot-swappable.
func TestReloadDiffMemoryFieldSplit(t *testing.T) {
	base := config.Defaults()

	driverChange := base
	driverChange.Memory.DBDriver = "postgres"
	report := config.ReloadDiff(base, driverChange)
	if !report.NeedsRestart() {
		t.Fatal("memory.db_driver change must require restart")
	}
	if len(report.RestartRequired) != 1 || report.RestartRequired[0] != "memory.db_driver" {
		t.Errorf("RestartRequired: got %v, want [memory.db_driver]", report.RestartRequired)
	}

	enabledChange := base
	enabledChange.Memory.Enabled = !base.Memory.Enabled
	report = config.ReloadDiff(base, enabledChange)
	if report.NeedsRestart() {
		t.Fatalf("memory.enabled change must not require restart, got RestartRequired=%v", report.RestartRequired)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "memory.enabled" {
		t.Errorf("Applied: got %v, want [memory.enabled]", report.Applied)
	}
}

// TestReloadDiffIdenticalConfigs verifies that diffing a config against
// itself produces an empty report.
func TestReloadDiffIdenticalConfigs(t *testing.T) {
	cfg := config.Defaults()
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"tool": {Transport: "stdio", Command: "/bin/tool", Args: []string{"--verbose"}},
	}

	report := config.ReloadDiff(cfg, cfg)

	if report.Changed() {
		t.Errorf("identical configs must produce empty report, got Applied=%v RestartRequired=%v",
			report.Applied, report.RestartRequired)
	}
	if report.NeedsRestart() {
		t.Error("identical configs must not require restart")
	}
}

// TestReloadDiffMCPServersChangeRequiresRestart verifies that adding,
// removing, or altering an MCP server entry is restart-only, because MCP
// server processes are wired once at startup.
func TestReloadDiffMCPServersChangeRequiresRestart(t *testing.T) {
	oldCfg := config.Defaults()
	newCfg := oldCfg
	newCfg.MCPServers = map[string]config.MCPServerConfig{
		"remote": {Transport: "http", URL: "http://localhost:3001/mcp"},
	}

	report := config.ReloadDiff(oldCfg, newCfg)

	if !report.NeedsRestart() {
		t.Fatal("mcp_servers change must require restart")
	}
	if len(report.RestartRequired) != 1 || report.RestartRequired[0] != "mcp_servers" {
		t.Errorf("RestartRequired: got %v, want [mcp_servers]", report.RestartRequired)
	}
}

// TestReloadDiffSliceFieldChange verifies that changes to slice-valued
// fields (hook discovery dirs) are detected and classified.
func TestReloadDiffSliceFieldChange(t *testing.T) {
	oldCfg := config.Defaults()
	newCfg := oldCfg
	newCfg.Hooks.Dirs = []string{"/srv/team-hooks"}

	report := config.ReloadDiff(oldCfg, newCfg)

	if report.NeedsRestart() {
		t.Fatalf("hooks.dirs change must not require restart, got RestartRequired=%v", report.RestartRequired)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "hooks.dirs" {
		t.Errorf("Applied: got %v, want [hooks.dirs]", report.Applied)
	}
}

// TestReloadDiffMixedChangesDeterministic verifies that a diff containing
// both hot-swappable and restart-only changes populates both lists, and that
// repeated diffs return entries in a stable order.
func TestReloadDiffMixedChangesDeterministic(t *testing.T) {
	oldCfg := config.Defaults()
	newCfg := oldCfg
	newCfg.Model = "gpt-4o"
	newCfg.MaxSteps = 25
	newCfg.Addr = ":9090"
	newCfg.Memory.SQLitePath = "/tmp/other.db"
	newCfg.AutoCompact.Threshold = 0.9

	first := config.ReloadDiff(oldCfg, newCfg)
	second := config.ReloadDiff(oldCfg, newCfg)

	if !first.Changed() || !first.NeedsRestart() {
		t.Fatalf("mixed diff must be changed and require restart, got %+v", first)
	}
	if !reflect.DeepEqual(first.Applied, second.Applied) ||
		!reflect.DeepEqual(first.RestartRequired, second.RestartRequired) {
		t.Fatalf("ReloadDiff must be deterministic: first=%+v second=%+v", first, second)
	}
	for _, want := range []string{"model", "max_steps", "auto_compact.threshold"} {
		if !containsString(first.Applied, want) {
			t.Errorf("Applied missing %q: got %v", want, first.Applied)
		}
	}
	for _, want := range []string{"addr", "memory.sqlite_path"} {
		if !containsString(first.RestartRequired, want) {
			t.Errorf("RestartRequired missing %q: got %v", want, first.RestartRequired)
		}
	}
}

// TestReloadClassificationCoversEveryConfigField is the exhaustiveness
// guard: every leaf field of Config (walked by reflection over the TOML
// tags) must appear in the classification table, so a future field cannot
// be added without an explicit reload-class decision.
func TestReloadClassificationCoversEveryConfigField(t *testing.T) {
	classified := make(map[string]bool)
	for _, fc := range config.ReloadClassification() {
		if fc.Path == "" {
			t.Error("classification entry with empty path")
			continue
		}
		if classified[fc.Path] {
			t.Errorf("duplicate classification entry %q", fc.Path)
		}
		classified[fc.Path] = true
	}

	leaves := make(map[string]bool)
	walkConfigLeaves(reflect.TypeOf(config.Config{}), "", leaves)

	for path := range leaves {
		if !classified[path] {
			t.Errorf("config field %q is not classified in ReloadClassification()", path)
		}
	}
	for path := range classified {
		if !leaves[path] {
			t.Errorf("classification entry %q does not correspond to a Config field", path)
		}
	}
}

// walkConfigLeaves records dotted TOML paths for every leaf field of typ.
// Structs are walked recursively; maps, slices, and scalars are leaves.
func walkConfigLeaves(typ reflect.Type, prefix string, out map[string]bool) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if field.Type.Kind() == reflect.Struct {
			walkConfigLeaves(field.Type, path, out)
			continue
		}
		out[path] = true
	}
}

// TestReloadClassString verifies the human-readable class names used in
// reload reporting, including the defensive unknown-class branch.
func TestReloadClassString(t *testing.T) {
	cases := []struct {
		class config.ReloadClass
		want  string
	}{
		{config.ReloadHotSwappable, "hot-swappable"},
		{config.ReloadRestartOnly, "restart-only"},
		{config.ReloadClass(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.class.String(); got != tc.want {
			t.Errorf("ReloadClass(%d).String(): got %q, want %q", int(tc.class), got, tc.want)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
