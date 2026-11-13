// Package api serves the JSON API, the SSE event stream and the embedded UI.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aarlint/ezlol/internal/ai"
	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/hotkey"
	"github.com/aarlint/ezlol/internal/live"
	"github.com/aarlint/ezlol/internal/screen"
	"github.com/aarlint/ezlol/internal/settings"
	"github.com/aarlint/ezlol/internal/testlog"
	"github.com/aarlint/ezlol/internal/update"
	"github.com/aarlint/ezlol/internal/watcher"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	log       *slog.Logger
	watcher   *watcher.Watcher
	dd        *ddragon.Store
	community *builds.Community
	settings  *settings.Store
	update    *update.Checker
	version   string
	live      *live.Client
	player    playerCache
	info      infoCache
	ocr       *screen.Reader
	tlog      *testlog.Logger
	repo      string // owner/name of this build's GitHub repository (log uploads)
	dataDir   string
	ai        *ai.Client
	aiSt      aiState
	upload    uploadState
	offer     offerState
	keys      *hotkey.Watcher
	hk        hotkeyState
	admin     adminCheck
	pits      pitfallStore
	ui        fs.FS  // built frontend, may be nil in dev
	devProxy  string // vite dev server url, may be empty
}

// New wires a server.
func New(log *slog.Logger, w *watcher.Watcher, dd *ddragon.Store, community *builds.Community, cfg *settings.Store, upd *update.Checker, tlog *testlog.Logger, dataDir, repo, version string, ui fs.FS, devProxy string) *Server {
	aic := newAIClient(dataDir, func() string { return cfg.Get().DeepSeekKey })
	s := &Server{log: log, watcher: w, dd: dd, community: community, repo: repo, dataDir: dataDir, ai: aic, settings: cfg, update: upd, tlog: tlog, version: version, live: live.New(), ocr: screen.New(), ui: ui, devProxy: devProxy}
	s.startHotkeys()
	return s
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/champions", s.champions)
	mux.HandleFunc("GET /api/champions/{key}/build", s.build)
	mux.HandleFunc("GET /api/game", s.game)
	mux.HandleFunc("POST /api/game/pick", s.correctPick)
	mux.HandleFunc("POST /api/game/pick-slot", s.pickSlot)
	mux.HandleFunc("POST /api/game/scan", s.scanNow)
	mux.HandleFunc("POST /api/hotkeys", s.setHotkeys)
	mux.HandleFunc("GET /api/pitfalls", s.listPitfalls)
	mux.HandleFunc("GET /api/champions/{key}/pitfalls", s.championPitfalls)
	mux.HandleFunc("POST /api/pitfalls", s.setPitfall)
	mux.HandleFunc("POST /api/hotkeys/fire", s.fireHotkeyHTTP)
	mux.HandleFunc("GET /api/ai/research", s.aiResearch)
	mux.HandleFunc("GET /api/champions/{key}/augment-build", s.augmentBuild)
	mux.HandleFunc("POST /api/logs/open", s.openLogs)
	mux.HandleFunc("POST /api/logs/upload", s.uploadLogs)
	mux.HandleFunc("GET /api/me/{id}", s.meChampion)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/update", s.getUpdate)
	mux.HandleFunc("POST /api/update/check", s.checkUpdate)
	mux.HandleFunc("GET /api/mastery", s.mastery)
	mux.HandleFunc("GET /api/champions/{key}/info", s.championInfo)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "not found") })

	switch {
	case s.devProxy != "":
		target, err := url.Parse(s.devProxy)
		if err != nil {
			panic(err)
		}
		mux.Handle("/", httputil.NewSingleHostReverseProxy(target))
	case s.ui != nil:
		mux.Handle("/", spaHandler(s.ui))
	default:
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "ui not built; run `make web` or set EZLOL_DEV=http://localhost:5173", 503)
		})
	}
	return localOnly(secureHeaders(logRequests(s.log, mux)))
}

