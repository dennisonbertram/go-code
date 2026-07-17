package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TrustStore records which project-level hook files the user has explicitly
// trusted, keyed by absolute file path with the SHA-256 of the file content
// at trust time. Editing a trusted file changes its hash, which
// automatically un-trusts it — trust never silently extends to new content.
//
// The store persists as one JSON file under the user-global directory
// (TrustStorePath(home)) — never inside a project tree, so a cloned
// repository cannot trust itself. Writes are atomic (write temp file +
// rename) so a crash cannot leave a half-written store.
//
// Security scope: trusting a hook means "run this exact file content with my
// full user privileges". There is no sandboxing and no command allowlist —
// see docs/design/plugins.md for the explicit non-goals.
type TrustStore struct {
	path    string
	mu      sync.Mutex
	records map[string]trustRecord
}

// trustRecord is one trusted (path, content-hash) pair.
type trustRecord struct {
	SHA256    string    `json:"sha256"`
	TrustedAt time.Time `json:"trusted_at"`
}

// trustStoreFile is the on-disk JSON shape.
type trustStoreFile struct {
	Version int                    `json:"version"`
	Trusted map[string]trustRecord `json:"trusted"`
}

// LoadTrustStore reads the store at path. A missing or corrupt file yields
// an empty store (never fatal) — a lost trust store fails closed: every
// project hook simply requires re-trusting.
func LoadTrustStore(path string) (*TrustStore, error) {
	s := &TrustStore{path: path, records: map[string]trustRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read trust store %s: %w", path, err)
	}
	var f trustStoreFile
	if err := json.Unmarshal(data, &f); err != nil {
		// Corrupt store: treat as empty rather than failing startup.
		return s, nil
	}
	for k, v := range f.Trusted {
		s.records[k] = v
	}
	return s, nil
}

// CheckFile reports whether the hook file at path may load. It returns ""
// when the file is trusted (a record exists AND the current content hash
// matches), SkipReasonUntrusted when no record exists, or
// SkipReasonModifiedSinceTrusted when the content changed since trust.
func (s *TrustStore) CheckFile(path string) string {
	abs := canonicalPath(path)
	s.mu.Lock()
	rec, ok := s.records[abs]
	s.mu.Unlock()
	if !ok {
		return SkipReasonUntrusted
	}
	sum, err := hashFile(path)
	if err != nil {
		return SkipReasonUntrusted // unreadable file fails closed
	}
	if sum != rec.SHA256 {
		return SkipReasonModifiedSinceTrusted
	}
	return ""
}

// Trust records the current content hash of the file at path and persists
// the store atomically. The file must exist and be readable.
func (s *TrustStore) Trust(path string) error {
	sum, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("trust %s: %w", path, err)
	}
	s.mu.Lock()
	s.records[canonicalPath(path)] = trustRecord{SHA256: sum, TrustedAt: time.Now().UTC()}
	err = s.saveLocked()
	s.mu.Unlock()
	return err
}

// Revoke removes any trust record for path and persists the store. Revoking
// an untrusted path is a no-op.
func (s *TrustStore) Revoke(path string) error {
	s.mu.Lock()
	delete(s.records, canonicalPath(path))
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

// List returns the trusted paths, sorted, with their records. Used by the
// harnesscli hooks list command.
func (s *TrustStore) List() []TrustedFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TrustedFile, 0, len(s.records))
	for p, rec := range s.records {
		out = append(out, TrustedFile{Path: p, SHA256: rec.SHA256, TrustedAt: rec.TrustedAt})
	}
	sortTrusted(out)
	return out
}

// TrustedFile is one entry of TrustStore.List.
type TrustedFile struct {
	Path      string
	SHA256    string
	TrustedAt time.Time
}

// saveLocked persists the store via write-temp-then-rename. Caller holds mu.
func (s *TrustStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create trust store dir: %w", err)
	}
	f := trustStoreFile{Version: 1, Trusted: s.records}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trust store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".hooks-trust-*.tmp")
	if err != nil {
		return fmt.Errorf("create trust store temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write trust store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close trust store: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod trust store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename trust store into place: %w", err)
	}
	return nil
}

// hashFile returns the hex SHA-256 of a file's content.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalPath normalizes a hook file path for store keys: absolute +
// symlink-resolved when possible, so the same file cannot be trusted under
// one spelling and bypassed under another.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// sortTrusted orders TrustedFile entries by path for deterministic output.
func sortTrusted(files []TrustedFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
