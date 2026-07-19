package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type MarketplaceSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Marketplace struct {
	Plugins []MarketplacePlugin `json:"plugins"`
}
type MarketplacePlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}
type MarketplaceStore struct{ path string }

func NewMarketplaceStore(path string) *MarketplaceStore { return &MarketplaceStore{path: path} }
func (s *MarketplaceStore) List() ([]MarketplaceSource, error) {
	var v []MarketplaceSource
	b, e := os.ReadFile(s.path)
	if os.IsNotExist(e) {
		return v, nil
	}
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (s *MarketplaceStore) Add(src MarketplaceSource) error {
	v, e := s.List()
	if e != nil {
		return e
	}
	for _, x := range v {
		if x.Name == src.Name {
			return fmt.Errorf("marketplace %q already exists", src.Name)
		}
	}
	v = append(v, src)
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	return os.WriteFile(s.path, b, 0600)
}
func LoadMarketplace(source MarketplaceSource) (*Marketplace, error) {
	b, e := os.ReadFile(source.URL)
	if e != nil {
		return nil, e
	}
	var m Marketplace
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	return &m, nil
}
