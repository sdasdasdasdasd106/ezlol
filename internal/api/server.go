// Package api serves the JSON API, the SSE event stream and the embedded UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/live"
	"github.com/aarlint/ezlol/internal/screen"
	"github.com/aarlint/ezlol/internal/settings"
	"github.com/aarlint/ezlol/internal/update"
	"github.com/aarlint/ezlol/internal/watcher"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	log       *slog.Logger
	watcher   *watcher.Watcher
	dd        *ddragon.Store
	store     *builds.Store
	compiler  *builds.Compiler
	community *builds.Community
	settings  *settings.Store
	update    *update.Checker
	version   string
	live      *live.Client
	player    playerCache
	info      infoCache
	ocr       *screen.Reader
	offer     offerState
	ui        fs.FS  // built frontend, may be nil in dev
	devProxy  string // vite dev server url, may be empty
}

// New wires a server.
func New(log *slog.Logger, w *watcher.Watcher, dd *ddragon.Store, store *builds.Store, comp *builds.Compiler, community *builds.Community, cfg *settings.Store, upd *update.Checker, version string, ui fs.FS, devProxy string) *Server {
	return &Server{log: log, watcher: w, dd: dd, store: store, compiler: comp, community: community, settings: cfg, update: upd, version: version, live: live.New(), ocr: screen.New(), ui: ui, devProxy: devProxy}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/champions", s.champions)
	mux.HandleFunc("GET /api/champions/{key}/build", s.build)
	mux.HandleFunc("GET /api/game", s.game)
	mux.HandleFunc("GET /api/me/{id}", s.meChampion)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/update", s.getUpdate)
	mux.HandleFunc("POST /api/update/check", s.checkUpdate)
	mux.HandleFunc("GET /api/mastery", s.mastery)
	mux.HandleFunc("GET /api/champions/{key}/info", s.championInfo)
	mux.HandleFunc("POST /api/runes/apply", s.applyRunes)
	mux.HandleFunc("GET /api/builds/status", s.buildsStatus)
	mux.HandleFunc("POST /api/builds/compile", s.compile)
	mux.HandleFunc("POST /api/builds/compile/stop", s.compileStop)
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

var validRoles = map[string]bool{"TOP": true, "JUNGLE": true, "MIDDLE": true, "BOTTOM": true, "UTILITY": true}

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
	role := strings.ToUpper(r.URL.Query().Get("role"))
	if role == "NONE" {
		role = "" // ARAM builds carry NONE; let the server pick a lane for Rift
	}
	if role != "" && !validRoles[role] {
		writeErr(w, 400, "role must be TOP, JUNGLE, MIDDLE, BOTTOM or UTILITY")
		return
	}

	// Prefer the live client's patch; fall back to Data Dragon's.
	st := s.watcher.Status()
	patch := st.Patch
	if patch == "" {
		patch = builds.ShortPatch(d.Version)
	}
	// Queue family: explicit ?mode=aram|sr, else whatever the client is queued for.
	queueID := st.QueueID
	switch strings.ToLower(r.URL.Query().Get("mode")) {
	case "aram":
		queueID = 450
	case "sr":
		queueID = 420
	case "arena":
		queueID = 1700
	}
	mode := builds.QueueTag(queueID)
	patch = builds.StoreKey(patch, queueID)

	roles := s.store.Roles(patch, champ.ID)
	if role == "" {
		if len(roles) > 0 {
			role = roles[0].Role
		} else {
			role = defaultRole(champ)
		}
	}

	var b *builds.Build
	if s.community != nil {
		var cb *builds.Build
		var err error
		switch mode {
		case "aram":
			cb, err = s.community.ARAMBuild(r.Context(), d, champ)
		case "arena":
			cb, err = s.community.ArenaBuild(r.Context(), d, champ)
		default:
			cb, err = s.community.RiftBuild(r.Context(), d, champ, role)
		}
		if err != nil {
			s.log.Warn("community build", "champion", champ.Key, "mode", mode, "err", err)
		} else {
			b = cb
			b.Roles = roles
		}
	}
	if b != nil {
		// already resolved from the community source
	} else if agg, err := s.store.Agg(patch, champ.ID, role); err == nil {
		b = builds.Resolve(d, champ, patch, role, roles, agg)
		b.Mode = mode
	} else {
		b = &builds.Build{Champion: champ, Patch: patch, Role: role, Roles: roles, Source: "none", Mode: mode}
		if mode == "aram" {
			b.Notes = append(b.Notes, "大乱斗：下面的符文是客户端官方推荐。")
		}
		if s.compiler.HasKey() {
			b.Notes = append(b.Notes, "这个英雄/位置还没有统计数据，可以在「出装数据」面板里开始统计。")
		} else {
			b.Notes = append(b.Notes, "暂时拿不到出装数据（op.gg 无响应）。可在设置里填 Riot API key，从排位对局自己统计。")
		}
	}

	if mode == "aram" && s.community != nil {
		if augs, scope, err := s.community.Augments(r.Context(), st.Patch, champ.ID); err != nil {
			s.log.Warn("community augments", "champion", champ.Key, "err", err)
		} else {
			b.Augments, b.AugScope = augs, scope
		}
	}
	if mode == "arena" && s.community != nil {
		if augs, scope, err := s.community.ArenaAugments(r.Context(), st.Patch, champ.ID); err != nil {
			s.log.Warn("arena augments", "champion", champ.Key, "err", err)
		} else {
			b.Augments, b.AugScope = augs, scope
		}
	}

	// Riot's in-client recommended runes need no key; add them when the client is
	// up and we have nothing better than them.
	if c := s.watcher.Client(); c != nil && b.Source != "opgg" {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		mapID := 11
		if mode == "aram" {
			mapID = 12
		}
		if mode == "arena" {
			mapID = 30
		}
		pos := role
		if mapID == 12 {
			pos = "NONE"
		}
		if pages, err := c.RecommendedRunes(ctx, champ.ID, pos, mapID); err == nil && len(pages) > 0 {
			rp, sp := builds.FromLCU(d, pages)
			b.Runes = append(b.Runes, rp...)
			if len(b.Spells) == 0 {
				b.Spells = sp
			}
			if b.Source == "none" {
				b.Source = "lcu"
			}
		}
	} else if b.Source == "none" {
		b.Notes = append(b.Notes, "英雄联盟客户端没开：拿不到客户端推荐符文。")
	}
	writeJSON(w, 200, b)
}

