package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarketplaceStoreAndLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	s := NewMarketplaceStore(p)
	if e := s.Add(MarketplaceSource{Name: "local", URL: p}); e != nil {
		t.Fatal(e)
	}
	if lenMust, e := s.List(); e != nil || len(lenMust) != 1 {
		t.Fatalf("%v %v", lenMust, e)
	}
	if e := os.WriteFile(p, []byte(`{"plugins":[{"name":"demo","source":"owner/demo"}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := LoadMarketplace(MarketplaceSource{URL: p})
	if e != nil || len(m.Plugins) != 1 {
		t.Fatalf("%#v %v", m, e)
	}
}
