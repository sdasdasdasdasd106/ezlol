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
	// AI second opinion (DeepSeek); nothing runs without a key.
	DeepSeekKey string `json:"deepseekKey"` // never logged or echoed back
	AIReview    bool   `json:"aiReview"`    // A: second opinion on each augment choice
	AIResearch  bool   `json:"aiResearch"`  // B: web research before a game
	AIText      bool   `json:"aiText"`      // C: let the AI work out the cards from the OCR text when matching fails
}

// Defaults returns the initial configuration.
func Defaults() Settings {
	return Settings{OCR: true, UpdateCheck: true, TestLog: true, AIReview: true, AIResearch: true, AIText: true}
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
	return map[string]any{"ocr": s.OCR, "updateCheck": s.UpdateCheck, "testLog": s.TestLog, "logShots": s.LogShots,
		"deepseekKeySet": s.DeepSeekKey != "", "deepseekKeyHint": tail(s.DeepSeekKey), "aiReview": s.AIReview, "aiResearch": s.AIResearch, "aiText": s.AIText}
}

func tail(secret string) string {
	if n := len(secret); n > 4 {
		return "••••" + secret[n-4:]
	}
	return ""
}
