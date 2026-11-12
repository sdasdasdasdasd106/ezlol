package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
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
	PairGlobal bool            `json:"pairGlobal,omitempty"` // pair figures are all-champion, not this champion's
	Fit        float64         `json:"fit,omitempty"`        // your items: win rate with this augment vs without
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
	Active    bool          `json:"active"`
	Pending   bool          `json:"pending"`           // a pick is due (level threshold crossed) but not yet seen on screen
	Waiting   bool          `json:"waiting,omitempty"` // loading screen: the game has not started yet
	Level     int           `json:"level"`
	Stage     int           `json:"stage"`            // 1..4 = game start, level 7, 11, 15
	Offered   []offerCard   `json:"offered"`          // best first
	Screen    []int         `json:"screen,omitempty"` // augment ids left to right, as on screen (hotkeys 1/2/3)
	Chosen    int           `json:"chosen,omitempty"` // slot (1..3) the player marked with a hotkey, 0 = none yet
	Best      string        `json:"best,omitempty"`
	Why       string        `json:"why,omitempty"`
	Note      string        `json:"note,omitempty"`   // how clear the choice is
	Source    string        `json:"source,omitempty"` // aramkit | aramgg | op.gg
	Via       string        `json:"via,omitempty"`    // ocr | ai (worked out by the AI from the OCR text)
	AI        *aiReviewView `json:"ai,omitempty"`     // the AI's second opinion
	sig       string
	ScannedAt time.Time `json:"scannedAt"`
	Lines     []string  `json:"lines,omitempty"` // diagnostics: what OCR last read
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
	mu         sync.Mutex
	seenLevel  map[int]bool // levels whose pick has been completed (offer seen then gone)
	pending    int          // level with a pick outstanding, 0 = none
	pendingAt  time.Time
	last       *augmentOffer
	lastGT     float64
	lastLines  []string
	picks      []augPick
	champ      int
	chosen     int            // slot marked with a hotkey while the current offer is up
	lastOCR    []screen.Line  // raw lines of the last scan (for the AI text reader)
	lastNames  map[string]int // display name -> augment id
	champName  string
	aiManualAt time.Time // a scan hotkey press handed the text to the AI at this time
	// AI (DeepSeek): reviews per offer, and the screenshot reader.
	reviews    map[string]*aiReviewView
	aiRunning  bool
	aiAt       time.Time // last started
	aiDone     time.Time // last answered
	aiChoosing bool
	aiIDs      []int
}

func styleLabel(k string) string { return builds.StyleName(k) }

// Picks returns the augments taken so far this game.
func (s *Server) Picks() []augPick {
	s.offer.mu.Lock()
	defer s.offer.mu.Unlock()
	return append([]augPick{}, s.offer.picks...)
}

