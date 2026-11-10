package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/ddragon"
)

// ARAM Mayhem statistics from aramkit.com (data.aramkit.com): per champion,
// augment win rates per pick stage, augment pairs, and — the part nothing else
// has — item stats and core builds *given a specific augment*. These are the
// site's own data files, not a documented API, so every failure here falls
// back to the aramgg numbers.

const aramkitBase = "https://data.aramkit.com/"

// AKStat is one row of a ranking: an augment or an item with its record.
type AKStat struct {
	ID       int     `json:"id"`
	Tier     string  `json:"tier"` // S A B C D (augment rankings only)
	Games    int     `json:"sampleCount"`
	PickRate float64 `json:"pickRate"`
	WinRate  float64 `json:"winRate"`
}

// AKRef is an item id inside a set.
type AKRef struct {
	ID int `json:"id"`
}

// AKPick is one item choice (boots, a later item) with its record.
type AKPick struct {
	Games   int     `json:"sampleCount"`
	WinRate float64 `json:"winRate"`
	Item    AKRef   `json:"item"`
}

// AKRoute is one purchase order of a core set.
type AKRoute struct {
	Games         int     `json:"sampleCount"`
	PickRate      float64 `json:"pickRate"`
	WinRate       float64 `json:"winRate"`
	PurchaseOrder []AKRef `json:"purchaseOrder"`
	Starters      []struct {
		Games   int     `json:"sampleCount"`
		WinRate float64 `json:"winRate"`
		Items   []AKRef `json:"items"`
	} `json:"starters"`
	Boots      []AKPick `json:"boots"`
	LaterItems []AKPick `json:"laterItems"`
}

// AKProfile is a core item set (usually three items) and how it was bought.
type AKProfile struct {
	Games    int       `json:"sampleCount"`
	PickRate float64   `json:"pickRate"`
	WinRate  float64   `json:"winRate"`
	ItemSet  []AKRef   `json:"itemSet"`
	Routes   []AKRoute `json:"routes"`
}

// AKArchetype groups core sets by build style: crit, on_hit, ap, hybrid, lethality…
type AKArchetype struct {
	Key      string      `json:"key"`
	Games    int         `json:"sampleCount"`
	PickRate float64     `json:"pickRate"`
	WinRate  float64     `json:"winRate"`
	Profiles []AKProfile `json:"profiles"`
}

// AKItems is item usage: overall and by the order the item was completed (slot 1..6).
type AKItems struct {
	Games int                 `json:"sampleCount"`
	All   []AKStat            `json:"all"`
	Slots map[string][]AKStat `json:"slots"`
}

// AKPair is a pair of augments picked in the same game.
type AKPair struct {
	AugmentIDs []int   `json:"augmentIds"`
	Games      int     `json:"sampleCount"`
	PickRate   float64 `json:"pickRate"`
	WinRate    float64 `json:"winRate"`
}

// AKChampion is a champion's ARAM Mayhem detail.
type AKChampion struct {
	Version  string
	Games    int
	WinRate  float64
	PickRate float64
	Tier     string // S A B C D among all champions
	Rank     int
	// Augments overall and per pick stage ("1".."4" = game start, level 7, 11, 15).
	Augments []AKStat
	Stages   map[string][]AKStat
	Pairs    []AKPair
	Items    AKItems // items with augment-granted items filtered out
	Builds   []AKArchetype
}

// AKAugmentBuild is what players on this champion built after taking one augment.
type AKAugmentBuild struct {
	AugmentID int
	Games     int
	Items     AKItems
	Builds    []AKArchetype
}

type aramkitVersions struct {
	Latest   string `json:"latest"`
	Versions []struct {
		Version  string `json:"version"`
		DataPath string `json:"dataPath"`
	} `json:"versions"`
}

type aramkitCache struct {
	mu     sync.Mutex
	single map[int]map[int]*AKAugmentBuild // champion -> augment -> build, a few champions kept
	order  []int
}

// aramkitPath returns the latest data path, e.g. "data/16.22-20261109-7884767a6057", and its version.
func (c *Community) aramkitPath(ctx context.Context) (string, string, error) {
	b, err := c.fetch(ctx, "aramkit-versions.json", aramkitBase+"data/versions.json", 3*time.Hour)
	if err != nil {
		return "", "", err
	}
	var v aramkitVersions
	if err := json.Unmarshal(b, &v); err != nil {
		return "", "", fmt.Errorf("aramkit versions: %w", err)
	}
	for _, x := range v.Versions {
		if x.Version == v.Latest && x.DataPath != "" {
			return x.DataPath, x.Version, nil
		}
	}
	if len(v.Versions) > 0 && v.Versions[0].DataPath != "" {
		return v.Versions[0].DataPath, v.Versions[0].Version, nil
	}
	return "", "", fmt.Errorf("aramkit versions: no data path")
}

