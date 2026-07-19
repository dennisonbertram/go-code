package plugins

import "path/filepath"

// EnabledBundles resolves validated bundles for enabled installed plugins.
func EnabledBundles(root string, store *StateStore) ([]*Bundle, error) {
	items, err := store.List()
	if err != nil {
		return nil, err
	}
	var bundles []*Bundle
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		bundle, err := LoadBundle(filepath.Join(root, item.Name, item.Version))
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}
