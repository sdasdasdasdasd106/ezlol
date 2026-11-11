// Package settings persists user configuration in the data directory.
package settings

import (
	"encoding/json"
	"os"
	"sync"
)

// Settings is everything the user can change from the UI.
type Settings struct {
	OCR         bool `json:"ocr"`         // augment pick detection
	UpdateCheck bool `json:"updateCheck"` // look for new releases
	TestLog     bool `json:"testLog"`     // write a local log of every augment scan and pick
	LogShots    bool `json:"logShots"`    // also save screenshots of what was scanned
	// Testing aid (remove with internal/logupload): upload logs to GitHub.
	GitHubToken string `json:"githubToken"` // never logged or echoed back
	AutoUpload  bool   `json:"autoUpload"`  // upload after every game
}

// Defaults returns the initial configuration.
func Defaults() Settings {
	return Settings{OCR: true, UpdateCheck: true, TestLog: true}
}

// Store is a mutex-guarded settings file.
type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// Load reads path (missing file = defaults).
func Load(path string) *Store {
	st := &Store{path: path, s: Defaults()}
	if b, err := os.ReadFile(path); err == nil {
		// Unmarshal over defaults so new fields keep their default values.
		_ = json.Unmarshal(b, &st.s)
	}
	return st
}

// Get returns a copy.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Update applies fn under the lock and saves.
func (st *Store) Update(fn func(s *Settings)) error {
	st.mu.Lock()
	fn(&st.s)
	s := st.s
	st.mu.Unlock()
	if st.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file may contain the Riot API key.
	return os.WriteFile(st.path, b, 0o600)
}

// Masked returns settings for the UI (nothing secret is stored any more).
func (s Settings) Masked() map[string]any {
	hint := ""
	if n := len(s.GitHubToken); n > 4 {
		hint = "••••" + s.GitHubToken[n-4:]
	}
	return map[string]any{"ocr": s.OCR, "updateCheck": s.UpdateCheck, "testLog": s.TestLog, "logShots": s.LogShots,
		"githubTokenSet": s.GitHubToken != "", "githubTokenHint": hint, "autoUpload": s.AutoUpload}
}