// AramkitChampion returns a champion's ARAM Mayhem detail from aramkit.
func (c *Community) AramkitChampion(ctx context.Context, champID int) (*AKChampion, error) {
	path, ver, err := c.aramkitPath(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s%s/stats/all/champion-details/%d.json", aramkitBase, path, champID)
	b, err := c.fetch(ctx, fmt.Sprintf("aramkit-%s-champ-%d.json", ver, champID), url, 6*time.Hour)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Champion struct {
			Stats struct {
				Games    int     `json:"sampleCount"`
				WinRate  float64 `json:"winRate"`
				PickRate float64 `json:"pickRate"`
			} `json:"stats"`
			Rank int    `json:"rank"`
			Tier string `json:"tier"`
		} `json:"champion"`
		Augments struct {
			All    []AKStat            `json:"all"`
			Stages map[string][]AKStat `json:"stages"`
		} `json:"augments"`
		Pairs []AKPair `json:"augmentCombinations"`
		Items struct {
			Filtered AKItems `json:"filtered"`
		} `json:"items"`
		Builds struct {
			Filtered struct {
				Archetypes []AKArchetype `json:"archetypes"`
			} `json:"filtered"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("aramkit champion %d: %w", champID, err)
	}
	return &AKChampion{
		Version: ver, Games: raw.Champion.Stats.Games, WinRate: raw.Champion.Stats.WinRate, PickRate: raw.Champion.Stats.PickRate,
		Tier: raw.Champion.Tier, Rank: raw.Champion.Rank,
		Augments: raw.Augments.All, Stages: raw.Augments.Stages, Pairs: raw.Pairs,
		Items: raw.Items.Filtered, Builds: raw.Builds.Filtered.Archetypes,
	}, nil
}

// AramkitAugmentBuilds returns, per augment, what this champion built after taking it.
// The source file is several MB, so parsed results for the last few champions stay in memory.
func (c *Community) AramkitAugmentBuilds(ctx context.Context, champID int) (map[int]*AKAugmentBuild, error) {
	c.ak.mu.Lock()
	if m, ok := c.ak.single[champID]; ok {
		c.ak.mu.Unlock()
		return m, nil
	}
	c.ak.mu.Unlock()

	path, ver, err := c.aramkitPath(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s%s/stats/all/champion-details/%d-single-augments.json", aramkitBase, path, champID)
	b, err := c.fetch(ctx, fmt.Sprintf("aramkit-%s-champ-%d-single.json", ver, champID), url, 6*time.Hour)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []struct {
			AugmentID int `json:"augmentId"`
			Games     int `json:"sampleCount"`
			AKItems
		} `json:"items"`
		Builds []struct {
			AugmentID  int           `json:"augmentId"`
			Games      int           `json:"sampleCount"`
			Archetypes []AKArchetype `json:"archetypes"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("aramkit augment builds %d: %w", champID, err)
	}
	out := map[int]*AKAugmentBuild{}
	get := func(id int) *AKAugmentBuild {
		if out[id] == nil {
			out[id] = &AKAugmentBuild{AugmentID: id}
		}
		return out[id]
	}
	for _, it := range raw.Items {
		ab := get(it.AugmentID)
		ab.Games = max(ab.Games, it.Games)
		ab.Items = it.AKItems
	}
	for _, bd := range raw.Builds {
		ab := get(bd.AugmentID)
		ab.Games = max(ab.Games, bd.Games)
		ab.Builds = bd.Archetypes
	}

	c.ak.mu.Lock()
	defer c.ak.mu.Unlock()
	if c.ak.single == nil {
		c.ak.single = map[int]map[int]*AKAugmentBuild{}
	}
	c.ak.single[champID] = out
	c.ak.order = append(c.ak.order, champID)
	for len(c.ak.order) > 4 {
		delete(c.ak.single, c.ak.order[0])
		c.ak.order = c.ak.order[1:]
	}
	return out, nil
}

// AramkitBuild turns aramkit's ARAM Mayhem data into the build view: the
// best-performing core sets (with enough games), and starters, boots and
// later items pooled over those sets' purchase routes.
func (c *Community) AramkitBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion) (*Build, error) {
	ch, err := c.AramkitChampion(ctx, champ.ID)
	if err != nil {
		return nil, err
	}
	base := ch.WinRate
	out := &Build{Champion: champ, Patch: ch.Version, Mode: "aram", Source: "aramkit",
		Total: Count{Games: ch.Games, Wins: int(float64(ch.Games)*ch.WinRate + 0.5)},
		Tier:  ch.Tier, Rank: ch.Rank, PickRate: ch.PickRate}

	type prof struct {
		p     AKProfile
		style string
	}
	var profs []prof
	for _, a := range ch.Builds {
		for _, p := range a.Profiles {
			if p.Games >= 200 {
				profs = append(profs, prof{p, a.Key})
			}
		}
	}
	sort.Slice(profs, func(i, j int) bool {
		return Shrink(profs[i].p.WinRate, profs[i].p.Games, base) > Shrink(profs[j].p.WinRate, profs[j].p.Games, base)
	})
	// The popular sets matter too: keep the best 4 by win rate, plus the most
	// played one if it is not among them.
	if len(profs) > 4 {
		most := 0
		for i, p := range profs {
			if p.p.Games > profs[most].p.Games {
				most = i
			}
		}
		keep := profs[:4]
		if most >= 4 {
			keep = append(keep[:3:3], profs[most])
		}
		profs = keep
	}
	type tally struct{ wins, games float64 }
	starters := map[string]*tally{}
	starterItems := map[string][]AKRef{}
	boots := map[int]*tally{}
	later := map[int]*tally{}
	add := func(m map[int]*tally, id, n int, wr float64) {
		t := m[id]
		if t == nil {
			t = &tally{}
			m[id] = t
		}
		t.games += float64(n)
		t.wins += wr * float64(n)
	}
	for _, p := range profs {
		order := p.p.ItemSet
		if len(p.p.Routes) > 0 && len(p.p.Routes[0].PurchaseOrder) > 0 {
			order = p.p.Routes[0].PurchaseOrder
		}
		set := ItemSet{Count: Count{Games: p.p.Games, Wins: int(float64(p.p.Games)*p.p.WinRate + 0.5)}}
		for _, it := range order {
			set.Items = append(set.Items, itemRef(d, it.ID))
		}
		out.Core = append(out.Core, set)
		out.CoreTags = append(out.CoreTags, p.style)
	}
	// Starters, boots and later items go with one build: the top core set's.
	if len(profs) > 0 {
		for _, r := range profs[0].p.Routes {
			for _, st := range r.Starters {
				key := ""
				for _, it := range st.Items {
					key += itoa(it.ID) + ","
				}
				t := starters[key]
				if t == nil {
					t = &tally{}
					starters[key] = t
					starterItems[key] = st.Items
				}
				t.games += float64(st.Games)
				t.wins += st.WinRate * float64(st.Games)
			}
			for _, b := range r.Boots {
				add(boots, b.Item.ID, b.Games, b.WinRate)
			}
			for _, l := range r.LaterItems {
				add(later, l.Item.ID, l.Games, l.WinRate)
			}
		}
	}
	rank := func(m map[int]*tally, n, minGames int) []ItemSet {
		var rows []ItemSet
		for id, t := range m {
			if t.games < float64(minGames) {
				continue
			}
			rows = append(rows, ItemSet{Count: Count{Games: int(t.games), Wins: int(t.wins + 0.5)}, Items: []ItemRef{itemRef(d, id)}})
		}
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			return Shrink(float64(a.Wins)/float64(a.Games), a.Games, base) > Shrink(float64(b.Wins)/float64(b.Games), b.Games, base)
		})
		if len(rows) > n {
			rows = rows[:n]
		}
		return rows
	}
	out.Boots = rank(boots, 3, 300)
	// Late items: games that reach a 4th-6th item skew long and winning, so
	// their win rates are not comparable; list the common ones.
	out.Late = rank(later, 12, 300)
	sort.SliceStable(out.Late, func(i, j int) bool { return out.Late[i].Games > out.Late[j].Games })
	if len(out.Late) > 6 {
		out.Late = out.Late[:6]
	}
	var srows []ItemSet
	for key, t := range starters {
		if t.games < 100 {
			continue
		}
		row := ItemSet{Count: Count{Games: int(t.games), Wins: int(t.wins + 0.5)}}
		for _, it := range starterItems[key] {
			row.Items = append(row.Items, itemRef(d, it.ID))
		}
		srows = append(srows, row)
	}
	// Starters: by how often they are bought, then win rate.
	sort.Slice(srows, func(i, j int) bool { return srows[i].Games > srows[j].Games })
	if len(srows) > 3 {
		srows = srows[:3]
	}
	out.Starting = srows
	if len(out.Core) == 0 {
		return nil, fmt.Errorf("aramkit: no core builds for %s", champ.Key)
	}
	return out, nil
}

// AramkitAugments is the champion's augment list from aramkit (all pick
// stages), best first within each rarity, with names from the game data.
func (c *Community) AramkitAugments(ctx context.Context, patch string, champID int) ([]Augment, error) {
	ch, err := c.AramkitChampion(ctx, champID)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(ch.Augments))
	for _, r := range ch.Augments {
		ids = append(ids, r.ID)
	}
	info := c.AugmentInfo(ctx, patch, ids)
	tiers := map[string]int{"S": 1, "A": 2, "B": 3, "C": 4, "D": 5}
	out := make([]Augment, 0, len(ch.Augments))
	for _, r := range ch.Augments {
		a := info[r.ID]
		a.WinRate, a.PickRate, a.Games, a.Scope, a.Tier = r.WinRate, r.PickRate, r.Games, "champion", tiers[r.Tier]
		out = append(out, a)
	}
	base := ch.WinRate
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rarity != b.Rarity {
			return rarityRank(a.Rarity) < rarityRank(b.Rarity)
		}
		return Shrink(a.WinRate, a.Games, base) > Shrink(b.WinRate, b.Games, base)
	})
	return out, nil
}
