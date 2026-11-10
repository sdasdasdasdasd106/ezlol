package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
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

// AKRoute is one purchase order of a core set.
type AKRoute struct {
	Games         int     `json:"sampleCount"`
	PickRate      float64 `json:"pickRate"`
	WinRate       float64 `json:"winRate"`
	PurchaseOrder []AKRef `json:"purchaseOrder"`
	Boots         []struct {
		Games   int     `json:"sampleCount"`
		WinRate float64 `json:"winRate"`
		Item    AKRef   `json:"item"`
	} `json:"boots"`
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
	Version string
	Games   int
	WinRate float64
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
				Games   int     `json:"sampleCount"`
				WinRate float64 `json:"winRate"`
			} `json:"stats"`
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
		Version: ver, Games: raw.Champion.Stats.Games, WinRate: raw.Champion.Stats.WinRate,
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
