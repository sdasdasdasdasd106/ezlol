package builds

import (
	"sort"

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

// AugBuild is the item plan for a champion given the augments picked (or being
// considered) and the items already owned.
type AugBuild struct {
	Augments []int     `json:"augments"`
	Games    int       `json:"games"`    // fewest games among the augments' samples
	Slot     int       `json:"slot"`     // which completed item comes next (1..6)
	SlotBase float64   `json:"slotBase"` // this champion's average win rate for that item slot
	Next     []AugItem `json:"next"`     // best next items, best first
	Cores    []AugCore `json:"cores"`    // best core sets, ones matching your items first
	Source   string    `json:"source"`
}

// AugItem is a candidate item with its record in games with these augments.
type AugItem struct {
	Item    ItemRef `json:"item"`
	WinRate float64 `json:"winRate"`
	Games   int     `json:"games"`
	Lift    float64 `json:"lift"` // vs this champion's average for the same item slot
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

func itemRef(d *ddragon.Data, id int) ItemRef {
	if d != nil {
		if it, ok := d.Items[id]; ok {
			return ItemRef{ID: id, Name: it.Name, Image: it.Image}
		}
	}
	return ItemRef{ID: id}
}

// PlanAugBuild works out the next item and core sets from the per-augment data.
// With several augments, their item records are pooled (weighted by games).
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
	done := 0
	ownBoots := false
	for id := range have {
		if known[id] {
			done++
			if isBoots(d, id) {
				ownBoots = true
			}
		}
	}
	out.Slot = min(done+1, 6)

	base := 0.5
	if ch != nil && ch.WinRate > 0 {
		base = ch.WinRate
	}
	// Games that reach a 3rd or 5th item skew long and winning, so an item is
	// compared with this champion's average for the same slot, not overall.
	slotBase := base
	{
		var w, n float64
		for _, r := range ch.Items.Slots[itoa(out.Slot)] {
			w += r.WinRate * float64(r.Games)
			n += float64(r.Games)
		}
		if n > 0 {
			slotBase = w / n
		}
	}
	out.SlotBase = slotBase
	type agg struct {
		wins, games float64
	}
	pool := map[int]*agg{}
	for _, ab := range use {
		for _, r := range ab.Items.Slots[itoa(out.Slot)] {
			if have[r.ID] || (ownBoots && isBoots(d, r.ID)) {
				continue
			}
			a := pool[r.ID]
			if a == nil {
				a = &agg{}
				pool[r.ID] = a
			}
			a.wins += r.WinRate * float64(r.Games)
			a.games += float64(r.Games)
		}
	}
	for id, a := range pool {
		if a.games < 30 {
			continue
		}
		wr := a.wins / a.games
		out.Next = append(out.Next, AugItem{Item: itemRef(d, id), WinRate: wr, Games: int(a.games), Lift: wr - slotBase})
	}
	// Rank by win rate pulled toward the slot average, so 40-game flukes do not lead.
	sort.Slice(out.Next, func(i, j int) bool {
		a, b := out.Next[i], out.Next[j]
		return Shrink(a.WinRate, a.Games, slotBase) > Shrink(b.WinRate, b.Games, slotBase)
	})
	if len(out.Next) > 6 {
		out.Next = out.Next[:6]
	}

	for _, ab := range use {
		for _, arch := range ab.Builds {
			for _, p := range arch.Profiles {
				if p.Games < 50 {
					continue
				}
				c := AugCore{Style: arch.Key, WinRate: p.WinRate, Games: p.Games, PickRate: p.PickRate, Augment: ab.AugmentID}
				order := p.ItemSet
				if len(p.Routes) > 0 && len(p.Routes[0].PurchaseOrder) > 0 {
					order = p.Routes[0].PurchaseOrder
					if bs := p.Routes[0].Boots; len(bs) > 0 {
						best := bs[0]
						for _, b := range bs {
							if b.Games >= 50 && Shrink(b.WinRate, b.Games, base) > Shrink(best.WinRate, best.Games, base) {
								best = b
							}
						}
						r := itemRef(d, best.Item.ID)
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
	return out
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
