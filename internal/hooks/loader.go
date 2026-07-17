package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadOptions controls trust-aware loading.
type LoadOptions struct {
	// UserDir is the user-global hooks directory (typically
	// UserHooksDir(home)). Defs loaded from it are classified SourceUser and
	// skip trust checks. Defs from every other directory classify as
	// SourceProject. When empty, every def classifies as SourceProject.
	UserDir string

	// TrustStore gates project-level defs when non-nil: a project def with no
	// trust record, or whose content hash no longer matches the record, is
	// skipped with reason "untrusted" / "modified_since_trusted". A nil
	// TrustStore disables trust checks entirely (parsing-only mode) —
	// production startup must always pass one.
	TrustStore *TrustStore
}

// Load reads and validates every *.json hook file in the given directories.
// It performs NO trust checks — production startup must use LoadWithOptions
// with a TrustStore so project-level files cannot execute without explicit
// trust.
//
// One invalid file never aborts the load: it produces a SkipRecord naming
// the file and the reason, and remaining files still load. Directories that
// do not exist are silently skipped (zero defs, no error).
func Load(dirs ...string) ([]HookDef, []SkipRecord) {
	return LoadWithOptions(LoadOptions{}, dirs...)
}

// LoadWithOptions is Load with source classification and trust enforcement.
// Directory order is preserved; within one directory files load in sorted
// filename order so hook registration order is deterministic.
func LoadWithOptions(opts LoadOptions, dirs ...string) ([]HookDef, []SkipRecord) {
	var defs []HookDef
	var skips []SkipRecord

	userDir := ""
	if opts.UserDir != "" {
		if abs, err := filepath.Abs(opts.UserDir); err == nil {
			userDir = abs
		} else {
			userDir = filepath.Clean(opts.UserDir)
		}
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // absent discovery dir is normal — not an error
			}
			skips = append(skips, SkipRecord{File: dir, Reason: fmt.Sprintf("read directory: %v", err)})
			continue
		}

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)

		source := SourceProject
		if userDir != "" {
			absDir, err := filepath.Abs(dir)
			if err != nil {
				absDir = filepath.Clean(dir)
			}
			if absDir == userDir {
				source = SourceUser
			}
		}

		for _, name := range names {
			path := filepath.Join(dir, name)
			def, skip := loadHookFile(path)
			if skip != nil {
				skips = append(skips, *skip)
				continue
			}
			def.Source = source
			def.SourceDir = dir
			def.FilePath = path

			if source == SourceProject && opts.TrustStore != nil {
				if reason := opts.TrustStore.CheckFile(path); reason != "" {
					skips = append(skips, SkipRecord{File: path, Reason: reason})
					continue
				}
			}
			defs = append(defs, def)
		}
	}
	return defs, skips
}

// loadHookFile reads, decodes, and validates one hook file. It returns a
// SkipRecord (never an error) when the file is invalid.
func loadHookFile(path string) (HookDef, *SkipRecord) {
	skip := func(reason string) (HookDef, *SkipRecord) {
		return HookDef{}, &SkipRecord{File: path, Reason: reason}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return skip(fmt.Sprintf("read file: %v", err))
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // reject typos in a security-sensitive file
	var def HookDef
	if err := dec.Decode(&def); err != nil {
		return skip(fmt.Sprintf("invalid JSON: %v", err))
	}

	if err := def.validate(filepath.Base(path)); err != nil {
		return skip(err.Error())
	}
	return def, nil
}
