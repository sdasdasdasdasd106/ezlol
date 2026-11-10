package api

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/screen"
)

// augmentOffer is the current augment selection screen, when detected.
type augmentOffer struct {
	Active    bool             `json:"active"`
	Pending   bool             `json:"pending"` // a pick is due (level threshold crossed) but not yet seen on screen
	Level     int              `json:"level"`
	Offered   []builds.Augment `json:"offered"` // ranked best first
	Best      string           `json:"best,omitempty"`
	Why       string           `json:"why,omitempty"`
	ScannedAt time.Time        `json:"scannedAt"`
	Lines     []string         `json:"lines,omitempty"` // diagnostics: what OCR last read
}

// Mayhem grants augment picks at game start and at these champion levels.
var pickLevels = []int{1, 7, 11, 15}

type offerState struct {
	mu        sync.Mutex
	gameStart time.Time
	seenLevel map[int]bool // levels whose pick has been completed (offer seen then gone)
	pending   int          // level with a pick outstanding, 0 = none
	pendingAt time.Time
	last      *augmentOffer
	lastScan  time.Time
	lastGT    float64
	lastLines []string
	picks     int // Arena: completed picks this game
}

// detectOffer decides whether an augment pick is likely on screen and, if so,
// OCRs the display to identify the three offered augments and rank them.
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
	tag := builds.QueueTag(s.watcher.Status().QueueID)
	if tag == "arena" {
		return s.detectArenaOffer(ctx, live)
	}
	if tag != "aram" {
		return nil, "available"
	}
	// New game: reset.
	fresh := live.GameTime < st.lastGT-30 || st.seenLevel == nil
	if fresh {
		st.seenLevel = map[int]bool{}
		st.pending = 0
		st.last = nil
	}
	st.lastGT = live.GameTime
	var me *livePlayer
	for i := range live.Players {
		if live.Players[i].IsMe {
			me = &live.Players[i]
		}
	}
	if me == nil {
		return nil, "available"
	}
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
	// Picks are only shown while dead or at the fountain; scanning while alive
	// mid-fight wastes CPU, but early game and right after level-up we scan anyway.
	shouldScan := me.IsDead || live.GameTime < 120 || time.Since(st.pendingAt) < 45*time.Second || (st.last != nil && st.last.Active)
	if !shouldScan || time.Since(st.lastScan) < 1500*time.Millisecond {
		if st.last != nil && st.last.Active && time.Since(st.lastScan) < 6*time.Second {
			return st.last, "available"
		}
		return &augmentOffer{Pending: true, Level: st.pending, Lines: st.lastLines}, "available"
	}
	st.lastScan = time.Now()
	lines, err := s.ocr.Read(ctx)
	if err != nil {
		return &augmentOffer{Pending: true, Level: st.pending}, err.Error()
	}
	patch := s.watcher.Status().Patch
	augs, _, err := s.community.Augments(ctx, patch, me.Champion.ID)
	if err != nil {
		return &augmentOffer{Pending: true, Level: st.pending}, "available"
	}
	byID := map[int]builds.Augment{}
	cands := map[string]string{}
	for _, a := range augs {
		byID[a.ID] = a
		addCandidates(cands, a)
	}
	var diag []string
	for _, l := range lines {
		if l.Conf >= 0.3 && len(l.Text) >= 3 && len(diag) < 40 {
			diag = append(diag, l.Text)
		}
	}
	st.lastLines = diag
	keys := screen.OfferLayout(screen.Match(lines, cands))
	if len(keys) < 2 {
		// Offer disappeared after being seen: the pick is done.
		if st.last != nil && st.last.Active && time.Since(st.last.ScannedAt) > 4*time.Second {
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
		return &augmentOffer{Pending: st.pending != 0, Level: st.pending, Lines: st.lastLines}, "available"
	}
	off := &augmentOffer{Active: true, Level: st.pending, ScannedAt: time.Now(), Lines: diag}
	for _, k := range keys {
		id, _ := strconv.Atoi(k.Key)
		if a, ok := byID[id]; ok {
			off.Offered = append(off.Offered, a)
		}
	}
	sort.SliceStable(off.Offered, func(i, j int) bool {
		a, b := off.Offered[i], off.Offered[j]
		ta, tb := a.Tier, b.Tier
		if ta == 0 {
			ta = 9
		}
		if tb == 0 {
			tb = 9
		}
		// Prefer win rate when both have a meaningful sample; tier breaks ties.
		if a.Games >= 200 && b.Games >= 200 && a.WinRate != b.WinRate {
			return a.WinRate > b.WinRate
		}
		if ta != tb {
			return ta < tb
		}
		return a.WinRate > b.WinRate
	})
	if len(off.Offered) > 0 {
		b := off.Offered[0]
		off.Best = b.Name
		switch {
		case b.Games >= 200:
			off.Why = me.Champion.Name + " 用它胜率 " + strconv.Itoa(int(b.WinRate*100+0.5)) + "%（" + strconv.Itoa(b.Games) + " 场）"
		case b.Tier > 0:
			off.Why = me.Champion.Name + " 评级 " + tierLetter(b.Tier)
		default:
			off.Why = "数据不足，按评级排序"
		}
	}
	if st.last == nil || !st.last.Active {
		s.log.Info("augment offer detected", "level", off.Level, "offered", len(off.Offered), "best", off.Best)
	}
	st.last = off
	return off, "available"
}

