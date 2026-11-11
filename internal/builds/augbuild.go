package builds

import (
	"sort"
	"strconv"

	"github.com/aarlint/ezlol/internal/ddragon"
)

// shrinkK pulls small samples toward the champion's average: a win rate over
// n games counts as if shrinkK average games were added. 200 games is where
// the sites themselves stop flagging a number as "low sample".
const shrinkK = 200

// Shrink returns a win rate pulled toward base by its sample size.
func Shrink(wr float64, n int, base float64) float64 {
	if n <= 0 {
		return base
	}
	return (wr*float64(n) + base*shrinkK) / float64(n+shrinkK)
}

// StageStat is the augment's record at a pick stage (1 = game start, 2/3/4 =
// level 7/11/15), falling back to its all-stage record.
func (ch *AKChampion) StageStat(stage, augID int) (AKStat, bool) {
	if rows := ch.Stages[itoa(stage)]; rows != nil {
		for _, r := range rows {
			if r.ID == augID {
				return r, true
			}
		}
	}
	for _, r := range ch.Augments {
		if r.ID == augID {
			return r, true
		}
	}
	return AKStat{}, false
}

// BestPair is the best recorded pair of augID with any of the already picked augments.
func (ch *AKChampion) BestPair(augID int, picked []int) (AKPair, int, bool) {
	var best AKPair
	partner, ok := 0, false
	for _, p := range ch.Pairs {
		if len(p.AugmentIDs) != 2 {
			continue
		}
		a, b := p.AugmentIDs[0], p.AugmentIDs[1]
		for _, x := range picked {
			if (a == augID && b == x) || (b == augID && a == x) {
				if !ok || p.WinRate > best.WinRate {
					best, partner, ok = p, x, true
				}
			}
		}
	}
	return best, partner, ok
}

// ItemFit compares how the items you own do in games with this augment versus
// this champion's games in general. Positive = your items fit this augment.
// Only items with enough games on both sides count.
func ItemFit(ch *AKChampion, ab *AKAugmentBuild, owned []int) (delta float64, games int, ok bool) {
	if ch == nil || ab == nil || len(owned) == 0 {
		return 0, 0, false
	}
	base := map[int]AKStat{}
	for _, r := range ch.Items.All {
		base[r.ID] = r
	}
	with := map[int]AKStat{}
	for _, r := range ab.Items.All {
		with[r.ID] = r
	}
	var sum, w float64
	for _, id := range owned {
		a, okA := with[id]
		b, okB := base[id]
		if !okA || !okB || a.Games < 30 || b.Games < 100 {
			continue
		}
		weight := float64(min(a.Games, 2000))
		sum += (Shrink(a.WinRate, a.Games, b.WinRate) - b.WinRate) * weight
		w += weight
		games += a.Games
	}
	if w == 0 || games < 100 {
		return 0, 0, false
	}
	return sum / w, games, true
}

// StyleName is the Chinese label of a build style (aramkit archetype key).
func StyleName(k string) string {
	if n, ok := styleNames[k]; ok {
		return n
	}
	return k
}

var styleNames = map[string]string{"crit": "暴击流", "on_hit": "攻速特效流", "ap": "法强流", "hybrid": "混伤流", "lethality": "穿甲流",
	"tank": "坦克流", "bruiser": "战士流", "ad": "物理流", "utility": "辅助流", "fighter": "战士流", "assassin": "刺客流"}

// minItemGames is the sample an item needs before it is recommended.
const minItemGames = 200

// AugBuild is the item plan for a champion given the augments picked (or being
// considered) and the items already owned.
type AugBuild struct {
	Augments  []int      `json:"augments"`
	Games     int        `json:"games"`     // fewest games among the augments' samples
	Slot      int        `json:"slot"`      // which completed item comes next (1..6)
	SlotBase  float64    `json:"slotBase"`  // this champion's average win rate for that item slot
	Basis     string     `json:"basis"`     // path: players with these augments and your items | partial: sharing some | champion: this champion with your items, any augments | all: everyone with these augments
	Next      []AugItem  `json:"next"`      // best next items, best first
	Boots     []AugItem  `json:"boots"`     // when you have no boots yet
	Cores     []AugCore  `json:"cores"`     // best core sets, ones matching your items first
	Direction *Direction `json:"direction"` // which build style goes with these augments
	Source    string     `json:"source"`
}

// AugItem is a candidate item with its record in games with these augments.
type AugItem struct {
	Item      ItemRef `json:"item"`
	WinRate   float64 `json:"winRate"`
	Games     int     `json:"games"`
	Lift      float64 `json:"lift"` // vs this champion's average for the same item slot
	LowSample bool    `json:"lowSample,omitempty"`
}

