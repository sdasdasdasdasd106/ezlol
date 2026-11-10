package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/screen"
)

// offerCard is one augment on the selection screen with why it ranks where it does.
type offerCard struct {
	builds.Augment
	Score      float64         `json:"score"`
	StageWR    float64         `json:"stageWinRate"` // this champion, this pick stage
	StageGames int             `json:"stageGames"`
	Lift       float64         `json:"lift"` // stage win rate minus the champion's average
	PairWR     float64         `json:"pairWinRate,omitempty"`
	PairGames  int             `json:"pairGames,omitempty"`
	PairWith   string          `json:"pairWith,omitempty"`
	Fit        float64         `json:"fit,omitempty"` // your items: win rate with this augment vs without
	FitOK      bool            `json:"fitOk"`
	FitGames   int             `json:"fitGames,omitempty"`
	Core       *builds.AugCore `json:"core,omitempty"` // what people build after taking it
	Reasons    []string        `json:"reasons"`
}

// augPick is an augment you took (assumed = we guessed the recommended one).
type augPick struct {
	Stage     int              `json:"stage"`
	Level     int              `json:"level"`
	AugmentID int              `json:"augmentId"`
	Name      string           `json:"name"`
	Icon      string           `json:"icon"`
	Rarity    string           `json:"rarity"`
	Assumed   bool             `json:"assumed"`
	Offered   []builds.Augment `json:"offered"` // the choices that were on screen, left to right
}

// augmentOffer is the current augment selection screen, when detected.
type augmentOffer struct {
	Active    bool        `json:"active"`
	Pending   bool        `json:"pending"` // a pick is due (level threshold crossed) but not yet seen on screen
	Level     int         `json:"level"`
	Stage     int         `json:"stage"`            // 1..4 = game start, level 7, 11, 15
	Offered   []offerCard `json:"offered"`          // best first
	Screen    []int       `json:"screen,omitempty"` // augment ids left to right, as on screen (hotkeys 1/2/3)
	Chosen    int         `json:"chosen,omitempty"` // slot (1..3) the player marked with a hotkey, 0 = none yet
	Best      string      `json:"best,omitempty"`
	Why       string      `json:"why,omitempty"`
	Note      string      `json:"note,omitempty"`   // how clear the choice is
	Source    string      `json:"source,omitempty"` // aramkit | aramgg | op.gg
	ScannedAt time.Time   `json:"scannedAt"`
	Lines     []string    `json:"lines,omitempty"` // diagnostics: what OCR last read
}

// Mayhem grants augment picks at game start and at these champion levels.
var pickLevels = []int{1, 7, 11, 15}

func stageOf(level int) int {
	for i, lv := range pickLevels {
		if level == lv {
			return i + 1
		}
	}
	return 0
}

type offerState struct {
	mu        sync.Mutex
	seenLevel map[int]bool // levels whose pick has been completed (offer seen then gone)
	pending   int          // level with a pick outstanding, 0 = none
	pendingAt time.Time
	last      *augmentOffer
	lastScan  time.Time
	lastGT    float64
	lastLines []string
	picks     []augPick
	champ     int
	chosen    int // slot marked with a hotkey while the current offer is up
}

var styleName = map[string]string{"crit": "暴击流", "on_hit": "攻速特效流", "ap": "法强流", "hybrid": "混伤流", "lethality": "穿甲流", "tank": "坦克流", "bruiser": "战士流", "ad": "物理流"}

func styleLabel(k string) string {
	if n, ok := styleName[k]; ok {
		return n
	}
	return k
}

// Picks returns the augments taken so far this game.
func (s *Server) Picks() []augPick {
	s.offer.mu.Lock()
	defer s.offer.mu.Unlock()
	return append([]augPick{}, s.offer.picks...)
}

