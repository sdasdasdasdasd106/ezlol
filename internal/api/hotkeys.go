package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/hotkey"
	"github.com/aarlint/ezlol/internal/watcher"
)

// Hotkeys arrive two ways and both end in fireHotkey:
//   - "poll": this process reads the keyboard state (Windows, see package
//     hotkey). It works while a fullscreen game has focus.
//   - "shortcut": the desktop shell's registered hotkey (Electron
//     globalShortcut), which fullscreen games often switch off.
// When both see the same press, the second is ignored. The result goes to the
// window as an SSE "hotkey" event (the player switches out to read it).

type hotkeyState struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var hotkeyActions = map[string]bool{"scan": true, "pick1": true, "pick2": true, "pick3": true}

// startHotkeys begins polling the keyboard (a no-op where that is not possible).
func (s *Server) startHotkeys() {
	s.keys = hotkey.New(func(action string) { s.fireHotkey(action, "poll") })
	go s.keys.Run(context.Background())
}

// setHotkeys: POST /api/hotkeys {"bindings": {"scan": "CommandOrControl+Alt+S", ...}, "suspended": bool}
// from the desktop shell, at start and whenever Settings change them.
func (s *Server) setHotkeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bindings  map[string]string `json:"bindings"`
		Suspended *bool             `json:"suspended"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "请求格式不对")
		return
	}
	var bad []string
	if body.Bindings != nil {
		b := map[string]string{}
		for k, v := range body.Bindings {
			if hotkeyActions[k] && v != "" {
				b[k] = v
			}
		}
		bad = s.keys.Set(b)
		s.tlog.Log("hotkeys", map[string]any{"bindings": b, "bad": bad, "polling": s.keys.Supported()})
	}
	if body.Suspended != nil {
		s.keys.Suspend(*body.Suspended)
	}
	writeJSON(w, 200, map[string]any{"polling": s.keys.Supported(), "bad": bad})
}

// fireHotkeyHTTP: POST /api/hotkeys/fire {"action": "scan"} — the shell's registered hotkey.
func (s *Server) fireHotkeyHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil || !hotkeyActions[body.Action] {
		writeErr(w, 400, "没有这个快捷键动作")
		return
	}
	go s.fireHotkey(body.Action, "shortcut")
	writeJSON(w, 202, map[string]any{"ok": true})
}

// fireHotkey runs a hotkey's action once per press and tells the window.
func (s *Server) fireHotkey(action, source string) {
	s.hk.mu.Lock()
	if s.hk.last == nil {
		s.hk.last = map[string]time.Time{}
	}
	dup := time.Since(s.hk.last[action]) < 800*time.Millisecond
	if !dup {
		s.hk.last[action] = time.Now()
	}
	s.hk.mu.Unlock()
	if dup {
		return
	}
	s.tlog.Log("hotkey", map[string]any{"action": action, "source": source})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var code int
	var out map[string]any
	if action == "scan" {
		code, out = s.doScan(ctx, false)
	} else {
		code, out = s.doPick(int(action[len(action)-1] - '0'))
	}
	text, say := hotkeyText(action, code, out)
	b, _ := json.Marshal(map[string]any{"action": action, "ok": code < 300, "text": text, "say": say, "source": source})
	s.watcher.Publish(watcher.Event{Type: "hotkey", Data: b})
}

// hotkeyText is what the window shows for a hotkey's result; say = speak it
// too (a read offer is spoken by the window itself).
func hotkeyText(action string, code int, out map[string]any) (string, bool) {
	if e, ok := out["error"].(string); ok {
		return e, true
	}
	if action == "scan" {
		if code == 202 {
			m, _ := out["message"].(string)
			return m, true
		}
		names, _ := out["names"].([]string)
		return fmt.Sprintf("识别到：%s，推荐「%v」", strings.Join(names, " / "), out["best"]), false
	}
	return fmt.Sprintf("已记录：选了第 %v 个「%v」", out["slot"], out["augment"]), false
}
