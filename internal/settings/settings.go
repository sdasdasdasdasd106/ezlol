// Package settings persists user configuration in the data directory.
package settings

import (
	"encoding/json"
	"os"
	"sync"
)

// Settings is everything the user can change from the UI.
type Settings struct {
	RiotAPIKey    string `json:"riotApiKey"` // never logged; masked in API responses
	Platform      string `json:"platform"`
	MatchesPerRun int    `json:"matchesPerRun"`
	AutoCompile   bool   `json:"autoCompile"`
	OCR           bool   `json:"ocr"`         // augment pick detection
	UpdateCheck   bool   `json:"updateCheck"` // look for new releases
	TestLog       bool   `json:"testLog"`     // write a local log of every augment scan and pick
	LogShots      bool   `json:"logShots"`    // also save screenshots of what was scanned
}

// Defaults returns the initial configuration.
func Defaults() Settings {
	return Settings{Platform: "sg2", MatchesPerRun: 200, OCR: true, UpdateCheck: true, TestLog: true}
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

// Masked returns settings safe to send to the UI.
func (s Settings) Masked() map[string]any {
	key := ""
	if n := len(s.RiotAPIKey); n > 0 {
		if n > 4 {
			key = "••••" + s.RiotAPIKey[n-4:]
		} else {
			key = "••••"
		}
	}
	return map[string]any{
		"riotApiKeySet": s.RiotAPIKey != "", "riotApiKeyHint": key,
		"platform": s.Platform, "matchesPerRun": s.MatchesPerRun, "autoCompile": s.AutoCompile,
		"ocr": s.OCR, "updateCheck": s.UpdateCheck, "testLog": s.TestLog, "logShots": s.LogShots,
	}
}
