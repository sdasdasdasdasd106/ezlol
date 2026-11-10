// Package testlog writes a local, line-per-event JSON log of what the augment
// assistant saw and decided, so a test session can be reviewed afterwards.
// Files: <data>/logs/ezlol-YYYY-MM-DD.jsonl, plus optional screenshots in
// <data>/logs/shots (the newest 60 are kept).
package testlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Logger appends events while enabled() is true.
type Logger struct {
	dir      string
	enabled  func() bool
	shots    func() bool
	mu       sync.Mutex
	lastShot time.Time
}

// New logs under dir. enabled and shots are read on every call, so settings
// changes apply immediately.
func New(dir string, enabled, shots func() bool) *Logger {
	return &Logger{dir: dir, enabled: enabled, shots: shots}
}

// Dir is where the logs go.
func (l *Logger) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Log writes one event. Fields are merged into the line next to "t" and "event".
func (l *Logger) Log(event string, fields map[string]any) {
	if l == nil || l.enabled == nil || !l.enabled() {
		return
	}
	line := map[string]any{"t": time.Now().Format("2006-01-02 15:04:05.000"), "event": event}
	for k, v := range fields {
		line[k] = v
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(l.dir, "ezlol-"+time.Now().Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// ShotPath returns where to save a screenshot for this scan, or "" when
// screenshots are off or one was saved less than 30 seconds ago (force skips
// that wait, for scans that found an offer).
func (l *Logger) ShotPath(kind string, force bool) string {
	if l == nil || l.enabled == nil || !l.enabled() || l.shots == nil || !l.shots() {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !force && time.Since(l.lastShot) < 30*time.Second {
		return ""
	}
	dir := filepath.Join(l.dir, "shots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	l.lastShot = time.Now()
	prune(dir, 60)
	return filepath.Join(dir, time.Now().Format("20060102-150405.000")+"-"+kind+".png")
}

func prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) < keep {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names) // timestamp prefix: oldest first
	for _, n := range names[:len(names)-keep+1] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}