// AugCore is a core item set and the order it is usually bought in.
type AugCore struct {
	Style    string    `json:"style"` // crit | on_hit | ap | hybrid | lethality | …
	Items    []ItemRef `json:"items"`
	Boots    *ItemRef  `json:"boots,omitempty"`
	WinRate  float64   `json:"winRate"`
	Games    int       `json:"games"`
	PickRate float64   `json:"pickRate"`
	Owned    int       `json:"owned"`   // how many of these you already have
	Augment  int       `json:"augment"` // which augment's games this comes from
}

// StyleStat is one build style's record in games with the augments.
type StyleStat struct {
	Style   string  `json:"style"`
	Name    string  `json:"name"`
	WinRate float64 `json:"winRate"`
	Games   int     `json:"games"`
	Share   float64 `json:"share"` // of the games with these augments
}

// Direction says which build style players take with these augments, which
// one wins most, and whether your items point elsewhere.
type Direction struct {
	Popular  StyleStat  `json:"popular"`
	Best     StyleStat  `json:"best"`
	Yours    *StyleStat `json:"yours,omitempty"`
	Conflict bool       `json:"conflict"`
	Note     string     `json:"note"`
}

func itemRef(d *ddragon.Data, id int) ItemRef {
	if d != nil {
		if it, ok := d.Items[id]; ok {
			return ItemRef{ID: id, Name: it.Name, Image: it.Image}
		}
	}
	return ItemRef{ID: id}
}

type tally struct{ wins, games float64 }

func (t *tally) add(wr float64, n int) {
	t.wins += wr * float64(n)
	t.games += float64(n)
}

func (t *tally) wr() float64 {
	if t.games == 0 {
		return 0
	}
	return t.wins / t.games
}

