package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/live"
	"github.com/aarlint/ezlol/internal/screen"
)

// livePlayer is the little the augment assistant needs about each player.
type livePlayer struct {
	Name     string           `json:"name"`
	Champion ddragon.Champion `json:"champion"`
	Team     string           `json:"team"`
	IsDead   bool             `json:"isDead"`
	Level    int              `json:"level"`
	IsMe     bool             `json:"isMe"`
	Items    []int            `json:"items"` // item ids in the inventory
}

// liveResponse is what the in-game augment assistant polls.
type liveResponse struct {
	InGame   bool          `json:"inGame"`
	GameTime float64       `json:"gameTime"`
	Started  bool          `json:"started"` // past the loading screen (GameStart event seen)
	MapID    int           `json:"mapId"`
	Mode     string        `json:"mode"` // aram | arena | sr
	Me       *livePlayer   `json:"me,omitempty"`
	Patch    string        `json:"patch"`
	Players  []livePlayer  `json:"-"`
	Picks    []augPick     `json:"picks"` // augments taken this game (ARAM Mayhem)
	Offer    *augmentOffer `json:"offer,omitempty"`
	OCR      string        `json:"ocr"`                // available | disabled | unavailable | error text
	OCRLang  string        `json:"ocrLang"`            // language the OCR helper reads, e.g. zh-Hans-CN
	Research *researchView `json:"research,omitempty"` // AI strategy card for this champion
	// The game runs as administrator and ezlol does not: hotkeys cannot reach
	// ezlol while the game has focus.
	NeedAdmin bool `json:"needAdmin,omitempty"`
}

// game reports your champion and level in the running game, plus the augment
// offer currently on screen (if any), ranked best first.
func (s *Server) game(w http.ResponseWriter, r *http.Request) {
	resp, err := s.liveState(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if !resp.InGame {
		writeJSON(w, 200, resp)
		return
	}
	resp.Offer, resp.OCR = s.detectOffer(r.Context(), resp)
	resp.OCRLang = s.ocr.Lang()
	if resp.Me != nil && resp.Mode == "aram" {
		if q := s.watcher.Status().QueueID; q != 450 && q != 100 {
			resp.Research = s.research(resp.Me.Champion.ID, true)
		}
	}
	resp.Picks = s.Picks()
	resp.NeedAdmin = s.gameNeedsAdmin()
	writeJSON(w, 200, resp)
}

// adminCheck caches whether the game runs as administrator (checked every 30 s).
type adminCheck struct {
	mu   sync.Mutex
	at   time.Time
	need bool
}

func (s *Server) gameNeedsAdmin() bool {
	a := &s.admin
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.at) > 30*time.Second {
		a.at, a.need = time.Now(), screen.GameNeedsAdmin()
		if a.need {
			s.tlog.Log("need-admin", map[string]any{"why": "the game runs as administrator, ezlol does not"})
		}
	}
	return a.need
}

// liveState reads the running game from the live client API (InGame false
// when there is none).
func (s *Server) liveState(ctx context.Context) (*liveResponse, error) {
	gd, err := s.live.Get(ctx)
	if err != nil {
		return nil, err
	}
	if gd == nil {
		return &liveResponse{InGame: false}, nil
	}
	d := s.dd.Data()
	resp := &liveResponse{InGame: true, GameTime: gd.GameData.GameTime, MapID: gd.GameData.MapNumber, Patch: s.watcher.Status().Patch}
	if resp.Patch == "" && d != nil {
		resp.Patch = builds.ShortPatch(d.Version)
	}
	switch {
	case gd.GameData.MapNumber == 30:
		resp.Mode = "arena"
	case gd.GameData.MapNumber == 12:
		resp.Mode = "aram"
	default:
		resp.Mode = builds.QueueTag(s.watcher.Status().QueueID)
	}
	meName := gd.ActivePlayer.RiotIDName
	if meName == "" {
		meName = gd.ActivePlayer.SummonerName
	}
	for _, p := range gd.AllPlayers {
		lp := livePlayer{Name: firstNonEmpty(p.RiotIDName, p.SummonerName), Team: p.Team, IsDead: p.IsDead, Level: p.Level}
		lp.IsMe = lp.Name == meName
		lp.Champion = resolveChampion(d, p)
		for _, it := range p.Items {
			lp.Items = append(lp.Items, it.ID)
		}
		resp.Players = append(resp.Players, lp)
		if lp.IsMe {
			me := lp
			resp.Me = &me
		}
	}
	for _, ev := range gd.Events.Events {
		if ev.Name == "GameStart" {
			resp.Started = true
			break
		}
	}
	return resp, nil
}

