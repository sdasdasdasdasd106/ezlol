// Package watcher polls the League client: connection, game phase, queue and the picked champion.
package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/lcu"
	"github.com/aarlint/ezlol/internal/settings"
)

// Status is a snapshot of what the watcher knows.
type Status struct {
	Connected      bool   `json:"connected"`
	Phase          string `json:"phase"`
	Summoner       string `json:"summoner"`
	GameVersion    string `json:"gameVersion"`
	Patch          string `json:"patch"`
	QueueID        int    `json:"queueId"`
	QueueName      string `json:"queueName"`
	MapID          int    `json:"mapId"`
	GameMode       string `json:"gameMode"`
	PickedChampion int    `json:"pickedChampion"`
	PickedPosition string `json:"pickedPosition"`
	// ChampSelect is the bench and rerolls during champion select, nil otherwise.
	ChampSelect *ChampSelectState `json:"champSelect,omitempty"`
	Error       string            `json:"error,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// ChampSelectState is what champion select offers besides your own champion.
type ChampSelectState struct {
	BenchEnabled bool  `json:"benchEnabled"`
	Bench        []int `json:"bench"` // champion ids, client order
	// Rerolls left; -1 when the client does not report it.
	Rerolls int `json:"rerolls"`
}

func (a *ChampSelectState) equal(b *ChampSelectState) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.BenchEnabled != b.BenchEnabled || a.Rerolls != b.Rerolls || len(a.Bench) != len(b.Bench) {
		return false
	}
	for i := range a.Bench {
		if a.Bench[i] != b.Bench[i] {
			return false
		}
	}
	return true
}

// LogEntry is a user-visible event line.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Event is what SSE subscribers receive.
type Event struct {
	Type string          `json:"type"` // status | log | hotkey
	Data json.RawMessage `json:"data"`
}

// Watcher owns the LCU connection and the poll loop.
type Watcher struct {
	mu       sync.RWMutex
	client   *lcu.Client
	status   Status
	logs     []LogEntry
	subs     map[chan Event]struct{}
	interval time.Duration
	log      *slog.Logger
	settings *settings.Store
	fails    int
}

// New creates a watcher. It never touches the queue: no auto-accept.
func New(log *slog.Logger, interval time.Duration, st *settings.Store) *Watcher {
	w := &Watcher{
		subs:     map[chan Event]struct{}{},
		interval: interval,
		log:      log,
		settings: st,
	}
	return w
}

// Client returns the current LCU client, or nil when disconnected.
func (w *Watcher) Client() *lcu.Client {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.client
}

// Status returns a copy of the current snapshot.
func (w *Watcher) Status() Status {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.status
}

// Logs returns recent log entries, oldest first.
func (w *Watcher) Logs() []LogEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]LogEntry, len(w.logs))
	copy(out, w.logs)
	return out
}

// Subscribe returns a channel of events plus an unsubscribe func.
func (w *Watcher) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subs, ch)
		w.mu.Unlock()
	}
}

// Publish sends an event to every SSE subscriber (also used for hotkey results).
func (w *Watcher) Publish(ev Event) { w.publish(ev) }

func (w *Watcher) publish(ev Event) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for ch := range w.subs {
		select {
		case ch <- ev:
		default: // slow subscriber; drop
		}
	}
}

func (w *Watcher) publishStatus() {
	b, _ := json.Marshal(w.Status())
	w.publish(Event{Type: "status", Data: b})
}

func (w *Watcher) addLog(level, msg string) {
	e := LogEntry{Time: time.Now(), Level: level, Message: msg}
	w.mu.Lock()
	w.logs = append(w.logs, e)
	if len(w.logs) > 200 {
		w.logs = w.logs[len(w.logs)-200:]
	}
	w.mu.Unlock()
	w.log.Info("watcher", "level", level, "msg", msg)
	b, _ := json.Marshal(e)
	w.publish(Event{Type: "log", Data: b})
}

// Run blocks, polling until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watcher) tick(ctx context.Context) {
	prev := w.Status()
	next := prev
	next.UpdatedAt = time.Now()
	next.Error = ""

	c := w.Client()
	if c == nil {
		creds, err := lcu.Discover()
		if err != nil {
			next.Connected = false
			next.Phase = ""
			w.setStatus(next, prev)
			return
		}
		c = lcu.New(creds)
		if s, err := c.CurrentSummoner(ctx); err == nil {
			next.Summoner = s.GameName + "#" + s.TagLine
		} else {
			// client process is up but not ready to serve yet
			next.Connected = false
			next.Error = "client starting: " + err.Error()
			w.setStatus(next, prev)
			return
		}
		if v, err := c.GameVersion(ctx); err == nil {
			next.GameVersion = v
			next.Patch = shortPatch(v)
		}
		w.mu.Lock()
		w.client = c
		w.mu.Unlock()
		next.Connected = true
		w.addLog("info", "connected to League client as "+next.Summoner)
	}

	phase, err := c.GameflowPhase(ctx)
	if err != nil {
		// The client is briefly unresponsive while launching a game; only drop it
		// after several consecutive failures.
		w.fails++
		if w.fails < 4 {
			next.Error = "client busy: " + err.Error()
			w.setStatus(next, prev)
			return
		}
		w.fails = 0
		w.mu.Lock()
		w.client = nil
		w.mu.Unlock()
		next.Connected = false
		next.Phase = ""
		next.PickedChampion = 0
		next.ChampSelect = nil
		w.addLog("warn", "lost connection to League client")
		w.setStatus(next, prev)
		return
	}
	w.fails = 0
	next.Connected = true
	next.Phase = phase
	if phase != prev.Phase {
		w.addLog("info", "phase: "+phase)
		if sess, err := c.Session(ctx); err == nil && sess != nil {
			next.QueueID = sess.GameData.Queue.ID
			next.QueueName = sess.GameData.Queue.Description
			if next.QueueName == "" {
				next.QueueName = sess.GameData.Queue.Name
			}
			next.MapID = sess.Map.ID
			next.GameMode = sess.Map.GameMode
		} else if phase == "None" || phase == "" {
			next.QueueID, next.QueueName, next.MapID, next.GameMode = 0, "", 0, ""
		}
	}

	if phase == "ChampSelect" {
		// One read of the session gives the pick plus the bench and rerolls.
		if cs, err := c.ChampSelectSession(ctx); err == nil {
			id, pos := 0, ""
			next.ChampSelect = nil
			if cs != nil {
				id, pos = cs.Pick()
				rerolls := -1
				if cs.RerollsRemaining != nil {
					rerolls = *cs.RerollsRemaining
				}
				next.ChampSelect = &ChampSelectState{BenchEnabled: cs.BenchEnabled, Bench: cs.BenchIDs(), Rerolls: rerolls}
			}
			if id != next.PickedChampion && id != 0 {
				w.addLog("info", "champ select pick detected")
			}
			next.PickedChampion = id
			next.PickedPosition = pos
		}
	} else {
		next.ChampSelect = nil
		if phase != "InProgress" {
			next.PickedChampion = 0
			next.PickedPosition = ""
		}
	}

	w.setStatus(next, prev)
}

func (w *Watcher) setStatus(next, prev Status) {
	w.mu.Lock()
	w.status = next
	w.mu.Unlock()
	if statusChanged(prev, next) {
		w.publishStatus()
	}
}

func statusChanged(a, b Status) bool {
	if a.Connected != b.Connected || a.Phase != b.Phase || a.Error != b.Error ||
		a.PickedChampion != b.PickedChampion || a.PickedPosition != b.PickedPosition ||
		a.Summoner != b.Summoner || a.Patch != b.Patch || a.QueueID != b.QueueID || a.MapID != b.MapID ||
		!a.ChampSelect.equal(b.ChampSelect) {
		return true
	}
	return false
}

// shortPatch turns "16.17.8104348+..." into "16.17".
func shortPatch(v string) string {
	dots := 0
	for i, r := range v {
		if r == '.' {
			dots++
			if dots == 2 {
				return v[:i]
			}
		}
	}
	return v
}

var _ = errors.New