func spaHandler(ui fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(ui, p); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

// localOnly rejects requests that do not come from this machine's own pages:
// a Host that is not loopback (DNS rebinding) or a cross-site Origin (another
// website in the browser trying to drive ezlol).
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "null" {
			u, err := url.Parse(o)
			if err != nil || !isLoopbackHost(u.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://ddragon.leagueoflegends.com https://raw.communitydragon.org data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; connect-src 'self' ws://localhost:* ws://127.0.0.1:* http://localhost:*; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			log.Debug("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type statusResponse struct {
	watcher.Status
	Logs      []watcher.LogEntry `json:"logs"`
	DDVersion string             `json:"ddVersion"`
	Version   string             `json:"version"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{Status: s.watcher.Status(), Logs: s.watcher.Logs(), Version: s.version}
	if d := s.dd.Data(); d != nil {
		resp.DDVersion = d.Version
	}
	writeJSON(w, 200, resp)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, unsub := s.watcher.Subscribe()
	defer unsub()

	send := func(ev watcher.Event) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
		fl.Flush()
	}
	b, _ := json.Marshal(s.watcher.Status())
	send(watcher.Event{Type: "status", Data: b})

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			send(ev)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) champions(w http.ResponseWriter, r *http.Request) {
	d := s.dd.Data()
	if d == nil {
		writeErr(w, 503, "英雄数据还没加载好")
		return
	}
	list := make([]ddragon.Champion, 0, len(d.Champions))
	for _, c := range d.Champions {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, 200, map[string]any{"version": d.Version, "champions": list})
}

// build returns the ARAM Mayhem build and augment list for a champion:
// aramkit (Mayhem data) first, op.gg's ARAM data and aramgg as fallbacks.
func (s *Server) build(w http.ResponseWriter, r *http.Request) {
	d := s.dd.Data()
	if d == nil {
		writeErr(w, 503, "英雄数据还没加载好")
		return
	}
	key := r.PathValue("key")
	var champ ddragon.Champion
	var ok bool
	if id, err := strconv.Atoi(key); err == nil {
		champ, ok = d.Champions[id]
	} else {
		champ, ok = d.ChampionByKey(key)
	}
	if !ok {
		writeErr(w, 404, "找不到这个英雄")
		return
	}
	patch := s.watcher.Status().Patch
	if patch == "" {
		patch = builds.ShortPatch(d.Version)
	}
	b, err := s.community.AramkitBuild(r.Context(), d, champ)
	if err != nil {
		s.log.Warn("aramkit build", "champion", champ.Key, "err", err)
		if b, err = s.community.ARAMBuild(r.Context(), d, champ); err != nil {
			s.log.Warn("op.gg build", "champion", champ.Key, "err", err)
			b = &builds.Build{Champion: champ, Patch: patch, Mode: "aram", Source: "none", Notes: []string{"暂时拿不到出装数据，请检查网络后重试。"}}
		}
	}
	if augs, err := s.community.AramkitAugments(r.Context(), patch, champ.ID); err == nil && len(augs) > 0 {
		b.Augments, b.AugScope, b.AugFrom = augs, "champion", "aramkit"
	} else if augs, scope, err := s.community.Augments(r.Context(), patch, champ.ID); err == nil {
		b.Augments, b.AugScope, b.AugFrom = augs, scope, "aramgg"
	} else {
		s.log.Warn("augments", "champion", champ.Key, "err", err)
	}
	writeJSON(w, 200, b)
}

// meChampion returns the local player's mastery and recent record on a champion.
func (s *Server) meChampion(w http.ResponseWriter, r *http.Request) {
	c := s.watcher.Client()
	id, err := strconv.Atoi(r.PathValue("id"))
	if c == nil || err != nil {
		writeJSON(w, 200, map[string]any{})
		return
	}
	s.player.refresh(r.Context(), c)
	tag := "aram"
	m, rec := s.player.lookup(tag, id)
	writeJSON(w, 200, map[string]any{"mastery": m, "record": rec, "mode": tag})
}

// mastery returns the local player's mastery for every champion.
func (s *Server) mastery(w http.ResponseWriter, r *http.Request) {
	c := s.watcher.Client()
	if c == nil {
		writeJSON(w, 200, map[string]any{})
		return
	}
	s.player.refresh(r.Context(), c)
	writeJSON(w, 200, s.player.AllMastery())
}
