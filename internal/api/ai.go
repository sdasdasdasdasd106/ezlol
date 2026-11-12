package api

// AI second opinion (DeepSeek). Everything runs in the background and never
// holds up the statistics' own recommendation:
//   A. review: when an augment choice is confirmed, ask for a second opinion;
//   B. research: when the champion is known, search the web once per champion
//      and patch and write a strategy card (also fed to A);
//   C. text: when OCR reads text but cannot match the card names (misread
//      characters), send the OCR text to the model and let it work them out.
//      Text only, no images.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aarlint/ezlol/internal/ai"
	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/screen"
)

// aiReviewView is the review shown with an offer.
type aiReviewView struct {
	Status     string  `json:"status"` // pending | done | error
	Pick       string  `json:"pick,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Agree      bool    `json:"agree"`
	Confidence string  `json:"confidence,omitempty"`
	Error      string  `json:"error,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
}

// researchView is the strategy card for the champion being played.
type researchView struct {
	Status   string       `json:"status"` // none (not researched yet) | pending | done | error
	Strategy *ai.Strategy `json:"strategy,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type aiState struct {
	mu       sync.Mutex
	research map[string]*researchView // champion id + data version
	running  map[string]bool
	byChamp  map[int]string // champion id -> research key
	failedAt map[string]time.Time
}

func (s *Server) aiOn(feature string) bool {
	if !s.ai.Ready() {
		return false
	}
	cfg := s.settings.Get()
	switch feature {
	case "review":
		return cfg.AIReview
	case "research":
		return cfg.AIResearch
	case "text":
		return cfg.AIText
	}
	return false
}

// newAIClient builds the DeepSeek client and keeps the month's spending on disk.
func newAIClient(dataDir string, key func() string) *ai.Client {
	u := &ai.Usage{}
	path := filepath.Join(dataDir, "ai", "usage.json")
	if b, err := os.ReadFile(path); err == nil {
		var t ai.Totals
		if json.Unmarshal(b, &t) == nil {
			u.Restore(t)
		}
	}
	u.OnChange = func(t ai.Totals) {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if b, err := json.Marshal(t); err == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}
	return &ai.Client{Key: key, Usage: u}
}

// ---- B: research ----

// research returns the strategy card for a champion (nil = not enabled). With
// start it begins the research in the background if it is not known yet;
// without, it only answers from the cache (status none), so browsing
// champions in the lobby costs nothing. A failed research is retried after
// two minutes.
func (s *Server) research(champID int, start bool) *researchView {
	if champID == 0 || !s.aiOn("research") {
		return nil
	}
	// Polled every second in game: answer from memory when possible.
	st := &s.aiSt
	st.mu.Lock()
	if key, ok := st.byChamp[champID]; ok {
		if v := st.research[key]; v != nil && v.Status == "error" && time.Since(st.failedAt[key]) > 2*time.Minute {
			delete(st.research, key)
		}
		if v := st.research[key]; v != nil {
			cp := *v
			st.mu.Unlock()
			return &cp
		}
		if st.running[key] {
			st.mu.Unlock()
			return &researchView{Status: "pending"}
		}
	}
	st.mu.Unlock()
	ak, err := s.community.AramkitChampion(context.Background(), champID)
	if err != nil {
		return &researchView{Status: "error", Error: "拿不到这个英雄的统计数据"}
	}
	key := fmt.Sprintf("%d-%s", champID, ak.Version)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.research == nil {
		st.research, st.running, st.byChamp, st.failedAt = map[string]*researchView{}, map[string]bool{}, map[int]string{}, map[string]time.Time{}
	}
	st.byChamp[champID] = key
	if v := st.research[key]; v != nil {
		cp := *v
		return &cp
	}
	// On disk from an earlier session?
	path := filepath.Join(s.dataDir, "ai", "research-"+key+".json")
	if b, err := os.ReadFile(path); err == nil {
		var sg ai.Strategy
		if json.Unmarshal(b, &sg) == nil && time.Since(sg.At) < 3*24*time.Hour {
			st.research[key] = &researchView{Status: "done", Strategy: &sg}
			return &researchView{Status: "done", Strategy: &sg}
		}
	}
	if !start {
		return &researchView{Status: "none"}
	}
	if !st.running[key] {
		st.running[key] = true
		go s.runResearch(champID, ak, key, path)
	}
	return &researchView{Status: "pending"}
}

func (s *Server) runResearch(champID int, ak *builds.AKChampion, key, path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t0 := time.Now()
	in := ai.ResearchInput{Patch: ak.Version, WinRate: ak.WinRate}
	if d := s.dd.Data(); d != nil {
		c := d.Champions[champID]
		in.Champion, in.ChampionEN = c.Name, c.Key
		if b, err := s.community.AramkitBuild(ctx, d, c); err == nil {
			for i, set := range b.Core {
				names := make([]string, 0, len(set.Items))
				for _, it := range set.Items {
					names = append(names, it.Name)
				}
				style := ""
				if i < len(b.CoreTags) {
					style = builds.StyleName(b.CoreTags[i]) + "："
				}
				in.Cores = append(in.Cores, fmt.Sprintf("%s%s（胜率 %.1f%%，%d 场）", style, strings.Join(names, " → "), 100*float64(set.Wins)/float64(max(set.Games, 1)), set.Games))
			}
		}
	}
	if augs, err := s.community.AramkitAugments(ctx, s.watcher.Status().Patch, champID); err == nil {
		// The best-known 40 by win rate with a real sample.
		sort.SliceStable(augs, func(i, j int) bool {
			return builds.Shrink(augs[i].WinRate, augs[i].Games, ak.WinRate) > builds.Shrink(augs[j].WinRate, augs[j].Games, ak.WinRate)
		})
		for _, a := range augs {
			if len(in.Augments) >= 40 {
				break
			}
			if a.Games >= 200 {
				in.Augments = append(in.Augments, ai.AugStat{Name: a.Name, Rarity: rarityCN(a.Rarity), WinRate: a.WinRate, Games: a.Games})
			}
		}
	}
	sg, err := s.ai.Research(ctx, in)
	v := &researchView{Status: "done", Strategy: sg}
	if err != nil {
		v = &researchView{Status: "error", Error: err.Error()}
	} else {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if b, err := json.Marshal(sg); err == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}
	s.tlog.Log("ai-research", map[string]any{"champion": in.Champion, "patch": in.Patch, "ok": err == nil, "error": errString(err), "seconds": time.Since(t0).Seconds(),
		"summary": strategySummary(sg)})
	st := &s.aiSt
	st.mu.Lock()
	st.research[key] = v
	st.running[key] = false
	if err != nil {
		st.failedAt[key] = time.Now()
	}
	st.mu.Unlock()
}

func strategySummary(sg *ai.Strategy) string {
	if sg == nil {
		return ""
	}
	return sg.Text()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func rarityCN(r string) string {
	switch r {
	case "prismatic":
		return "棱彩"
	case "gold":
		return "金色"
	case "silver":
		return "银色"
	}
	return r
}

// aiResearch: GET /api/ai/research?champion=ID (the main window, outside games).
func (s *Server) aiResearch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("champion"))
	v := s.research(id, r.URL.Query().Get("start") == "1")
	if v == nil {
		writeJSON(w, 200, map[string]any{"status": "off"})
		return
	}
	writeJSON(w, 200, v)
}

// ---- A: review ----

// startReview asks for a second opinion on a confirmed offer (caller holds s.offer.mu).
func (s *Server) startReview(sig string, off *augmentOffer, live *liveResponse) {
	st := &s.offer
	if st.reviews == nil {
		st.reviews = map[string]*aiReviewView{}
	}
	if st.reviews[sig] != nil {
		return
	}
	st.reviews[sig] = &aiReviewView{Status: "pending"}
	in := s.reviewInput(off, live)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rv, err := s.ai.ReviewChoice(ctx, in)
		v := &aiReviewView{Status: "done"}
		if err != nil {
			v = &aiReviewView{Status: "error", Error: err.Error()}
		} else {
			v.Pick, v.Reason, v.Agree, v.Confidence, v.Seconds = rv.Pick, rv.Reason, rv.Agree, rv.Confidence, rv.Seconds
		}
		s.tlog.Log("ai-review", map[string]any{"stage": in.Stage, "champion": in.Champion, "dataPick": in.DataPick, "pick": v.Pick, "agree": v.Agree,
			"reason": v.Reason, "confidence": v.Confidence, "error": v.Error, "seconds": v.Seconds})
		st.mu.Lock()
		st.reviews[sig] = v
		st.mu.Unlock()
	}()
}

func (s *Server) reviewInput(off *augmentOffer, live *liveResponse) ai.ReviewInput {
	d := s.dd.Data()
	itemNames := func(ids []int) []string {
		var out []string
		for _, id := range ids {
			if d == nil {
				break
			}
			it, ok := d.Items[id]
			// Finished items only: components and consumables add noise.
			if ok && it.Gold >= 2000 {
				out = append(out, it.Name)
			}
		}
		return out
	}
	stage := map[int]string{1: "开局", 2: "7 级", 3: "11 级", 4: "15 级"}[off.Stage]
	in := ai.ReviewInput{Stage: stage, Level: off.Level, GameMinute: int(live.GameTime / 60), DataPick: off.Best}
	var myTeam string
	if live.Me != nil {
		in.Champion, in.MyItems, myTeam = live.Me.Champion.Name, itemNames(live.Me.Items), live.Me.Team
	}
	for _, p := range live.Players {
		pl := ai.Player{Champion: p.Champion.Name, Items: itemNames(p.Items)}
		if p.IsMe {
			continue
		}
		if p.Team == myTeam {
			in.Allies = append(in.Allies, pl)
		} else {
			in.Enemies = append(in.Enemies, pl)
		}
	}
	for _, p := range s.offer.picks {
		in.Picked = append(in.Picked, p.Name)
	}
	for _, c := range off.Offered {
		in.Cards = append(in.Cards, ai.Card{Name: c.Name, Rarity: rarityCN(c.Rarity), Desc: c.Desc, Reasons: c.Reasons})
	}
	if live.Me != nil {
		if rv := s.cachedResearch(live.Me.Champion.ID); rv != nil && rv.Strategy != nil {
			in.Strategy = rv.Strategy.Text()
		}
	}
	return in
}

// cachedResearch returns a finished strategy card without starting research.
func (s *Server) cachedResearch(champID int) *researchView {
	st := &s.aiSt
	st.mu.Lock()
	defer st.mu.Unlock()
	prefix := strconv.Itoa(champID) + "-"
	for k, v := range st.research {
		if strings.HasPrefix(k, prefix) && v.Status == "done" {
			return v
		}
	}
	return nil
}

// ---- C: work out the cards from garbled OCR text ----

// hanCount counts Chinese characters.
func hanCount(t string) int {
	n := 0
	for _, r := range t {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

// bandLines keeps the OCR lines in the middle of the screen, where the cards are.
func bandLines(lines []screen.Line) []ai.OCRLine {
	var out []ai.OCRLine
	for _, l := range lines {
		if l.Conf < 0.3 || l.Y < 0.12 || l.Y > 0.9 || hanCount(l.Text) < 2 || len([]rune(l.Text)) > 60 {
			continue
		}
		out = append(out, ai.OCRLine{Text: strings.TrimSpace(l.Text), X: l.X + l.W/2, Y: l.Y})
		if len(out) == 40 {
			break
		}
	}
	return out
}

// titleLike: short lines that could be a card title (names are 2-8 characters).
func titleLike(t string) bool {
	h := hanCount(t)
	return h >= 2 && len([]rune(screen.Normalize(t))) <= 12
}

// worthAsking: enough Chinese text in the card area that a choice may be shown.
func worthAsking(band []ai.OCRLine) bool {
	titles := 0
	for _, l := range band {
		if titleLike(l.Text) {
			titles++
		}
	}
	return titles >= 2 && len(band) >= 3
}

// overlap scores how many characters a name shares with an OCR line (0..1).
func overlap(line, name string) float64 {
	have := map[rune]int{}
	for _, r := range line {
		have[r]++
	}
	n, shared := 0, 0
	for _, r := range name {
		n++
		if have[r] > 0 {
			have[r]--
			shared++
		}
	}
	l := len([]rune(line))
	if l > n {
		n = l
	}
	if n == 0 {
		return 0
	}
	return float64(shared) / float64(n)
}

// shortlist picks the augment names that look like the title lines, so the
// prompt stays small (a few hundred tokens).
func shortlist(band []ai.OCRLine, names map[string]int) []string {
	keep := map[string]bool{}
	for _, l := range band {
		t := screen.Normalize(l.Text)
		if !titleLike(t) {
			continue
		}
		type sc struct {
			name string
			v    float64
		}
		var top []sc
		for n := range names {
			if v := overlap(t, screen.Normalize(n)); v >= 0.34 {
				top = append(top, sc{n, v})
			}
		}
		sort.Slice(top, func(i, j int) bool {
			if top[i].v != top[j].v {
				return top[i].v > top[j].v
			}
			return top[i].name < top[j].name
		})
		for i := 0; i < len(top) && i < 6; i++ {
			keep[top[i].name] = true
		}
	}
	out := make([]string, 0, len(keep))
	for n := range keep {
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

// startTextRead sends a scan's text to the model (caller holds s.offer.mu).
// The answer is picked up by takeManualAI on the next game poll.
func (s *Server) startTextRead(lines []screen.Line, names map[string]int, why string) bool {
	st := &s.offer
	if st.aiRunning {
		return false
	}
	band := bandLines(lines)
	if !worthAsking(band) {
		return false
	}
	cands := shortlist(band, names)
	if len(cands) < 2 {
		return false
	}
	st.aiRunning = true
	st.aiAt = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		t0 := time.Now()
		r, err := s.ai.ReadOCR(ctx, band, cands)
		var ids []int
		var got []string
		choosing := false
		if err == nil && r.Choosing {
			choosing = true
			got = r.Names
			seen := map[int]bool{}
			for _, n := range r.Names {
				if id, ok := names[strings.TrimSpace(n)]; ok && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		s.tlog.Log("ai-text", map[string]any{"why": why, "lines": len(band), "candidates": len(cands), "choosing": choosing, "names": got, "matched": len(ids), "error": errString(err), "seconds": time.Since(t0).Seconds()})
		st.mu.Lock()
		st.aiRunning = false
		st.aiDone = time.Now()
		st.aiChoosing = choosing
		st.aiIDs = ids
		st.mu.Unlock()
	}()
	return true
}