// resolveChampion maps the live "rawChampionName" (game_character_displayname_KaiSa)
// onto a Data Dragon champion. The raw name is language independent, so this
// works with a Chinese client too.
func resolveChampion(d *ddragon.Data, p live.Player) ddragon.Champion {
	c := ddragon.Champion{Name: p.ChampionName}
	if d == nil {
		return c
	}
	raw := p.RawChampionName
	if i := strings.LastIndex(raw, "_"); i >= 0 {
		raw = raw[i+1:]
	}
	if found, ok := d.ChampionByKey(raw); ok {
		return found
	}
	norm := strings.NewReplacer("'", "", " ", "", ".", "", "&", "").Replace(p.ChampionName)
	if found, ok := d.ChampionByKey(norm); ok {
		return found
	}
	// Transformed forms carry a suffix ("GnarBig"): take the longest champion
	// key the raw name starts with.
	var best ddragon.Champion
	for _, ch := range d.Champions {
		if len(ch.Key) > len(best.Key) && len(ch.Key) >= 3 && strings.HasPrefix(strings.ToLower(raw), strings.ToLower(ch.Key)) {
			best = ch
		}
	}
	if best.ID != 0 {
		return best
	}
	for _, ch := range d.Champions {
		if strings.EqualFold(ch.Name, p.ChampionName) {
			return ch
		}
	}
	return c
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// augmentBuild is the item plan for a champion given augments: what to build
// next (by your owned items) and the best core sets, from aramkit's
// per-augment ARAM Mayhem data. ?augments=1,2&owned=3031,3006
func (s *Server) augmentBuild(w http.ResponseWriter, r *http.Request) {
	d := s.dd.Data()
	if d == nil {
		writeErr(w, 503, "英雄数据还没加载好")
		return
	}
	key := r.PathValue("key")
	champ, ok := d.ChampionByKey(key)
	if id, err := strconv.Atoi(key); err == nil {
		champ, ok = d.Champions[id]
	}
	if !ok {
		writeErr(w, 404, "找不到这个英雄")
		return
	}
	augs := intList(r.URL.Query().Get("augments"), 4)
	owned := intList(r.URL.Query().Get("owned"), 12)
	if len(augs) == 0 {
		writeErr(w, 400, "没有指定强化")
		return
	}
	ak, err := s.community.AramkitChampion(r.Context(), champ.ID)
	if err != nil {
		writeErr(w, 502, "拿不到强化出装数据："+err.Error())
		return
	}
	abs, err := s.community.AramkitAugmentBuilds(r.Context(), champ.ID)
	if err != nil {
		writeErr(w, 502, "拿不到强化出装数据："+err.Error())
		return
	}
	plan := builds.PlanAugBuild(d, ak, abs, augs, owned)
	info := s.community.AugmentInfo(r.Context(), s.watcher.Status().Patch, augs)
	names := make([]builds.Augment, 0, len(augs))
	for _, id := range augs {
		names = append(names, info[id])
	}
	writeJSON(w, 200, map[string]any{"plan": plan, "augments": names, "championWinRate": ak.WinRate, "version": ak.Version})
}

func intList(s string, maxN int) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 && len(out) < maxN {
			out = append(out, n)
		}
	}
	return out
}

// openLogs opens the test-log folder in the system file manager.
func (s *Server) openLogs(w http.ResponseWriter, r *http.Request) {
	dir := s.tlog.Dir()
	if dir == "" {
		writeErr(w, 404, "没有日志目录")
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()
	writeJSON(w, 200, map[string]any{"dir": dir})
}
