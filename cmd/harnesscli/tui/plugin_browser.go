package tui

import (
	"fmt"
	"go-agent-harness/internal/plugins"
	"os"
	"path/filepath"
	"strings"
)

type pluginBrowserItem struct {
	Name, Version    string
	Enabled, Trusted bool
}
type pluginBrowserState struct {
	items    []pluginBrowserItem
	selected int
}

func loadPluginBrowser() pluginBrowserState {
	h, e := os.UserHomeDir()
	if e != nil {
		return pluginBrowserState{}
	}
	r := filepath.Join(h, ".go-harness", "plugins")
	ps, e := plugins.NewStateStore(filepath.Join(r, "state.json")).List()
	if e != nil {
		return pluginBrowserState{}
	}
	v := pluginBrowserState{}
	for _, p := range ps {
		v.items = append(v.items, pluginBrowserItem{p.Name, p.Version, p.Enabled, p.Trusted})
	}
	return v
}
func (p pluginBrowserState) View(w int) string {
	l := []string{"Plugin browser", "↑/↓ select • enter enable/disable • esc close", ""}
	if len(p.items) == 0 {
		l = append(l, "No installed plugin bundles. Use harnesscli plugin install <source>.")
	}
	for i, x := range p.items {
		m := " "
		if i == p.selected {
			m = ">"
		}
		l = append(l, fmt.Sprintf("%s %s@%s enabled=%t trusted=%t", m, x.Name, x.Version, x.Enabled, x.Trusted))
	}
	return boxOverlay(strings.Join(l, "\n"), w)
}