// PlanAugBuild works out the next item, boots, core sets and build style for
// the augments taken, from aramkit's per-augment purchase routes. The next
// item comes from players who took these augments AND bought the items you
// own ("path"); if nobody did, from those sharing some of them ("partial");
// only then from everyone who took the augments ("all").
func PlanAugBuild(d *ddragon.Data, ch *AKChampion, abs map[int]*AKAugmentBuild, augs, owned []int) *AugBuild {
	out := &AugBuild{Augments: augs, Source: "aramkit"}
	var use []*AKAugmentBuild
	for _, a := range augs {
		if ab := abs[a]; ab != nil {
			use = append(use, ab)
			if out.Games == 0 || ab.Games < out.Games {
				out.Games = ab.Games
			}
		}
	}
	if len(use) == 0 {
		return out
	}
	have := map[int]bool{}
	for _, id := range owned {
		have[id] = true
	}
	// Completed items you own = owned items that show up in the data at all
	// (the data only counts finished items and boots).
	known := map[int]bool{}
	for _, ab := range use {
		for _, r := range ab.Items.All {
			known[r.ID] = true
		}
	}
	var core []int // finished non-boots items you own
	ownBoots := false
	for id := range have {
		if !known[id] {
			continue
		}
		if isBoots(d, id) {
			ownBoots = true
		} else {
			core = append(core, id)
		}
	}
	sort.Ints(core)
	done := len(core)
	if ownBoots {
		done++
	}
	out.Slot = min(done+1, 6)

	base := 0.5
	if ch != nil && ch.WinRate > 0 {
		base = ch.WinRate
	}
	// Games that reach a 3rd or 5th item skew long and winning, so an item is
	// compared with this champion's average for the same slot, not overall.
	slotBase := base
	if ch != nil {
		var t tally
		for _, r := range ch.Items.Slots[itoa(out.Slot)] {
			t.add(r.WinRate, r.Games)
		}
		if t.games > 0 {
			slotBase = t.wr()
		}
	}
	out.SlotBase = slotBase

	// Purchase routes and how many of your finished items each one contains.
	type match struct {
		r     AKRoute
		style string
		over  int
	}
	collect := func(archs []AKArchetype) ([]match, int) {
		var ms []match
		best := 0
		for _, arch := range archs {
			for _, p := range arch.Profiles {
				for _, r := range p.Routes {
					over := 0
					for _, it := range r.PurchaseOrder {
						if have[it.ID] {
							over++
						}
					}
					ms = append(ms, match{r, arch.Key, over})
					best = max(best, over)
				}
			}
		}
		return ms, best
	}
	nextT := map[int]*tally{}
	bootsT := map[int]*tally{}
	addTo := func(m map[int]*tally, id int, wr float64, n int) {
		if have[id] {
			return
		}
		t := m[id]
		if t == nil {
			t = &tally{}
			m[id] = t
		}
		t.add(wr, n)
	}
	// follow tallies the next item of every route that contains at least
	// `need` of your items: its first core item you lack, or with the core
	// complete, its later items.
	follow := func(ms []match, need int) {
		for _, m := range ms {
			if m.over < need {
				continue
			}
			nextCore := 0
			for _, it := range m.r.PurchaseOrder {
				if !have[it.ID] {
					nextCore = it.ID
					break
				}
			}
			if nextCore != 0 {
				addTo(nextT, nextCore, m.r.WinRate, m.r.Games)
			} else {
				for _, l := range m.r.LaterItems {
					addTo(nextT, l.Item.ID, l.WinRate, l.Games)
				}
			}
			if !ownBoots {
				for _, b := range m.r.Boots {
					addTo(bootsT, b.Item.ID, b.WinRate, b.Games)
				}
			}
		}
	}
	var augArchs []AKArchetype
	for _, ab := range use {
		augArchs = append(augArchs, ab.Builds...)
	}
	routes, best := collect(augArchs)
	full := min(len(core), 3)
	switch {
	case best >= full:
		out.Basis = "path" // nothing bought yet counts as on every path
		follow(routes, full)
	case best > 0:
		out.Basis = "partial"
		follow(routes, best)
	}
	// Nobody with these augments built like you: the champion's own routes
	// (any augments) that contain your items, before giving up on your path.
	if len(nextT) == 0 && len(core) > 0 && ch != nil {
		champRoutes, cbest := collect(ch.Builds)
		if cbest > 0 {
			out.Basis = "champion"
			follow(champRoutes, min(cbest, full))
		}
	}
	if len(nextT) == 0 {
		out.Basis = "all"
		for _, ab := range use {
			for _, r := range ab.Items.Slots[itoa(out.Slot)] {
				if ownBoots && isBoots(d, r.ID) {
					continue
				}
				addTo(nextT, r.ID, r.WinRate, r.Games)
			}
		}
	}
	out.Next = rankItems(d, nextT, slotBase, 6)
	if !ownBoots {
		out.Boots = rankItems(d, bootsT, slotBase, 2)
	}

	// Core sets: ones sharing more of your items first, then by win rate.
	for _, ab := range use {
		for _, arch := range ab.Builds {
			for _, p := range arch.Profiles {
				if p.Games < minItemGames {
					continue
				}
				c := AugCore{Style: arch.Key, WinRate: p.WinRate, Games: p.Games, PickRate: p.PickRate, Augment: ab.AugmentID}
				order := p.ItemSet
				if len(p.Routes) > 0 && len(p.Routes[0].PurchaseOrder) > 0 {
					order = p.Routes[0].PurchaseOrder
					if bs := p.Routes[0].Boots; len(bs) > 0 {
						bb := bs[0]
						for _, b := range bs {
							if b.Games >= 50 && Shrink(b.WinRate, b.Games, base) > Shrink(bb.WinRate, bb.Games, base) {
								bb = b
							}
						}
						r := itemRef(d, bb.Item.ID)
						c.Boots = &r
					}
				}
				for _, it := range order {
					c.Items = append(c.Items, itemRef(d, it.ID))
					if have[it.ID] {
						c.Owned++
					}
				}
				out.Cores = append(out.Cores, c)
			}
		}
	}
	sort.Slice(out.Cores, func(i, j int) bool {
		a, b := out.Cores[i], out.Cores[j]
		if a.Owned != b.Owned {
			return a.Owned > b.Owned
		}
		return Shrink(a.WinRate, a.Games, base) > Shrink(b.WinRate, b.Games, base)
	})
	if len(out.Cores) > 3 {
		out.Cores = out.Cores[:3]
	}
	out.Direction = direction(use, ch, core, base)
	return out
}

