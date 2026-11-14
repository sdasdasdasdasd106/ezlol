package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/lcu"
)

// Champion select bench advice (ARAM / ARAM Mayhem): your champion and every
// champion on the bench, ordered by ARAM Mayhem win rate, with one line saying
// whether to swap, keep or reroll. Read-only: the player swaps or rerolls in
// the client themselves; ezlol never calls the client's write endpoints.

const (
	// benchSwapMargin is how much higher (win rate, 0..1) a bench champion must
	// be before we say "swap": 1.5 percentage points. Smaller gaps are within
	// what patch-to-patch noise and the player's own skill on a champion move.
	benchSwapMargin = 0.015
	// benchMinGames is the sample below which a champion's win rate is not
	// trusted: it is shown marked "数据少" and never recommended. At 2000 games
	// the standard error is about 1.1 points, already close to the margin.
	benchMinGames = 2000
	// benchFallbackAverage is the average champion win rate when the data set
	// cannot give one (op.gg, fetched per champion).
	benchFallbackAverage = 0.5
	// A champion you have "hardly played": mastery points below this and fewer
	// than benchNewGames recent ARAM games on it.
	benchNewPoints = 5000
	benchNewGames  = 3
	// A champion you "know well": at least this many mastery points.
	benchKnownPoints = 50000
)

// benchRow is one champion in the list.
type benchRow struct {
	Champion  ddragon.Champion `json:"champion"`
	Current   bool             `json:"current"`
	HasData   bool             `json:"hasData"`
	WinRate   float64          `json:"winRate"`
	Games     int              `json:"games"`
	LowSample bool             `json:"lowSample"` // no data or too few games: not recommended
	Mastery   *lcu.Mastery     `json:"mastery,omitempty"`
	Record    *Record          `json:"record,omitempty"`
}

// benchAdvice is GET /api/champ-select/bench.
type benchAdvice struct {
	Active       bool       `json:"active"` // in champion select with a bench
	BenchEnabled bool       `json:"benchEnabled"`
	Rows         []benchRow `json:"rows"`
	// Kind: swap | keep | reroll | empty | unsure, "" when there is nothing to say.
	Kind    string  `json:"kind"`
	Verdict string  `json:"verdict"`
	Note    string  `json:"note,omitempty"`   // familiarity reminder on a swap
	Target  int     `json:"target,omitempty"` // champion to swap to
	Source  string  `json:"source"`           // aramkit | opgg | ""
	Patch   string  `json:"patch,omitempty"`
	Average float64 `json:"average"`
	Rerolls int     `json:"rerolls"` // -1 unknown
}

// benchInput is what adviseBench decides from.
type benchInput struct {
	Current benchRow // Champion.ID 0 when you have none yet
	Bench   []benchRow
	Average float64 // average champion win rate, 0..1
	Rerolls int     // -1 unknown
}

func pct1(x float64) string { return fmt.Sprintf("%.1f%%", x*100) }

func (r benchRow) reliable() bool { return r.HasData && r.Games >= benchMinGames }

func (r benchRow) points() int {
	if r.Mastery == nil {
		return 0
	}
	return r.Mastery.ChampionPoints
}

func (r benchRow) recent() int {
	if r.Record == nil {
		return 0
	}
	return r.Record.Games
}

