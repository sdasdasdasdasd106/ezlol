package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A pitfall (坑) is an augment a champion cannot make use of: its kit does not
// trigger the effect (e.g. Samira and 弱点). Three sources keep the
// recommendation away from them:
//   - marks: the player's own, from the augment cards; certain, so the
//     augment goes to the bottom. A few known ones are built in.
//   - data: the augment helps champions on average but this champion does
//     clearly worse with it, over enough games. Live in rankMayhem; and a
//     table for every champion (pitdata.json, made by tools/pitscan) for the
//     augment lists outside a game.
//   - AI: the strategy card's "avoid" list, and the review checks mechanics.

type pitfall struct {
	Champion int       `json:"champion"`
	Augment  int       `json:"augment"`
	Note     string    `json:"note,omitempty"`
	Builtin  bool      `json:"builtin,omitempty"`
	Off      bool      `json:"off,omitempty"` // the player removed a built-in one
	At       time.Time `json:"at,omitempty"`
}

// builtinPitfalls are reported by players and certain.
var builtinPitfalls = []pitfall{
	{Champion: 360, Augment: 1092, Note: "莎弥拉触发不了它的效果（玩家反馈）", Builtin: true}, // Samira, 弱点
}

//go:embed pitdata.json
var pitdataJSON []byte

// pitTable is tools/pitscan's output: data pitfalls for every champion.
type pitTable struct {
	Version   string `json:"version"`
	Generated string `json:"generated"`
	Pits      []struct {
		Champion int     `json:"c"`
		Augment  int     `json:"a"`
		Mine     float64 `json:"mine"`
		Avg      float64 `json:"avg"`
		Games    int     `json:"g"`
	} `json:"pits"`
}

var dataPits = func() pitTable {
	var t pitTable
	_ = json.Unmarshal(pitdataJSON, &t)
	return t
}()

type pitfallStore struct {
	mu     sync.Mutex
	loaded bool
	marks  []pitfall
}

func (s *Server) pitfallPath() string { return filepath.Join(s.dataDir, "pitfalls.json") }

// pitfallMarks returns the player's marks (loaded on first use). Caller holds st.mu.
func (s *Server) pitfallMarks() []pitfall {
	st := &s.pits
	if !st.loaded {
		st.loaded = true
		if b, err := os.ReadFile(s.pitfallPath()); err == nil {
			_ = json.Unmarshal(b, &st.marks)
		}
	}
	return st.marks
}

// pitfallFor returns the mark for this champion and augment, if any.
func (s *Server) pitfallFor(champ, aug int) *pitfall {
	st := &s.pits
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, p := range s.pitfallMarks() {
		if p.Champion == champ && p.Augment == aug {
			if p.Off {
				return nil
			}
			cp := p
			return &cp
		}
	}
	for _, p := range builtinPitfalls {
		if p.Champion == champ && p.Augment == aug {
			cp := p
			return &cp
		}
	}
	return nil
}

type pitfallView struct {
	pitfall
	ChampionName string `json:"championName"`
	AugmentName  string `json:"augmentName"`
	Icon         string `json:"icon,omitempty"`
}

// listPitfalls: GET /api/pitfalls — every active mark, built-in ones included.
func (s *Server) listPitfalls(w http.ResponseWriter, r *http.Request) {
	st := &s.pits
	st.mu.Lock()
	var all []pitfall
	off := map[[2]int]bool{}
	for _, p := range s.pitfallMarks() {
		if p.Off {
			off[[2]int{p.Champion, p.Augment}] = true
		} else {
			all = append(all, p)
		}
	}
	st.mu.Unlock()
	for _, p := range builtinPitfalls {
		if !off[[2]int{p.Champion, p.Augment}] {
			all = append(all, p)
		}
	}
	writeJSON(w, 200, s.pitfallViews(r.Context(), all))
}

func (s *Server) pitfallViews(ctx context.Context, list []pitfall) []pitfallView {
	ids := make([]int, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.Augment)
	}
	info := s.community.AugmentInfo(ctx, s.watcher.Status().Patch, ids)
	d := s.dd.Data()
	out := make([]pitfallView, 0, len(list))
	for _, p := range list {
		v := pitfallView{pitfall: p, AugmentName: info[p.Augment].Name, Icon: info[p.Augment].Icon}
		if d != nil {
			v.ChampionName = d.Champions[p.Champion].Name
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ChampionName < out[j].ChampionName })
	return out
}

