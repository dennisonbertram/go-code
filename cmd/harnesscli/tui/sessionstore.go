package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go-agent-harness/cmd/harnesscli/tui/components/sessionpicker"
)

const (
	// maxStoredSessions is the maximum number of sessions kept in the store.
	// When exceeded, the oldest session (by StartedAt) is evicted.
	maxStoredSessions = 100

	// sessionsFileName is the name of the JSON file in the config directory.
	sessionsFileName = "sessions.json"
)

// StoredSessionEntry holds metadata for a single past session.
// It is persisted to sessions.json.
type StoredSessionEntry struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Model     string    `json:"model,omitempty"`
	TurnCount int       `json:"turn_count"`
	LastMsg   string    `json:"last_msg,omitempty"`
	// Title is an optional user-assigned label for the session (see /title).
	Title string `json:"title,omitempty"`
}

// SessionStore manages a list of StoredSessionEntry values persisted to a
// JSON file in a config directory.  All mutating methods operate in-memory;
// call Save() to flush to disk.
type SessionStore struct {
	dir     string
	entries []StoredSessionEntry
}

// NewSessionStore creates a SessionStore that persists to <dir>/sessions.json.
// Call Load() to populate from disk before use.
func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{dir: dir}
}

// filePath returns the full path to the sessions JSON file.
func (s *SessionStore) filePath() string {
	return filepath.Join(s.dir, sessionsFileName)
}

// Load reads sessions.json from the config directory.
// If the file does not exist, the store is initialized empty and no error is
// returned.
func (s *SessionStore) Load() error {
	data, err := os.ReadFile(s.filePath())
	if os.IsNotExist(err) {
		s.entries = nil
		return nil
	}
	if err != nil {
		return err
	}

	var entries []StoredSessionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// Corrupt file — start fresh rather than blocking the user.
		s.entries = nil
		return nil
	}
	s.entries = entries
	return nil
}

// Save writes the current in-memory entries to sessions.json, creating the
// directory if necessary.  It uses a write-to-temp-then-rename pattern to
// prevent data corruption if the process is interrupted during the write.
func (s *SessionStore) Save() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	tmpPath := s.filePath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath())
}

// Add appends a new entry to the store.  If the store already holds
// maxStoredSessions entries, the oldest entry (by StartedAt) is evicted first.
// If an entry with the same ID already exists it is replaced in-place.
func (s *SessionStore) Add(entry StoredSessionEntry) {
	// Replace existing entry with the same ID.
	for i, e := range s.entries {
		if e.ID == entry.ID {
			s.entries[i] = entry
			return
		}
	}

	// Evict oldest when at capacity.
	if len(s.entries) >= maxStoredSessions {
		oldest := 0
		for i := 1; i < len(s.entries); i++ {
			if s.entries[i].StartedAt.Before(s.entries[oldest].StartedAt) {
				oldest = i
			}
		}
		s.entries = append(s.entries[:oldest], s.entries[oldest+1:]...)
	}

	s.entries = append(s.entries, entry)
}

// Delete removes the entry with the given ID.  Silently ignores unknown IDs.
func (s *SessionStore) Delete(id string) {
	for i, e := range s.entries {
		if e.ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

// Get returns the entry with the given ID and true, or a zero value and false
// if not found.
func (s *SessionStore) Get(id string) (StoredSessionEntry, bool) {
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return StoredSessionEntry{}, false
}

// Update calls fn with a pointer to the entry matching id, allowing the caller
// to mutate it in place.  If the ID is not found, fn is not called.
func (s *SessionStore) Update(id string, fn func(*StoredSessionEntry)) {
	for i := range s.entries {
		if s.entries[i].ID == id {
			fn(&s.entries[i])
			return
		}
	}
}

// SetTitle sets the title of the entry with the given ID. An empty title
// clears it. It returns false when no entry with that ID exists.
// Call Save() to persist the change.
func (s *SessionStore) SetTitle(id, title string) bool {
	for i := range s.entries {
		if s.entries[i].ID == id {
			s.entries[i].Title = title
			return true
		}
	}
	return false
}

// List returns a copy of all entries sorted by StartedAt descending (most
// recent first). It is safe to call on a nil *SessionStore.
func (s *SessionStore) List() []StoredSessionEntry {
	if s == nil {
		return nil
	}
	cp := make([]StoredSessionEntry, len(s.entries))
	copy(cp, s.entries)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].StartedAt.After(cp[j].StartedAt)
	})
	return cp
}

// SessionStoreFileInfo returns os.FileInfo for the sessions.json file in dir.
// This is exported for use in tests that need to inspect file permissions.
func SessionStoreFileInfo(dir string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(dir, sessionsFileName))
}

// sessionEntriesToPicker converts StoredSessionEntry values to the
// sessionpicker.SessionEntry type expected by the picker component.
func sessionEntriesToPicker(entries []StoredSessionEntry) []sessionpicker.SessionEntry {
	out := make([]sessionpicker.SessionEntry, len(entries))
	for i, e := range entries {
		out[i] = sessionpicker.SessionEntry{
			ID:        e.ID,
			StartedAt: e.StartedAt,
			Model:     e.Model,
			TurnCount: e.TurnCount,
			LastMsg:   e.LastMsg,
			Title:     e.Title,
		}
	}
	return out
}