// syncGame updates the per-game state from the live data: it resets on a new
// game and arms a pending pick when a level threshold is crossed. When there
// is nothing to scan for it returns stop = true with the offer/status to
// report and, for the manual scan, why in words. Caller holds s.offer.mu.
func (s *Server) syncGame(live *liveResponse) (off *augmentOffer, status, why string, stop bool) {
	st := &s.offer
	if !s.ocr.Available() {
		return nil, "unavailable", "这台电脑用不了屏幕识别", true
	}
	if !s.settings.Get().OCR {
		return nil, "disabled", "屏幕识别在设置里关掉了", true
	}
	me := live.Me
	if me == nil || live.Mode != "aram" {
		return nil, "available", "只有海克斯乱斗有强化可选", true
	}
	// Plain ARAM (queue 450) has no augments; only Mayhem (2400) does.
	if q := s.watcher.Status().QueueID; q == 450 || q == 100 {
		return nil, "available", "普通大乱斗没有强化可选", true
	}
	// New game: the clock went back, or nothing is known yet. (Not a champion
	// change alone: Gnar, Kayn & co. change form mid-game.)
	fresh := st.seenLevel == nil || live.GameTime < st.lastGT-30
	if fresh {
		st.seenLevel = map[int]bool{}
		st.pending = 0
		st.last = nil
		st.picks = nil
		st.champ = 0
		st.chosen = 0
		st.aiManualAt = time.Time{}
		s.tlog.Log("game", map[string]any{"champion": me.Champion.Name, "championId": me.Champion.ID, "gameTime": live.GameTime, "level": me.Level, "started": live.Started})
	}
	st.lastGT = live.GameTime
	st.champName = me.Champion.Name
	if me.Champion.ID != 0 {
		st.champ = me.Champion.ID
	} else if st.champ != 0 {
		me.Champion.ID = st.champ // transformed form we could not resolve
	}
	if !live.Started || live.GameTime < 1 {
		return &augmentOffer{Waiting: true}, "available", "还在加载画面，进入游戏后再按", true
	}
	// Joined mid-game: picks below the current level already happened.
	if fresh && live.GameTime > 120 {
		for _, lv := range pickLevels {
			if me.Level > lv {
				st.seenLevel[lv] = true
			}
		}
	}
	// The next pick level has been reached: an offer still shown is recorded,
	// a pick never read is skipped (otherwise a scan would use the old stage).
	if st.pending != 0 {
		for _, lv := range pickLevels {
			if lv > st.pending && me.Level >= lv {
				if st.last != nil && st.last.Active {
					s.closeOffer()
				} else {
					s.tlog.Log("pick-missed", map[string]any{"level": st.pending, "stage": stageOf(st.pending), "why": "next-level"})
					st.seenLevel[st.pending] = true
					st.pending = 0
				}
				break
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
	return nil, "", "", false
}

// offerData is everything needed to read and rank the cards for one champion.
type offerData struct {
	byID  map[int]builds.Augment
	cands map[string]string // normalized name -> augment id (string)
	names map[string]int    // display name -> augment id
	ak    *builds.AKChampion
}

// loadOfferData collects every augment name that can be offered to this
// champion (all cached after the first call). Caller holds s.offer.mu.
func (s *Server) loadOfferData(ctx context.Context, live *liveResponse) (*offerData, error) {
	me := live.Me
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
		return nil, fmt.Errorf("强化数据加载失败")
	}
	// Augments without statistics for this champion can still be offered.
	var rest []int
	for _, id := range s.community.AllAugmentIDs(ctx, live.Patch) {
		if _, ok := byID[id]; !ok {
			rest = append(rest, id)
		}
	}
	od := &offerData{byID: byID, cands: map[string]string{}, names: map[string]int{}, ak: ak}
	addName := func(id int, a builds.Augment) {
		addCandidates(od.cands, a)
		for _, n := range []string{a.Name, a.NameCN} {
			if n != "" {
				od.names[n] = id
			}
		}
	}
	for id, a := range s.community.AugmentInfo(ctx, live.Patch, rest) {
		byID[id] = a
		addName(id, a)
	}
	// Names shared by two ids (Arena and Mayhem versions): the one with
	// statistics for this champion wins, so it is added last.
	for id, a := range byID {
		if !slices.Contains(rest, id) {
			addName(id, a)
		}
	}
	s.offer.lastNames = od.names
	return od, nil
}

// screenRead is one capture of the screen and what was matched on it.
type screenRead struct {
	lines []screen.Line
	diag  []string     // readable lines, for the log and the UI
	keys  []screen.Hit // matched names laid out like the selection cards
	ids   []int        // their augment ids, left to right
	shot  string
	ms    int64
}

// readScreen captures the screen once, OCRs it and matches augment names.
// Caller holds s.offer.mu.
func (s *Server) readScreen(ctx context.Context, od *offerData, tag string) (*screenRead, error) {
	st := &s.offer
	r := &screenRead{shot: s.tlog.ShotPath(tag, false)}
	t0 := time.Now()
	lines, err := s.ocr.ReadDump(ctx, r.shot)
	r.ms = time.Since(t0).Milliseconds()
	if err != nil {
		return r, err
	}
	r.lines = lines
	for _, l := range lines {
		if l.Conf >= 0.3 && len(l.Text) >= 3 && len(r.diag) < 40 {
			r.diag = append(r.diag, l.Text)
		}
	}
	st.lastLines = r.diag
	st.lastOCR = lines
	r.keys = screen.OfferLayout(screen.Match(lines, od.cands))
	// A real offer shows 3 cards (4 at most if one is being rerolled); more is a list of names, not a choice.
	if len(r.keys) > 4 {
		r.keys = nil
	}
	for _, k := range r.keys {
		id, _ := strconv.Atoi(k.Key)
		r.ids = append(r.ids, id)
	}
	return r, nil
}

func (r *screenRead) names(od *offerData) []string {
	out := make([]string, 0, len(r.ids))
	for _, id := range r.ids {
		out = append(out, od.byID[id].Name)
	}
	return out
}

// offerTTL is how long a read offer stays up without a 1/2/3 mark; then the
// recommended augment is recorded (a later 1/2/3 still corrects it).
const offerTTL = 90 * time.Second

// detectOffer reports the augment pick state of the running game. Nothing is
// captured here: the screen is read only when the player presses the scan
// hotkey (scanNow). A read offer stays until the player marks the pick with
// 1/2/3, the next pick level is reached, or offerTTL passes; then the pick is
// recorded (the recommended one unless marked).
func (s *Server) detectOffer(ctx context.Context, live *liveResponse) (*augmentOffer, string) {
	st := &s.offer
	st.mu.Lock()
	defer st.mu.Unlock()
	if off, status, _, stop := s.syncGame(live); stop {
		return off, status
	}
	// A scan handed the cards to the AI: show its answer as soon as it is in.
	if off := s.takeManualAI(ctx, live); off != nil {
		return off, "available"
	}
	if st.last != nil && st.last.Active {
		if time.Since(st.last.ScannedAt) > offerTTL {
			s.closeOffer()
		} else {
			s.attachReview(st.last)
			return st.last, "available"
		}
	}
	if st.pending == 0 {
		return nil, "available"
	}
	return &augmentOffer{Pending: true, Level: st.pending, Stage: stageOf(st.pending)}, "available"
}

// showOffer ranks the cards read on screen and makes them the current offer.
// fresh = a new choice (not a re-read of the one shown): the hotkey mark is
// reset, the AI review starts and the offer is logged. Caller holds s.offer.mu.
func (s *Server) showOffer(ctx context.Context, live *liveResponse, od *offerData, ids []int, via string, fresh bool, trigger string) *augmentOffer {
	st := &s.offer
	me := live.Me
	stage := stageOf(st.pending)
	sig := fmt.Sprint(ids)
	var offered []builds.Augment
	for _, id := range ids {
		if a, ok := od.byID[id]; ok {
			offered = append(offered, a)
		}
	}
	off := &augmentOffer{Active: true, Level: st.pending, Stage: stage, ScannedAt: time.Now(), Lines: st.lastLines, Chosen: st.chosen, Via: via, sig: sig}
	for _, a := range offered {
		off.Screen = append(off.Screen, a.ID)
	}
	off.Offered, off.Source = s.rankMayhem(ctx, me, stage, offered, od.ak, st.picks)
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
	if fresh {
		st.chosen = 0
		off.Chosen = 0
		if s.aiOn("review") {
			s.startReview(sig, off, live)
		}
		cards := make([]map[string]any, 0, len(off.Offered))
		for _, c := range off.Offered {
			cards = append(cards, map[string]any{"id": c.ID, "name": c.Name, "score": round4(c.Score), "stageWinRate": c.StageWR, "stageGames": c.StageGames,
				"lift": round4(c.Lift), "pairWinRate": c.PairWR, "pairWith": c.PairWith, "fit": round4(c.Fit), "fitOk": c.FitOK, "reasons": c.Reasons})
		}
		s.tlog.Log("offer", map[string]any{"level": off.Level, "stage": stage, "trigger": trigger, "via": via, "champion": me.Champion.Name, "items": me.Items, "picks": pickNames(st.picks),
			"source": off.Source, "cards": cards, "best": off.Best, "note": off.Note, "shot": s.tlog.ShotPath("offer", true)})
		s.log.Info("augment offer detected", "level", off.Level, "offered", len(off.Offered), "best", off.Best, "trigger", trigger)
	}
	s.attachReview(off)
	st.last = off
	return off
}

// closeOffer records the pick for the current offer and ends it. Caller holds s.offer.mu.
func (s *Server) closeOffer() {
	st := &s.offer
	s.recordPick(st.last, st.champName)
	if st.seenLevel != nil {
		st.seenLevel[st.pending] = true
	}
	st.pending = 0
	st.last = nil
}

// manualLevel is the pick a scan hotkey press is for when none is due: the
// latest threshold reached (re-reading it replaces that stage's record).
func manualLevel(level int) int {
	lv := pickLevels[0]
	for _, l := range pickLevels {
		if level >= l {
			lv = l
		}
	}
	return lv
}

// scanNow: POST /api/game/scan — the player pressed the "read the augments
// now" hotkey (or the button in the window). This is the only time the
// screen is captured. One capture, and two or more names on screen count
// straight away: the player says the choice is open. When the names cannot
// be matched and AI option C is on, the text goes to the AI and its answer
// becomes the offer (see takeManualAI).
func (s *Server) scanNow(w http.ResponseWriter, r *http.Request) {
	live, err := s.liveState(r.Context())
	if err != nil {
		writeErr(w, 502, "读不到游戏数据："+err.Error())
		return
	}
	if !live.InGame {
		writeErr(w, 409, "现在不在游戏里")
		return
	}
	st := &s.offer
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, _, why, stop := s.syncGame(live); stop {
		writeErr(w, 409, why)
		return
	}
	if st.pending == 0 {
		st.pending, st.pendingAt = manualLevel(live.Me.Level), time.Now()
	}
	stage := stageOf(st.pending)
	od, err := s.loadOfferData(r.Context(), live)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	rd, err := s.readScreen(r.Context(), od, fmt.Sprintf("lv%d-key", st.pending))
	if err != nil {
		s.tlog.Log("scan", map[string]any{"mode": "aram", "trigger": "manual", "level": st.pending, "stage": stage, "error": err.Error(), "capture": s.ocr.Capture(), "ms": rd.ms, "gameTime": live.GameTime})
		writeErr(w, 409, "截屏识别失败："+err.Error())
		return
	}
	names := rd.names(od)
	ok := len(rd.ids) >= 2
	s.tlog.Log("scan", map[string]any{"mode": "aram", "trigger": "manual", "level": st.pending, "stage": stage, "dead": live.Me.IsDead, "ms": rd.ms, "gameTime": live.GameTime,
		"lines": len(rd.lines), "text": rd.diag, "matched": names, "via": "ocr", "confirmed": ok, "shot": rd.shot, "lang": s.ocr.Lang(), "capture": s.ocr.Capture()})
	if ok {
		sig := fmt.Sprint(rd.ids)
		fresh := st.last == nil || !st.last.Active || st.last.sig != sig
		off := s.showOffer(r.Context(), live, od, rd.ids, "ocr", fresh, "manual")
		writeJSON(w, 200, map[string]any{"status": "ok", "best": off.Best, "names": names, "ms": rd.ms})
		return
	}
	// Fewer than two names: let the AI read the text, if allowed.
	if s.aiOn("text") {
		if st.aiRunning || s.startTextRead(rd.lines, od.names, "manual") {
			st.aiManualAt = st.aiAt
			writeJSON(w, 202, map[string]any{"status": "ai", "names": names, "message": fmt.Sprintf("只认出 %d 个强化名，AI 正在根据文字推断（几秒）…", len(names))})
			return
		}
	}
	msg := "屏幕中间没读到强化卡片：请在强化选择界面打开时再按"
	if len(names) == 1 {
		msg = fmt.Sprintf("只认出「%s」一个强化，请等卡片完全显示后再按一次", names[0])
	}
	writeErr(w, 409, msg)
}

// takeManualAI turns the AI's reading of a manual scan into the offer once
// the answer is in. Caller holds s.offer.mu.
func (s *Server) takeManualAI(ctx context.Context, live *liveResponse) *augmentOffer {
	st := &s.offer
	if st.aiManualAt.IsZero() || st.aiRunning {
		return nil
	}
	if !st.aiDone.After(st.aiManualAt) {
		if time.Since(st.aiManualAt) > 30*time.Second {
			st.aiManualAt = time.Time{}
		}
		return nil
	}
	st.aiManualAt = time.Time{}
	if st.pending == 0 || !st.aiChoosing || len(st.aiIDs) < 2 || len(st.aiIDs) > 4 {
		return nil
	}
	od, err := s.loadOfferData(ctx, live)
	if err != nil {
		return nil
	}
	ids := append([]int(nil), st.aiIDs...)
	sig := fmt.Sprint(ids)
	fresh := st.last == nil || !st.last.Active || st.last.sig != sig
	return s.showOffer(ctx, live, od, ids, "ai", fresh, "manual")
}

// recordPick stores the pick for a finished offer: the slot marked with a
// hotkey, else (plan A) the recommended one. A stage read twice (a manual
// re-scan) replaces its earlier record. Caller holds the lock.
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
	replaced := false
	for i := range s.offer.picks {
		if s.offer.picks[i].Stage == p.Stage {
			s.offer.picks[i], replaced = p, true
		}
	}
	if !replaced {
		s.offer.picks = append(s.offer.picks, p)
	}
	s.offer.chosen = 0
	s.tlog.Log("pick", map[string]any{"stage": p.Stage, "level": p.Level, "augment": p.Name, "augmentId": p.AugmentID, "assumed": assumed, "replaced": replaced, "champion": champ})
}

// pickSlot marks which of the on-screen augments (1..3, left to right) the
// player took. During the offer it records the pick right away and closes the
// offer (no waiting for the cards to disappear); afterwards it corrects the
// latest recorded pick. Used by hotkeys.
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
		name := ""
		for _, c := range st.last.Offered {
			if c.ID == st.last.Screen[body.Slot-1] {
				name = c.Name
			}
		}
		st.chosen = body.Slot
		s.tlog.Log("pick-slot", map[string]any{"slot": body.Slot, "augment": name, "during": "offer"})
		s.closeOffer()
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
	// All-champion augment details (pairs) for the picked and offered augments.
	details := map[int]*builds.AKAugmentDetail{}
	if len(pickedIDs) > 0 {
		for _, id := range pickedIDs {
			if d, err := s.community.AramkitAugmentDetail(ctx, id); err == nil {
				details[id] = d
			}
		}
		for _, a := range offered {
			if d, err := s.community.AramkitAugmentDetail(ctx, a.ID); err == nil {
				details[a.ID] = d
			}
		}
	}
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
		} else if len(pickedIDs) > 0 {
			// This champion's data only lists its top 10 pairs; fall back to the
			// all-champion pairs, at a lower weight, compared with the augment's
			// own all-champion win rate.
			if p, partner, ok := builds.GlobalPair(details, a.ID, pickedIDs); ok {
				if cd := details[a.ID]; cd != nil && cd.WinRate > 0 {
					lift := builds.Shrink(p.WinRate, p.Games, cd.WinRate) - cd.WinRate
					c.Score += lift / 4
					c.PairWR, c.PairGames, c.PairWith, c.PairGlobal = p.WinRate, p.Games, names[partner], true
					c.Reasons = append(c.Reasons, fmt.Sprintf("全英雄数据：和已选「%s」一起选胜率 %s，比它平均 %s（%s）", c.PairWith, pct(p.WinRate), signedPct(p.WinRate-cd.WinRate), games(p.Games)))
				}
			}
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
			plan := builds.PlanAugBuild(d, ak, abs, []int{a.ID}, me.Items)
			if len(plan.Cores) > 0 {
				core := plan.Cores[0]
				c.Core = &core
				line := "推荐核心：" + styleLabel(core.Style) + " · "
				for i, it := range core.Items {
					if i > 0 {
						line += " → "
					}
					line += it.Name
				}
				c.Reasons = append(c.Reasons, line+fmt.Sprintf("（胜率 %s，%s）", pct(core.WinRate), games(core.Games)))
			}
			// Which build style goes with this augment, and whether your items
			// point the other way. A conflict is shown right after the win rate.
			if dir := plan.Direction; dir != nil && dir.Note != "" {
				if dir.Conflict {
					c.Reasons = append(c.Reasons[:1], append([]string{"注意：" + dir.Note}, c.Reasons[1:]...)...)
				} else {
					c.Reasons = append(c.Reasons, "路线："+dir.Note)
				}
			}
		}
		cards = append(cards, c)
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Score > cards[j].Score })
	return cards, "aramkit"
}

// correctPick replaces the assumed augment of a stage with the one actually
// taken, or removes the record when augmentId is 0.
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
		if body.AugmentID == 0 { // remove a wrong record
			s.tlog.Log("pick-removed", map[string]any{"stage": p.Stage, "augment": p.Name})
			st.picks = append(st.picks[:i], st.picks[i+1:]...)
			writeJSON(w, 200, st.picks)
			return
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

// addCandidates registers an augment's names for on-screen matching: SEA
// Simplified Chinese (what the client shows), China-server Chinese, English.
func addCandidates(cands map[string]string, a builds.Augment) {
	for _, name := range []string{a.Name, a.NameCN, a.NameEN} {
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

// attachReview puts the current AI review of an offer on it (a copy, so the
// response can be encoded after the lock is released). Caller holds s.offer.mu.
func (s *Server) attachReview(off *augmentOffer) {
	if off == nil || s.offer.reviews == nil {
		return
	}
	if v := s.offer.reviews[off.sig]; v != nil {
		cp := *v
		off.AI = &cp
	}
}