// defaultRole guesses a lane from Data Dragon tags when nothing is compiled.
func defaultRole(c ddragon.Champion) string {
	if len(c.Tags) == 0 {
		return "MIDDLE"
	}
	switch c.Tags[0] {
	case "Marksman":
		return "BOTTOM"
	case "Support":
		return "UTILITY"
	case "Tank", "Fighter":
		return "TOP"
	default:
		return "MIDDLE"
	}
}

func (s *Server) buildsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"hasKey":   s.compiler.HasKey(),
		"platform": s.compiler.Platform(),
		"progress": s.compiler.Progress(),
		"patches":  s.store.Patches(),
	})
}

func (s *Server) compile(w http.ResponseWriter, r *http.Request) {
	queue := 0
	switch strings.ToLower(r.URL.Query().Get("queue")) {
	case "aram":
		queue = 450
	case "ranked", "sr":
		queue = 420
	}
	if err := s.compiler.Start(context.Background(), queue); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, s.compiler.Progress())
}

func (s *Server) compileStop(w http.ResponseWriter, r *http.Request) {
	s.compiler.Stop()
	writeJSON(w, 200, s.compiler.Progress())
}

var _ = errors.New

// meChampion returns the local player's mastery and recent record on a champion.
func (s *Server) meChampion(w http.ResponseWriter, r *http.Request) {
	c := s.watcher.Client()
	id, err := strconv.Atoi(r.PathValue("id"))
	if c == nil || err != nil {
		writeJSON(w, 200, map[string]any{})
		return
	}
	s.player.refresh(r.Context(), c)
	tag := builds.QueueTag(s.watcher.Status().QueueID)
	if q := r.URL.Query().Get("mode"); q != "" {
		tag = q
	}
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