// setPitfall: POST /api/pitfalls {"champion": 360, "augment": 1092, "on": true, "note": "..."}.
// A read offer on screen is ranked again at once.
func (s *Server) setPitfall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Champion int    `json:"champion"`
		Augment  int    `json:"augment"`
		On       bool   `json:"on"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&body); err != nil || body.Champion <= 0 || body.Augment <= 0 {
		writeErr(w, 400, "请求格式不对")
		return
	}
	note := strings.TrimSpace(body.Note)
	if len([]rune(note)) > 60 {
		note = string([]rune(note)[:60])
	}
	builtin := false
	for _, p := range builtinPitfalls {
		if p.Champion == body.Champion && p.Augment == body.Augment {
			builtin = true
		}
	}
	st := &s.pits
	st.mu.Lock()
	marks := s.pitfallMarks()
	kept := marks[:0:0]
	for _, p := range marks {
		if p.Champion != body.Champion || p.Augment != body.Augment {
			kept = append(kept, p)
		}
	}
	switch {
	case body.On && !builtin:
		kept = append(kept, pitfall{Champion: body.Champion, Augment: body.Augment, Note: note, At: time.Now()})
	case !body.On && builtin:
		kept = append(kept, pitfall{Champion: body.Champion, Augment: body.Augment, Off: true, At: time.Now()})
	}
	st.marks = kept
	b, _ := json.MarshalIndent(kept, "", "  ")
	err := os.WriteFile(s.pitfallPath(), b, 0o644)
	st.mu.Unlock()
	if err != nil {
		writeErr(w, 500, "保存失败："+err.Error())
		return
	}
	s.tlog.Log("pitfall", map[string]any{"champion": body.Champion, "augment": body.Augment, "on": body.On, "note": note})
	s.rerankOffer(r.Context())
	writeJSON(w, 200, map[string]any{"ok": true})
}

// rerankOffer ranks the offer on screen again (after a pitfall mark changed).
func (s *Server) rerankOffer(ctx context.Context) {
	st := &s.offer
	st.mu.Lock()
	active := st.last != nil && st.last.Active
	st.mu.Unlock()
	if !active {
		return
	}
	live, err := s.liveState(ctx)
	if err != nil || !live.InGame || live.Me == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.last == nil || !st.last.Active {
		return
	}
	od, err := s.loadOfferData(ctx, live)
	if err != nil {
		return
	}
	s.showOffer(ctx, live, od, st.last.Screen, st.last.Via, false, "pitfall")
}

// champPitfall is one augment to avoid on a champion, for the augment lists.
type champPitfall struct {
	Augment int     `json:"augment"`
	Kind    string  `json:"kind"` // mark (the player's or built in) | data
	Note    string  `json:"note"`
	Mine    float64 `json:"mine,omitempty"`
	Avg     float64 `json:"avg,omitempty"`
	Games   int     `json:"games,omitempty"`
}

// championPitfalls: GET /api/champions/{key}/pitfalls — the augments to avoid
// on this champion: marks, then the data table.
func (s *Server) championPitfalls(w http.ResponseWriter, r *http.Request) {
	d := s.dd.Data()
	if d == nil {
		writeErr(w, 503, "英雄数据还没加载好")
		return
	}
	champ, ok := d.ChampionByKey(r.PathValue("key"))
	if !ok {
		writeErr(w, 404, "找不到这个英雄")
		return
	}
	out := []champPitfall{}
	seen := map[int]bool{}
	st := &s.pits
	st.mu.Lock()
	off := map[int]bool{}
	for _, p := range s.pitfallMarks() {
		if p.Champion != champ.ID {
			continue
		}
		if p.Off {
			off[p.Augment] = true
			continue
		}
		seen[p.Augment] = true
		out = append(out, champPitfall{Augment: p.Augment, Kind: "mark", Note: firstNonEmpty(p.Note, "你标记过这个英雄用不了它")})
	}
	st.mu.Unlock()
	for _, p := range builtinPitfalls {
		if p.Champion == champ.ID && !off[p.Augment] && !seen[p.Augment] {
			seen[p.Augment] = true
			out = append(out, champPitfall{Augment: p.Augment, Kind: "mark", Note: p.Note})
		}
	}
	for _, p := range dataPits.Pits {
		if p.Champion == champ.ID && !seen[p.Augment] && !off[p.Augment] {
			out = append(out, champPitfall{Augment: p.Augment, Kind: "data", Mine: p.Mine, Avg: p.Avg, Games: p.Games,
				Note: fmt.Sprintf("别的英雄用它平均 %s，%s 用它 %s（%s）", signedPct(p.Avg), champ.Name, signedPct(p.Mine), games(p.Games))})
		}
	}
	writeJSON(w, 200, map[string]any{"version": dataPits.Version, "pitfalls": out})
}