// direction compares the build styles of games with these augments: the most
// played, the best-winning (with a real sample), and the one your items are on.
func direction(use []*AKAugmentBuild, ch *AKChampion, core []int, base float64) *Direction {
	styles := map[string]*tally{}
	var total float64
	// Which style your items belong to: games of core sets sharing them.
	yours := map[string]float64{}
	have := map[int]bool{}
	for _, id := range core {
		have[id] = true
	}
	for _, ab := range use {
		for _, arch := range ab.Builds {
			t := styles[arch.Key]
			if t == nil {
				t = &tally{}
				styles[arch.Key] = t
			}
			t.add(arch.WinRate, arch.Games)
			total += float64(arch.Games)
			for _, p := range arch.Profiles {
				over := 0
				for _, it := range p.ItemSet {
					if have[it.ID] {
						over++
					}
				}
				yours[arch.Key] += float64(over * p.Games)
			}
		}
	}
	if total == 0 {
		return nil
	}
	stat := func(k string) StyleStat {
		t := styles[k]
		return StyleStat{Style: k, Name: StyleName(k), WinRate: t.wr(), Games: int(t.games), Share: t.games / total}
	}
	dir := &Direction{}
	var popular, bestK, yourK string
	for k, t := range styles {
		if popular == "" || t.games > styles[popular].games {
			popular = k
		}
		// A style needs a real sample to be called the best.
		if t.games >= 1000 || t.games/total >= 0.1 {
			if bestK == "" || Shrink(t.wr(), int(t.games), base) > Shrink(styles[bestK].wr(), int(styles[bestK].games), base) {
				bestK = k
			}
		}
	}
	if bestK == "" {
		bestK = popular
	}
	for k, w := range yours {
		if w > 0 && (yourK == "" || w > yours[yourK]) {
			yourK = k
		}
	}
	dir.Popular, dir.Best = stat(popular), stat(bestK)
	pct := func(x float64) string { return strconv.FormatFloat(x*100, 'f', 1, 64) + "%" }
	// Your items are on no route of these augments: name the style from the
	// champion's own builds and say that hardly anyone combines the two.
	if yourK == "" && len(core) > 0 && ch != nil {
		champYours := map[string]float64{}
		for _, arch := range ch.Builds {
			for _, p := range arch.Profiles {
				over := 0
				for _, it := range p.ItemSet {
					if have[it.ID] {
						over++
					}
				}
				champYours[arch.Key] += float64(over * p.Games)
			}
		}
		k := ""
		for kk, w := range champYours {
			if w > 0 && (k == "" || w > champYours[k]) {
				k = kk
			}
		}
		if k != "" {
			y := StyleStat{Style: k, Name: StyleName(k)}
			if t := styles[k]; t != nil {
				y = stat(k)
			}
			dir.Yours = &y
			dir.Conflict = true
			dir.Note = "你的装备在走" + y.Name + "，但选了这些强化的人几乎没人这样出（主流是" + dir.Popular.Name + "，占 " + pct(dir.Popular.Share) + "，胜率 " + pct(dir.Popular.WinRate) + "）。继续走" + y.Name + "的话，下一件按这个英雄平时的" + y.Name + "出装推荐。"
			return dir
		}
	}
	if yourK == "" {
		dir.Note = "选了这些强化的人最常走" + dir.Popular.Name + "（占 " + pct(dir.Popular.Share) + "，胜率 " + pct(dir.Popular.WinRate) + "）"
		if bestK != popular {
			dir.Note += "；胜率最高的是" + dir.Best.Name + "（" + pct(dir.Best.WinRate) + "）"
		}
		dir.Note += "。"
		return dir
	}
	y := stat(yourK)
	dir.Yours = &y
	if yourK == bestK {
		dir.Note = "你的装备走的是" + y.Name + "，正好是选了这些强化胜率最高的路线（" + pct(y.WinRate) + "）。"
		return dir
	}
	diff := dir.Best.WinRate - y.WinRate
	dir.Conflict = diff >= 0.02
	dir.Note = "你的装备在走" + y.Name + "（选了这些强化时胜率 " + pct(y.WinRate) + "），而" + dir.Best.Name + "胜率 " + pct(dir.Best.WinRate) + "。"
	if dir.Conflict {
		dir.Note += "差距明显，后面的装备可以考虑往" + dir.Best.Name + "转。"
	} else {
		dir.Note += "差距不大，按现在的路线继续就行。"
	}
	return dir
}

// rankItems turns tallies into the best few items (shrunk win rate), keeping
// the minimum sample; if nothing reaches it, the best small-sample ones,
// flagged.
func rankItems(d *ddragon.Data, m map[int]*tally, base float64, n int) []AugItem {
	var ok, low []AugItem
	for id, t := range m {
		it := AugItem{Item: itemRef(d, id), WinRate: t.wr(), Games: int(t.games), Lift: t.wr() - base}
		switch {
		case t.games >= minItemGames:
			ok = append(ok, it)
		case t.games >= 50:
			it.LowSample = true
			low = append(low, it)
		}
	}
	order := func(xs []AugItem) {
		sort.Slice(xs, func(i, j int) bool {
			return Shrink(xs[i].WinRate, xs[i].Games, base) > Shrink(xs[j].WinRate, xs[j].Games, base)
		})
	}
	order(ok)
	order(low)
	if len(ok) == 0 {
		ok = low
	}
	if len(ok) > n {
		ok = ok[:n]
	}
	return ok
}
func isBoots(d *ddragon.Data, id int) bool {
	if d == nil {
		return false
	}
	for _, t := range d.Items[id].Tags {
		if t == "Boots" {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i >= 0 && i < 10 {
		return string(rune('0' + i))
	}
	b := []byte{}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