// detectOffer decides whether an augment pick is likely on screen and, if so,
// OCRs the display to identify the offered augments and rank them.
func (s *Server) detectOffer(ctx context.Context, live *liveResponse) (*augmentOffer, string) {
	st := &s.offer
	st.mu.Lock()
	defer st.mu.Unlock()
	if !s.ocr.Available() {
		return nil, "unavailable"
	}
	if !s.settings.Get().OCR {
		return nil, "disabled"
	}
	me := live.Me
	if me == nil {
		return nil, "available"
	}
	if live.Mode != "aram" {
		return nil, "available"
	}
	// Plain ARAM (queue 450) has no augments; only Mayhem (2400) does.
	if q := s.watcher.Status().QueueID; q == 450 || q == 100 {
		return nil, "available"
	}
	// New game: reset.
	fresh := live.GameTime < st.lastGT-30 || st.seenLevel == nil || st.champ != me.Champion.ID
	if fresh {
		st.seenLevel = map[int]bool{}
		st.pending = 0
		st.last = nil
		st.picks = nil
		st.champ = me.Champion.ID
		s.tlog.Log("game", map[string]any{"champion": me.Champion.Name, "championId": me.Champion.ID, "gameTime": live.GameTime, "level": me.Level})
	}
	st.lastGT = live.GameTime
	// Joined mid-game: picks below the current level already happened.
	if fresh && live.GameTime > 120 {
		for _, lv := range pickLevels {
			if me.Level > lv {
				st.seenLevel[lv] = true
			}
		}
	}
	// Mark a pick pending when a threshold is crossed (start counts as level 1).
	if st.pending == 0 {
		for _, lv := range pickLevels {
			if me.Level >= lv && !st.seenLevel[lv] {
				st.pending, st.pendingAt = lv, time.Now()
				break
			}
		}
	}
	if st.pending == 0 {
		return nil, "available"
	}
	stage := stageOf(st.pending)
	// Picks are only shown while dead or at the fountain; scanning while alive
	// mid-fight wastes CPU, but early game and right after level-up we scan anyway.
	shouldScan := me.IsDead || live.GameTime < 120 || time.Since(st.pendingAt) < 45*time.Second || (st.last != nil && st.last.Active)
	if !shouldScan || time.Since(st.lastScan) < 1500*time.Millisecond {
		if st.last != nil && st.last.Active && time.Since(st.lastScan) < 6*time.Second {
			return st.last, "available"
		}
		return &augmentOffer{Pending: true, Level: st.pending, Stage: stage, Lines: st.lastLines}, "available"
	}
	st.lastScan = time.Now()
	augs, _, err := s.community.Augments(ctx, live.Patch, me.Champion.ID)
	if err != nil {
		augs = nil
	}
	ak, _ := s.community.AramkitChampion(ctx, me.Champion.ID)
	// Every augment either source knows for this champion is a candidate name.
	byID := map[int]builds.Augment{}
	for _, a := range augs {
		byID[a.ID] = a
	}
	if ak != nil {
		var missing []int
		for _, r := range ak.Augments {
			if _, ok := byID[r.ID]; !ok {
				missing = append(missing, r.ID)
			}
		}
		for id, a := range s.community.AugmentInfo(ctx, live.Patch, missing) {
			byID[id] = a
		}
	}
	if len(byID) == 0 {
		return &augmentOffer{Pending: true, Level: st.pending, Stage: stage}, "强化数据加载失败"
	}
	cands := map[string]string{}
	for _, a := range byID {
		addCandidates(cands, a)
	}

	activeBefore := st.last != nil && st.last.Active
	shot := s.tlog.ShotPath(fmt.Sprintf("lv%d", st.pending), false)
	t0 := time.Now()
	lines, err := s.ocr.ReadDump(ctx, shot)
	if err != nil {
		s.tlog.Log("scan", map[string]any{"mode": "aram", "level": st.pending, "stage": stage, "error": err.Error(), "capture": s.ocr.Capture(), "ms": time.Since(t0).Milliseconds()})
		return &augmentOffer{Pending: true, Level: st.pending, Stage: stage}, err.Error()
	}
	var diag []string
	for _, l := range lines {
		if l.Conf >= 0.3 && len(l.Text) >= 3 && len(diag) < 40 {
			diag = append(diag, l.Text)
		}
	}
	st.lastLines = diag
	keys := screen.OfferLayout(screen.Match(lines, cands))
	matched := make([]string, 0, len(keys))
	for _, k := range keys {
		id, _ := strconv.Atoi(k.Key)
		matched = append(matched, byID[id].Name)
	}
	s.tlog.Log("scan", map[string]any{"mode": "aram", "level": st.pending, "stage": stage, "dead": me.IsDead, "ms": time.Since(t0).Milliseconds(),
		"lines": len(lines), "text": diag, "matched": matched, "shot": shot, "lang": s.ocr.Lang(), "capture": s.ocr.Capture()})

	if len(keys) < 2 {
		// Offer disappeared after being seen: the pick is done. Plan A: assume
		// the recommended augment was taken; the player can correct it.
		if activeBefore && time.Since(st.last.ScannedAt) > 4*time.Second {
			s.recordPick(st.last, me.Champion.Name)
			st.seenLevel[st.pending] = true
			st.pending = 0
			st.last = nil
			return nil, "available"
		}
		// Give up waiting after a long time so the next threshold can arm.
		if time.Since(st.pendingAt) > 8*time.Minute {
			st.seenLevel[st.pending] = true
			st.pending = 0
		}
		return &augmentOffer{Pending: st.pending != 0, Level: st.pending, Stage: stage, Lines: st.lastLines}, "available"
	}
	var offered []builds.Augment
	for _, k := range keys {
		id, _ := strconv.Atoi(k.Key)
		if a, ok := byID[id]; ok {
			offered = append(offered, a)
		}
	}
	off := &augmentOffer{Active: true, Level: st.pending, Stage: stage, ScannedAt: time.Now(), Lines: diag, Chosen: st.chosen}
	for _, a := range offered {
		off.Screen = append(off.Screen, a.ID)
	}
	off.Offered, off.Source = s.rankMayhem(ctx, me, stage, offered, ak, st.picks)
	if len(off.Offered) > 0 {
		b := off.Offered[0]
		off.Best = b.Name
		if len(b.Reasons) > 0 {
			off.Why = b.Reasons[0]
		}
		if len(off.Offered) > 1 {
			gap := b.Score - off.Offered[1].Score
			switch {
			case gap < 0.01:
				off.Note = fmt.Sprintf("「%s」和「%s」差距很小（%.1f%%），按你的出装和习惯选都可以。", b.Name, off.Offered[1].Name, gap*100)
			case gap < 0.03:
				off.Note = fmt.Sprintf("「%s」比第二名「%s」略好（+%.1f%%）。", b.Name, off.Offered[1].Name, gap*100)
			default:
				off.Note = fmt.Sprintf("「%s」明显更好，比第二名高 %.1f%%。", b.Name, gap*100)
			}
		}
	}
	if !activeBefore {
		st.chosen = 0
		off.Chosen = 0
		cards := make([]map[string]any, 0, len(off.Offered))
		for _, c := range off.Offered {
			cards = append(cards, map[string]any{"id": c.ID, "name": c.Name, "score": round4(c.Score), "stageWinRate": c.StageWR, "stageGames": c.StageGames,
				"lift": round4(c.Lift), "pairWinRate": c.PairWR, "pairWith": c.PairWith, "fit": round4(c.Fit), "fitOk": c.FitOK, "reasons": c.Reasons})
		}
		s.tlog.Log("offer", map[string]any{"level": off.Level, "stage": stage, "champion": me.Champion.Name, "items": me.Items, "picks": pickNames(st.picks),
			"source": off.Source, "cards": cards, "best": off.Best, "note": off.Note})
		s.log.Info("augment offer detected", "level", off.Level, "offered", len(off.Offered), "best", off.Best)
	}
	st.last = off
	return off, "available"
}

