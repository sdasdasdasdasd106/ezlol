// Package hotkey watches key combinations by polling the keyboard state
// (GetAsyncKeyState on Windows), the way OBS does for its hotkeys. Unlike
// RegisterHotKey (Electron's globalShortcut), this still sees the keys while a
// fullscreen game has focus: games often switch registered hotkeys off for
// themselves. It only reads which keys are down; nothing is sent to the game.
package hotkey

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Virtual-key codes (Windows).
const (
	vkShift    = 0x10
	vkControl  = 0x11
	vkAlt      = 0x12
	vkXButton1 = 0x05 // mouse side button "back"
	vkXButton2 = 0x06 // mouse side button "forward"
)

var modKeys = []int{vkControl, vkAlt, vkShift}

// Combo is a key plus the modifiers that must be held (and no others).
type Combo struct {
	Mods []int
	Key  int
}

// Parse reads an Electron-style accelerator: "CommandOrControl+Alt+S", "F8",
// "num1", "Mouse4" (side button back), "Mouse5" (side button forward).
func Parse(accel string) (Combo, error) {
	var c Combo
	parts := strings.Split(accel, "+")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		last := i == len(parts)-1
		switch strings.ToLower(p) {
		case "commandorcontrol", "cmdorctrl", "control", "ctrl", "command", "cmd":
			if !last {
				c.Mods = append(c.Mods, vkControl)
				continue
			}
		case "alt", "option":
			if !last {
				c.Mods = append(c.Mods, vkAlt)
				continue
			}
		case "shift":
			if !last {
				c.Mods = append(c.Mods, vkShift)
				continue
			}
		}
		if !last {
			return c, fmt.Errorf("unknown modifier %q", p)
		}
		k, ok := keyCode(p)
		if !ok {
			return c, fmt.Errorf("unknown key %q", p)
		}
		c.Key = k
	}
	if c.Key == 0 {
		return c, fmt.Errorf("no key in %q", accel)
	}
	return c, nil
}

func keyCode(k string) (int, bool) {
	l := strings.ToLower(k)
	switch {
	case len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z':
		return int(k[0]), true
	case len(k) == 1 && k[0] >= 'a' && k[0] <= 'z':
		return int(k[0] - 'a' + 'A'), true
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		return int(k[0]), true
	case l == "mouse4":
		return vkXButton1, true
	case l == "mouse5":
		return vkXButton2, true
	case strings.HasPrefix(l, "num") && len(l) == 4 && l[3] >= '0' && l[3] <= '9':
		return 0x60 + int(l[3]-'0'), true
	case strings.HasPrefix(l, "f"):
		var n int
		if _, err := fmt.Sscanf(l[1:], "%d", &n); err == nil && n >= 1 && n <= 24 && fmt.Sprint(n) == l[1:] {
			return 0x70 + n - 1, true
		}
	}
	return 0, false
}

// Matches reports whether the combo is held: its key and modifiers down, and
// no other modifier (so Alt+1 and Ctrl+Alt+1 stay apart).
func (c Combo) Matches(down func(vk int) bool) bool {
	if !down(c.Key) {
		return false
	}
	for _, m := range modKeys {
		want := false
		for _, cm := range c.Mods {
			if cm == m {
				want = true
			}
		}
		if down(m) != want {
			return false
		}
	}
	return true
}

// Watcher polls the keyboard and calls fire(action) when a bound combo is
// pressed (once per press).
type Watcher struct {
	mu        sync.Mutex
	binds     map[string]Combo
	held      map[string]bool
	suspended bool
	fire      func(action string)
	down      func(vk int) bool // nil: polling is not available here
}

// New makes a watcher; Run starts it.
func New(fire func(action string)) *Watcher {
	return &Watcher{binds: map[string]Combo{}, held: map[string]bool{}, fire: fire, down: keyDown}
}

// Supported reports whether keys can be polled on this platform.
func (w *Watcher) Supported() bool { return w.down != nil }

// Set replaces the bindings (action -> accelerator). It returns the ones it
// could not read.
func (w *Watcher) Set(binds map[string]string) []string {
	var bad []string
	m := map[string]Combo{}
	for action, accel := range binds {
		c, err := Parse(accel)
		if err != nil {
			bad = append(bad, accel)
			continue
		}
		m[action] = c
	}
	w.mu.Lock()
	w.binds = m
	w.held = map[string]bool{}
	w.mu.Unlock()
	return bad
}

// Suspend stops reacting (while Settings records a new hotkey).
func (w *Watcher) Suspend(on bool) {
	w.mu.Lock()
	w.suspended = on
	w.mu.Unlock()
}

// Run polls until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	if w.down == nil {
		return
	}
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.poll()
		}
	}
}

func (w *Watcher) poll() {
	w.mu.Lock()
	var fired []string
	for action, c := range w.binds {
		on := c.Matches(w.down)
		if on && !w.held[action] && !w.suspended {
			fired = append(fired, action)
		}
		w.held[action] = on
	}
	w.mu.Unlock()
	for _, a := range fired {
		go w.fire(a)
	}
}