// detectArenaOffer scans continuously (every 3s, 1.5s while an offer is up):
// Arena offers augments between rounds, and nothing in the live data marks it.
func (s *Server) detectArenaOffer(ctx context.Context, live *liveResponse) (*augmentOffer, string) {
	st := &s.offer
	var me *livePlayer
	for i := range live.Players {
		if live.Players[i].IsMe {
			me = &live.Players[i]
		}
	}
	if me == nil {
		return nil, "available"
	}
	interval := 3 * time.Second
	if st.last != nil && st.last.Active {
		interval = 1500 * time.Millisecond
	}
	if time.Since(st.lastScan) < interval {
		if st.last != nil && st.last.Active && time.Since(st.lastScan) < 6*time.Second {
			return st.last, "available"
		}
		return nil, "available"
	}
	st.lastScan = time.Now()
	lines, err := s.ocr.Read(ctx)
	if err != nil {
		return nil, err.Error()
	}
	augs, _, err := s.community.ArenaAugments(ctx, s.watcher.Status().Patch, me.Champion.ID)
	if err != nil {
		return nil, "available"
	}
	byID := map[int]builds.Augment{}
	cands := map[string]string{}
	for _, a := range augs {
		byID[a.ID] = a
		addCandidates(cands, a)
	}
	keys := screen.OfferLayout(screen.Match(lines, cands))
	if len(keys) < 2 {
		if st.last != nil && st.last.Active && time.Since(st.last.ScannedAt) > 4*time.Second {
			st.picks++
			st.last = nil
		}
		return nil, "available"
	}
	off := &augmentOffer{Active: true, Level: st.picks + 1, ScannedAt: time.Now()}
	for _, k := range keys {
		id, _ := strconv.Atoi(k.Key)
		if a, ok := byID[id]; ok {
			off.Offered = append(off.Offered, a)
		}
	}
	// Lower average placement wins; tier breaks ties; then sample size.
	sort.SliceStable(off.Offered, func(i, j int) bool {
		a, b := off.Offered[i], off.Offered[j]
		ap, bp := a.AvgPlace, b.AvgPlace
		if ap == 0 {
			ap = 9
		}
		if bp == 0 {
			bp = 9
		}
		if a.Games >= 100 && b.Games >= 100 && ap != bp {
			return ap < bp
		}
		ta, tb := a.Tier, b.Tier
		if ta == 0 {
			ta = 9
		}
		if tb == 0 {
			tb = 9
		}
		if ta != tb {
			return ta < tb
		}
		return ap < bp
	})
	if len(off.Offered) > 0 {
		b := off.Offered[0]
		off.Best = b.Name
		switch {
		case b.Games >= 100 && b.AvgPlace > 0:
			off.Why = fmt.Sprintf("%s 用它平均名次 %.2f（%d 场）", me.Champion.Name, b.AvgPlace, b.Games)
		case b.Tier > 0:
			off.Why = "全英雄评级 " + tierLetter(b.Tier)
		default:
			off.Why = "数据不足，按评级排序"
		}
	}
	if st.last == nil || !st.last.Active {
		s.log.Info("arena augment offer detected", "pick", off.Level, "offered", len(off.Offered), "best", off.Best)
	}
	st.last = off
	return off, "available"
}

// tierLetter turns Tier 1..5 into S/A/B/C/D.
func tierLetter(t int) string {
	if t >= 1 && t <= 5 {
		return string("SABCD"[t-1])
	}
	return "?"
}

// addCandidates registers an augment's Chinese and English names for on-screen matching.
func addCandidates(cands map[string]string, a builds.Augment) {
	for _, name := range []string{a.Name, a.NameEN} {
		if n := screen.Normalize(name); len(n) >= 4 {
			cands[n] = strconv.Itoa(a.ID)
		}
	}
}