// recordPick stores the pick for a finished offer: the slot marked with a
// hotkey, else (plan A) the recommended one. Caller holds the lock.
func (s *Server) recordPick(off *augmentOffer, champ string) {
	if off == nil || len(off.Offered) == 0 {
		return
	}
	byID := map[int]builds.Augment{}
	for _, c := range off.Offered {
		byID[c.ID] = c.Augment
	}
	b := off.Offered[0].Augment
	assumed := true
	if ch := s.offer.chosen; ch >= 1 && ch <= len(off.Screen) {
		b, assumed = byID[off.Screen[ch-1]], false
	}
	p := augPick{Stage: off.Stage, Level: off.Level, AugmentID: b.ID, Name: b.Name, Icon: b.Icon, Rarity: b.Rarity, Assumed: assumed}
	for _, id := range off.Screen {
		p.Offered = append(p.Offered, byID[id])
	}
	s.offer.picks = append(s.offer.picks, p)
	s.offer.chosen = 0
	s.tlog.Log("pick", map[string]any{"stage": p.Stage, "level": p.Level, "augment": p.Name, "augmentId": p.AugmentID, "assumed": assumed, "champion": champ})
}

// pickSlot marks which of the three on-screen augments (1..3, left to right)
// the player took: during the offer it is remembered for when the offer
// closes; afterwards it corrects the latest recorded pick. Used by hotkeys.
func (s *Server) pickSlot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slot int `json:"slot"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body); err != nil || body.Slot < 1 || body.Slot > 3 {
		writeErr(w, 400, "slot 必须是 1、2 或 3")
		return
	}
	st := &s.offer
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.last != nil && st.last.Active && body.Slot <= len(st.last.Screen) {
		st.chosen = body.Slot
		st.last.Chosen = body.Slot
		name := ""
		for _, c := range st.last.Offered {
			if c.ID == st.last.Screen[body.Slot-1] {
				name = c.Name
			}
		}
		s.tlog.Log("pick-slot", map[string]any{"slot": body.Slot, "augment": name, "during": "offer"})
		writeJSON(w, 200, map[string]any{"slot": body.Slot, "augment": name, "picks": st.picks})
		return
	}
	if len(st.picks) == 0 {
		writeErr(w, 404, "现在没有可以标记的强化")
		return
	}
	p := &st.picks[len(st.picks)-1]
	if body.Slot > len(p.Offered) {
		writeErr(w, 400, "没有这个位置")
		return
	}
	a := p.Offered[body.Slot-1]
	from := p.Name
	p.AugmentID, p.Name, p.Icon, p.Rarity, p.Assumed = a.ID, a.Name, a.Icon, a.Rarity, false
	s.tlog.Log("pick-corrected", map[string]any{"stage": p.Stage, "from": from, "to": a.Name, "augmentId": a.ID, "via": "hotkey"})
	writeJSON(w, 200, map[string]any{"slot": body.Slot, "augment": a.Name, "picks": st.picks})
}

func pickNames(ps []augPick) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

func pct(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + "%" }

func signedPct(f float64) string {
	if f >= 0 {
		return "+" + pct(f)
	}
	return pct(f)
}

func games(n int) string {
	if n >= 10000 {
		return strconv.FormatFloat(float64(n)/10000, 'f', 1, 64) + " 万场"
	}
	return strconv.Itoa(n) + " 场"
}

// rankMayhem orders the offered augments. With aramkit data the score is the
// augment's win rate on this champion at this pick stage (small samples pulled
// toward the champion's average), plus half of how much better it does with
// your current items, plus half of any extra win rate it shows when paired with
// an augment you already have. Without aramkit it falls back to aramgg's
// all-stage win rate.
func (s *Server) rankMayhem(ctx context.Context, me *livePlayer, stage int, offered []builds.Augment, ak *builds.AKChampion, picks []augPick) ([]offerCard, string) {
	cards := make([]offerCard, 0, len(offered))
	if ak == nil {
		for _, a := range offered {
			c := offerCard{Augment: a, StageWR: a.WinRate, StageGames: a.Games, Score: builds.Shrink(a.WinRate, a.Games, 0.5)}
			if a.Games > 0 {
				c.Reasons = append(c.Reasons, fmt.Sprintf("%s 用它胜率 %s（%s）", me.Champion.Name, pct(a.WinRate), games(a.Games)))
			} else {
				c.Reasons = append(c.Reasons, "暂无这个英雄的胜率数据")
			}
			cards = append(cards, c)
		}
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].Score > cards[j].Score })
		return cards, "aramgg"
	}
	abs, err := s.community.AramkitAugmentBuilds(ctx, me.Champion.ID)
	if err != nil {
		s.log.Warn("aramkit augment builds", "champion", me.Champion.Name, "err", err)
	}
	d := s.dd.Data()
	base := ak.WinRate
	pickedIDs := make([]int, 0, len(picks))
	names := map[int]string{}
	for _, p := range picks {
		pickedIDs = append(pickedIDs, p.AugmentID)
		names[p.AugmentID] = p.Name
	}
	stageLabel := map[int]string{1: "开局", 2: "7 级", 3: "11 级", 4: "15 级"}[stage]
	for _, a := range offered {
		c := offerCard{Augment: a}
		if r, ok := ak.StageStat(stage, a.ID); ok {
			c.StageWR, c.StageGames = r.WinRate, r.Games
			// Show one set of numbers everywhere (cards, toast): this stage's.
			c.WinRate, c.Games, c.PickRate = r.WinRate, r.Games, r.PickRate
			c.Tier = map[string]int{"S": 1, "A": 2, "B": 3, "C": 4, "D": 5}[r.Tier]
		} else {
			c.WinRate, c.Games, c.PickRate, c.Tier = 0, 0, 0, 0
		}
		shr := builds.Shrink(c.StageWR, c.StageGames, base)
		c.Lift = c.StageWR - base
		c.Score = shr
		if c.StageGames > 0 {
			c.Reasons = append(c.Reasons, fmt.Sprintf("%s选它胜率 %s，比 %s 平均 %s（%s）", stageLabel, pct(c.StageWR), me.Champion.Name, signedPct(c.Lift), games(c.StageGames)))
		} else {
			c.Score = base - 0.02 // never seen on this champion: rank below known options
			c.Reasons = append(c.Reasons, "这个英雄几乎没人选过它，没有可靠数据")
		}
		if p, partner, ok := ak.BestPair(a.ID, pickedIDs); ok {
			c.PairWR, c.PairGames, c.PairWith = p.WinRate, p.Games, names[partner]
			bonus := builds.Shrink(p.WinRate, p.Games, base) - shr
			if bonus > 0 {
				c.Score += bonus / 2
			}
			c.Reasons = append(c.Reasons, fmt.Sprintf("和已选「%s」一起胜率 %s（%s）", c.PairWith, pct(p.WinRate), games(p.Games)))
		}
		if abs != nil {
			ab := abs[a.ID]
			if fit, n, ok := builds.ItemFit(ak, ab, me.Items); ok {
				c.Fit, c.FitOK, c.FitGames = fit, true, n
				c.Score += fit / 2
				switch {
				case fit >= 0.01:
					c.Reasons = append(c.Reasons, fmt.Sprintf("契合你现在的装备：这些装备配它胜率 %s", signedPct(fit)))
				case fit <= -0.01:
					c.Reasons = append(c.Reasons, fmt.Sprintf("和你现在的装备不太搭：这些装备配它胜率 %s", signedPct(fit)))
				}
			}
			if plan := builds.PlanAugBuild(d, ak, abs, []int{a.ID}, me.Items); len(plan.Cores) > 0 {
				core := plan.Cores[0]
				c.Core = &core
				line := "选它后主流出装：" + styleLabel(core.Style) + " · "
				for i, it := range core.Items {
					if i > 0 {
						line += " → "
					}
					line += it.Name
				}
				c.Reasons = append(c.Reasons, line+fmt.Sprintf("（胜率 %s，%s）", pct(core.WinRate), games(core.Games)))
				if core.Owned == 0 && plan.Slot > 1 {
					c.Reasons = append(c.Reasons, "注意：选它的人大多走"+styleLabel(core.Style)+"，和你已经出的装备不是一个路子，选了可能要转出装")
				}
			}
		}
		cards = append(cards, c)
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Score > cards[j].Score })
	return cards, "aramkit"
}

// correctPick replaces the assumed augment of a stage with the one actually taken.
func (s *Server) correctPick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stage     int `json:"stage"`
		AugmentID int `json:"augmentId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeErr(w, 400, "请求格式不对")
		return
	}
	st := &s.offer
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.picks {
		p := &st.picks[i]
		if p.Stage != body.Stage {
			continue
		}
		for _, a := range p.Offered {
			if a.ID == body.AugmentID {
				from := p.Name
				p.AugmentID, p.Name, p.Icon, p.Rarity, p.Assumed = a.ID, a.Name, a.Icon, a.Rarity, false
				s.tlog.Log("pick-corrected", map[string]any{"stage": p.Stage, "from": from, "to": a.Name, "augmentId": a.ID})
				writeJSON(w, 200, st.picks)
				return
			}
		}
		writeErr(w, 400, "这个强化不在当时的三个选项里")
		return
	}
	writeErr(w, 404, "没有这一阶段的强化记录")
}

// addCandidates registers an augment's Chinese and English names for on-screen matching.
func addCandidates(cands map[string]string, a builds.Augment) {
	for _, name := range []string{a.Name, a.NameEN} {
		if n := screen.Normalize(name); len(n) >= 4 {
			cands[n] = strconv.Itoa(a.ID)
		}
	}
}

// tierLetter turns Tier 1..5 into S/A/B/C/D.
func tierLetter(t int) string {
	if t >= 1 && t <= 5 {
		return string("SABCD"[t-1])
	}
	return "?"
}