// adviseBench orders your champion and the bench by win rate and picks the
// conclusion. Pure: everything it needs is in the input.
func adviseBench(in benchInput) benchAdvice {
	out := benchAdvice{Average: in.Average, Rerolls: in.Rerolls}
	cur := in.Current
	cur.Current = true
	var rows []benchRow
	if cur.Champion.ID != 0 {
		rows = append(rows, cur)
	}
	for _, b := range in.Bench {
		if b.Champion.ID == 0 || b.Champion.ID == cur.Champion.ID {
			continue
		}
		b.Current = false
		rows = append(rows, b)
	}
	anyData := false
	for i := range rows {
		rows[i].LowSample = !rows[i].reliable()
		anyData = anyData || rows[i].HasData
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.HasData != b.HasData {
			return a.HasData
		}
		if a.WinRate != b.WinRate {
			return a.WinRate > b.WinRate
		}
		return a.Games > b.Games
	})
	out.Rows = rows
	if cur.Champion.ID == 0 || !anyData {
		return out
	}

	// The best bench champion we trust.
	var best *benchRow
	benchCount := 0
	for i := range rows {
		if rows[i].Current {
			continue
		}
		benchCount++
		if best == nil && rows[i].reliable() {
			best = &rows[i]
		}
	}
	name := cur.Champion.Name

	if !cur.reliable() {
		out.Kind = "unsure"
		out.Verdict = name + " 数据太少，没法比较"
		if best != nil {
			out.Verdict += fmt.Sprintf("；板凳上胜率最高的是 %s（%s）", best.Champion.Name, pct1(best.WinRate))
		}
		return out
	}
	if best != nil && best.WinRate-cur.WinRate >= benchSwapMargin {
		out.Kind = "swap"
		out.Target = best.Champion.ID
		out.Verdict = fmt.Sprintf("建议换成 %s：胜率 %s，比你现在的 %s 高 %.1f 个百分点", best.Champion.Name, pct1(best.WinRate), name, (best.WinRate-cur.WinRate)*100)
		if best.points() < benchNewPoints && best.recent() < benchNewGames && cur.points() >= benchKnownPoints {
			out.Note = fmt.Sprintf("不过你没怎么玩过 %s，%s 你更熟", best.Champion.Name, name)
		}
		return out
	}
	if in.Rerolls > 0 && cur.WinRate < in.Average {
		out.Kind = "reroll"
		out.Verdict = fmt.Sprintf("可以骰一次（还剩 %d 次）：%s 胜率 %s，低于平均 %s", in.Rerolls, name, pct1(cur.WinRate), pct1(in.Average))
		if benchCount == 0 {
			out.Verdict = "板凳席还是空的，" + out.Verdict
		} else {
			out.Verdict += "，板凳上也没有更好的"
		}
		return out
	}
	if benchCount == 0 {
		out.Kind = "empty"
		out.Verdict = fmt.Sprintf("板凳席还是空的，%s 胜率 %s", name, pct1(cur.WinRate))
		return out
	}
	out.Kind = "keep"
	out.Verdict = "留着 " + name + " 就行，板凳上没有明显更好的"
	if best != nil && best.WinRate > cur.WinRate {
		out.Verdict += fmt.Sprintf("（%s 只高 %.1f 个百分点）", best.Champion.Name, (best.WinRate-cur.WinRate)*100)
	}
	return out
}

// champSelectBench serves the bench advice. The UI asks again whenever the
// status event says the bench, your champion or the rerolls changed.
func (s *Server) champSelectBench(w http.ResponseWriter, r *http.Request) {
	st := s.watcher.Status()
	cs := st.ChampSelect
	d := s.dd.Data()
	if st.Phase != "ChampSelect" || cs == nil || !cs.BenchEnabled || d == nil {
		writeJSON(w, 200, benchAdvice{Rerolls: -1, Rows: []benchRow{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	champ := func(id int) ddragon.Champion {
		if ch, ok := d.Champions[id]; ok {
			return ch
		}
		return ddragon.Champion{ID: id, Name: fmt.Sprintf("英雄 #%d", id)}
	}
	ids := []int{}
	if st.PickedChampion != 0 {
		ids = append(ids, st.PickedChampion)
	}
	ids = append(ids, cs.Bench...)
	rates, source, patch, avg := s.benchRates(ctx, ids)

	if c := s.watcher.Client(); c != nil {
		s.player.refresh(ctx, c)
	}
	row := func(id int) benchRow {
		b := benchRow{Champion: champ(id)}
		if rt, ok := rates[id]; ok {
			b.HasData, b.WinRate, b.Games = true, rt.WinRate, rt.Games
		}
		b.Mastery, b.Record = s.player.lookup("aram", id)
		return b
	}
	in := benchInput{Average: avg, Rerolls: cs.Rerolls}
	if st.PickedChampion != 0 {
		in.Current = row(st.PickedChampion)
	}
	for _, id := range cs.Bench {
		in.Bench = append(in.Bench, row(id))
	}
	out := adviseBench(in)
	out.Active, out.BenchEnabled = true, true
	if len(rates) > 0 {
		out.Source, out.Patch = source, patch
	}
	if out.Rows == nil {
		out.Rows = []benchRow{}
	}
	writeJSON(w, 200, out)
}

// benchRates returns the champions' win rates: aramkit's ARAM Mayhem ranking
// (one request, all champions, and the average from it), else op.gg's normal
// ARAM numbers fetched per champion in parallel (average assumed 50%).
func (s *Server) benchRates(ctx context.Context, ids []int) (map[int]builds.ChampRate, string, string, float64) {
	all, ver, err := s.community.AramkitChampionRates(ctx)
	if err == nil {
		sum := 0.0
		for _, r := range all {
			sum += r.WinRate
		}
		return all, "aramkit", ver, sum / float64(len(all))
	}
	s.log.Warn("aramkit champion rates", "err", err)
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := map[int]builds.ChampRate{}
	patch := ""
	for _, id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r, p, err := s.community.ARAMChampionRate(ctx, id)
			if err != nil || r.Games == 0 {
				return
			}
			mu.Lock()
			out[id] = r
			if patch == "" {
				patch = p
			}
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out, "opgg", patch, benchFallbackAverage
}
