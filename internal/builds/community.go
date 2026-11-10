package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aarlint/ezlol/internal/ddragon"
)

// Community pulls current-patch ARAM builds and ARAM Mayhem augment stats from
// public community endpoints (no Riot key needed) and caches them on disk.
//
//   - op.gg      lol-api-champion.op.gg   items, runes, spells, skills, win rates (queue 450)
//   - aramgg.com augments per champion for ARAM: Mayhem (Tencent CN + client uploads)
//   - CommunityDragon / blitz.gg  augment names, icons, rarity, descriptions
type Community struct {
	dir  string
	http *http.Client
	mu   sync.Mutex
	meta *augmentMeta
}

const cdragonBase = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/default/"

// cdragonZH is the same game data with Simplified Chinese strings.
const cdragonZH = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/zh_cn/"

// blitzLocales: augment descriptions/names from blitz, Chinese first.
var blitzLocales = []string{"zh_cn", "en_us"}

// NewCommunity creates a provider caching under dir.
func NewCommunity(dir string) *Community {
	_ = os.MkdirAll(dir, 0o755)
	return &Community{dir: dir, http: &http.Client{Timeout: 20 * time.Second}}
}

// Augment is one Mayhem augment with stats for a champion (or globally).
type Augment struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`             // Simplified Chinese when available
	NameEN   string  `json:"nameEn,omitempty"` // English name, also matched on screen
	Desc     string  `json:"desc"`
	Rarity   string  `json:"rarity"` // prismatic | gold | silver | bronze
	Icon     string  `json:"icon"`
	Tier     int     `json:"tier"` // 1 best .. 5
	WinRate  float64 `json:"winRate"`
	PickRate float64 `json:"pickRate"`
	Games    int     `json:"games"`
	Scope    string  `json:"scope"`              // champion | global
	AvgPlace float64 `json:"avgPlace,omitempty"` // Arena: average placement (lower is better)
	Top1     float64 `json:"top1,omitempty"`     // Arena: first-place rate
}

// fetch GETs a URL with a browser-ish UA, caching the body for ttl.
func (c *Community) fetch(ctx context.Context, name, url string, ttl time.Duration) ([]byte, error) {
	path := filepath.Join(c.dir, name)
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < ttl {
		if b, err := os.ReadFile(path); err == nil {
			return b, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36 ezlol/0.1")
	req.Header.Set("Accept", "application/json,*/*")
	res, err := c.http.Do(req)
	if err != nil {
		// offline: serve stale cache if any
		if b, rerr := os.ReadFile(path); rerr == nil {
			return b, nil
		}
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if b, rerr := os.ReadFile(path); rerr == nil {
			return b, nil
		}
		return nil, fmt.Errorf("%s: status %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(path, b, 0o644)
	return b, nil
}

// ---- op.gg ARAM builds ----

type opggCount struct {
	IDs      []int   `json:"ids"`
	Play     int     `json:"play"`
	Win      int     `json:"win"`
	PickRate float64 `json:"pick_rate"`
}

type opggRuneBuild struct {
	PrimaryRuneIDs   []int `json:"primary_rune_ids"`
	SecondaryRuneIDs []int `json:"secondary_rune_ids"`
	StatModIDs       []int `json:"stat_mod_ids"`
	Play             int   `json:"play"`
	Win              int   `json:"win"`
}

type opggResponse struct {
	Meta struct {
		Version string `json:"version"`
	} `json:"meta"`
	Data struct {
		Summary struct {
			AverageStats struct {
				Play     int     `json:"play"`
				WinRate  float64 `json:"win_rate"`
				PickRate float64 `json:"pick_rate"`
				Tier     int     `json:"tier"`
				Rank     int     `json:"rank"`
			} `json:"average_stats"`
		} `json:"summary"`
		SummonerSpells []opggCount `json:"summoner_spells"`
		CoreItems      []opggCount `json:"core_items"`
		Boots          []opggCount `json:"boots"`
		StarterItems   []opggCount `json:"starter_items"`
		LastItems      []opggCount `json:"last_items"`
		RunePages      []struct {
			PrimaryPageID   int             `json:"primary_page_id"`
			SecondaryPageID int             `json:"secondary_page_id"`
			Play            int             `json:"play"`
			Win             int             `json:"win"`
			Builds          []opggRuneBuild `json:"builds"`
		} `json:"rune_pages"`
		SkillMasteries []struct {
			IDs    []string `json:"ids"`
			Play   int      `json:"play"`
			Win    int      `json:"win"`
			Builds []struct {
				Order []string `json:"order"`
				Play  int      `json:"play"`
				Win   int      `json:"win"`
			} `json:"builds"`
		} `json:"skill_masteries"`
	} `json:"data"`
}

// opggRole maps our role names onto op.gg's path segment.
var opggRole = map[string]string{"TOP": "top", "JUNGLE": "jungle", "MIDDLE": "mid", "BOTTOM": "adc", "UTILITY": "support"}

// ARAMBuild returns the op.gg ARAM build for a champion resolved against Data Dragon.
func (c *Community) ARAMBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion) (*Build, error) {
	url := fmt.Sprintf("https://lol-api-champion.op.gg/api/global/champions/aram/%d/none?tier=all", champ.ID)
	return c.opggBuild(ctx, d, champ, url, fmt.Sprintf("opgg-aram-%d.json", champ.ID), "aram", "NONE")
}

// RiftBuild returns the op.gg ranked Summoner's Rift build for a champion in a role.
func (c *Community) RiftBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion, role string) (*Build, error) {
	r, ok := opggRole[role]
	if !ok {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	url := fmt.Sprintf("https://lol-api-champion.op.gg/api/global/champions/ranked/%d/%s?tier=all", champ.ID, r)
	return c.opggBuild(ctx, d, champ, url, fmt.Sprintf("opgg-ranked-%d-%s.json", champ.ID, r), "sr", role)
}

func (c *Community) opggBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion, url, cache, mode, role string) (*Build, error) {
	b, err := c.fetch(ctx, cache, url, 3*time.Hour)
	if err != nil {
		return nil, err
	}
	var r opggResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("op.gg: %w", err)
	}
	item := func(id int) ItemRef {
		it := d.Items[id]
		return ItemRef{ID: id, Name: it.Name, Image: it.Image}
	}
	sets := func(rows []opggCount, n int, skip map[int]bool) []ItemSet {
		var out []ItemSet
		for _, row := range rows {
			if len(out) >= n {
				break
			}
			s := ItemSet{Count: Count{Games: row.Play, Wins: row.Win}}
			for _, id := range row.IDs {
				if id == 0 || skip[id] {
					continue
				}
				s.Items = append(s.Items, item(id))
			}
			if len(s.Items) > 0 {
				out = append(out, s)
			}
		}
		return out
	}
	st := r.Data.Summary.AverageStats
	patch := r.Meta.Version
	if mode == "aram" {
		patch += "-aram"
	}
	out := &Build{
		Champion: champ, Patch: patch, Role: role, Mode: mode, Source: "opgg",
		Total: Count{Games: st.Play, Wins: int(float64(st.Play) * st.WinRate)},
		Tier:  st.Tier, Rank: st.Rank, PickRate: st.PickRate,
	}
	out.Starting = sets(r.Data.StarterItems, 3, nil)
	out.Core = sets(r.Data.CoreItems, 4, nil)
	out.Boots = sets(r.Data.Boots, 3, nil)
	inCore := map[int]bool{}
	for _, cs := range out.Core[:min(1, len(out.Core))] {
		for _, it := range cs.Items {
			inCore[it.ID] = true
		}
	}
	out.Late = sets(r.Data.LastItems, 8, inCore)
	for _, rp := range r.Data.RunePages {
		if len(rp.Builds) == 0 || len(out.Runes) >= 3 {
			continue
		}
		bb := rp.Builds[0]
		page := RunePage{Primary: styleRef(d, rp.PrimaryPageID), Secondary: styleRef(d, rp.SecondaryPageID), Source: "opgg", Count: Count{Games: bb.Play, Wins: bb.Win}}
		for _, id := range append(append([]int{}, bb.PrimaryRuneIDs...), bb.SecondaryRuneIDs...) {
			page.Perks = append(page.Perks, runeRef(d, id))
		}
		for _, id := range bb.StatModIDs {
			page.Shards = append(page.Shards, runeRef(d, id))
		}
		out.Runes = append(out.Runes, page)
	}
	for i, sp := range r.Data.SummonerSpells {
		if i >= 3 {
			break
		}
		pair := SpellPair{Count: Count{Games: sp.Play, Wins: sp.Win}}
		for _, id := range sp.IDs {
			s := d.Spells[id]
			pair.Spells = append(pair.Spells, SpellRef{ID: id, Name: s.Name, Image: s.Image})
		}
		out.Spells = append(out.Spells, pair)
	}
	for i, sm := range r.Data.SkillMasteries {
		if i >= 3 {
			break
		}
		out.SkillOrder = append(out.SkillOrder, Skills{Order: strings.Join(sm.IDs, ">"), Count: Count{Games: sm.Play, Wins: sm.Win}})
		if len(sm.Builds) > 0 {
			o := sm.Builds[0].Order
			if len(o) > 3 {
				o = o[:3]
			}
			out.SkillStart = append(out.SkillStart, Skills{Order: strings.Join(o, ""), Count: Count{Games: sm.Builds[0].Play, Wins: sm.Builds[0].Win}})
			if i == 0 {
				out.SkillPath = sm.Builds[0].Order
			}
		}
	}
	return out, nil
}

// ---- augments ----

type augmentMeta struct {
	names   map[int]string // Chinese, falling back to English
	namesEN map[int]string
	icons   map[int]string
	rarity  map[int]string
	descs   map[int]string
	at      time.Time
}

func (c *Community) loadMeta(ctx context.Context, patch string) *augmentMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.meta != nil && time.Since(c.meta.at) < 24*time.Hour {
		return c.meta
	}
	m := &augmentMeta{names: map[int]string{}, namesEN: map[int]string{}, icons: map[int]string{}, rarity: map[int]string{}, descs: map[int]string{}, at: time.Now()}
	type cherryRow struct {
		ID     int    `json:"id"`
		Name   string `json:"nameTRA"`
		Icon   string `json:"augmentSmallIconPath"`
		Rarity string `json:"rarity"`
	}
	if b, err := c.fetch(ctx, "cherry-augments.json", cdragonBase+"v1/cherry-augments.json", 24*time.Hour); err == nil {
		var rows []cherryRow
		if json.Unmarshal(b, &rows) == nil {
			for _, r := range rows {
				m.namesEN[r.ID] = r.Name
				m.rarity[r.ID] = strings.ToLower(strings.TrimPrefix(r.Rarity, "k"))
				p := strings.ToLower(strings.TrimPrefix(r.Icon, "/lol-game-data/assets/"))
				if p != "" {
					m.icons[r.ID] = cdragonBase + p
				}
			}
		}
	}
	if b, err := c.fetch(ctx, "cherry-augments.zh_cn.json", cdragonZH+"v1/cherry-augments.json", 24*time.Hour); err == nil {
		var rows []cherryRow
		if json.Unmarshal(b, &rows) == nil {
			for _, r := range rows {
				if r.Name != "" {
					m.names[r.ID] = r.Name
				}
			}
		}
	}
	for _, loc := range blitzLocales {
		b, err := c.fetch(ctx, "blitz-augments-"+loc+"-"+patch+".json", "https://utils.iesdev.com/static/json/lol/mayham/"+patch+"/augments_"+loc, 24*time.Hour)
		if err != nil {
			continue
		}
		m.mergeBlitz(b, loc)
	}
	m.fillNames()
	c.meta = m
	return m
}

// mergeBlitz adds blitz descriptions and any names CommunityDragon lacked.
// Callers merge Chinese first, so the first description seen wins.
func (m *augmentMeta) mergeBlitz(b []byte, loc string) {
	var rows map[string]struct {
		ID          int    `json:"id"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return
	}
	names := m.names
	if loc == "en_us" {
		names = m.namesEN
	}
	for _, r := range rows {
		if r.Description != "" && m.descs[r.ID] == "" {
			m.descs[r.ID] = stripTags(r.Description)
		}
		if names[r.ID] == "" && r.DisplayName != "" {
			names[r.ID] = r.DisplayName
		}
	}
}

// fillNames shows the English name where no Chinese one is known.
func (m *augmentMeta) fillNames() {
	for id, en := range m.namesEN {
		if m.names[id] == "" {
			m.names[id] = en
		}
	}
}

// stripTags drops <tags> and %i:placeholder% tokens from augment descriptions.
func stripTags(s string) string {
	var b strings.Builder
	inTag, inVar := false, false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case r == '%' && !inVar:
			inVar = true
			b.WriteString("X")
		case r == '%' && inVar:
			inVar = false
		case !inTag && !inVar:
			b.WriteRune(r)
		}
	}
	out := strings.ReplaceAll(b.String(), "X?", "")
	return strings.Join(strings.Fields(out), " ")
}

type aramggAugStat struct {
	Tier     string `json:"tier"`
	WinRate  string `json:"win_rate"`
	PickRate string `json:"pick_rate"`
	NumGames string `json:"num_games"`
}

func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
func atoi(s string) int     { i, _ := strconv.Atoi(s); return i }

// Augments returns Mayhem augment stats for a champion (falls back to global stats),
// best first within each rarity.
func (c *Community) Augments(ctx context.Context, patch string, champID int) ([]Augment, string, error) {
	meta := c.loadMeta(ctx, patch)
	stats := map[int]aramggAugStat{}
	scope := "champion"
	if b, err := c.fetch(ctx, fmt.Sprintf("aramgg-champ-%d.json", champID), fmt.Sprintf("https://aramgg.com/data/champion-details/%d.json", champID), 6*time.Hour); err == nil {
		var doc struct {
			ChampionAugments [][]json.RawMessage `json:"championAugments"`
		}
		if json.Unmarshal(b, &doc) == nil && len(doc.ChampionAugments) > 0 && len(doc.ChampionAugments[0]) > 1 {
			var inner string
			if json.Unmarshal(doc.ChampionAugments[0][1], &inner) == nil {
				var parsed struct {
					Augments map[string]aramggAugStat `json:"augments"`
				}
				if json.Unmarshal([]byte(inner), &parsed) == nil {
					for k, v := range parsed.Augments {
						stats[atoi(k)] = v
					}
				}
			}
		}
	}
	if len(stats) == 0 {
		scope = "global"
		b, err := c.fetch(ctx, "aramgg-global.json", "https://aramgg.com/data/augments-stats-raw.json", 6*time.Hour)
		if err != nil {
			return nil, "", err
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, "", fmt.Errorf("aramgg: %w", err)
		}
		for _, row := range rows {
			if len(row) < 2 {
				continue
			}
			var id, inner string
			_ = json.Unmarshal(row[0], &id)
			_ = json.Unmarshal(row[1], &inner)
			var st aramggAugStat
			if json.Unmarshal([]byte(inner), &st) == nil {
				stats[atoi(id)] = st
			}
		}
	}
	var out []Augment
	for id, st := range stats {
		games := atoi(st.NumGames)
		a := Augment{ID: id, Name: meta.names[id], NameEN: meta.namesEN[id], Desc: meta.descs[id], Rarity: meta.rarity[id], Icon: meta.icons[id],
			Tier: atoi(st.Tier), WinRate: atof(st.WinRate), PickRate: atof(st.PickRate), Games: games, Scope: scope}
		if a.Name == "" {
			a.Name = "强化 #" + strconv.Itoa(id)
		}
		if a.Rarity == "" {
			a.Rarity = "unknown"
		}
		out = append(out, a)
	}
	// Best first within a rarity: win rate, but only trust it on a real sample;
	// thin samples fall back to aramgg's tier, then win rate.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rarity != b.Rarity {
			return rarityRank(a.Rarity) < rarityRank(b.Rarity)
		}
		const minGames = 200
		if a.Games >= minGames && b.Games >= minGames {
			if a.WinRate != b.WinRate {
				return a.WinRate > b.WinRate
			}
			return a.Games > b.Games
		}
		if (a.Games >= minGames) != (b.Games >= minGames) {
			return a.Games >= minGames
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
		return a.WinRate > b.WinRate
	})
	rateAugments(out, func(a Augment) bool { return a.Games >= 200 })
	return out, scope, nil
}

// rateAugments assigns S/A/B/C/D (Tier 1..5) from an augment's rank within its
// rarity, so the letter always agrees with the order we show. Entries the
// ranking cannot trust (thin sample, no placement data) get Tier 0: unknown.
func rateAugments(out []Augment, trusted func(Augment) bool) {
	groups := map[string][]int{}
	var order []string
	for i, a := range out {
		if _, ok := groups[a.Rarity]; !ok {
			order = append(order, a.Rarity)
		}
		groups[a.Rarity] = append(groups[a.Rarity], i)
	}
	for _, r := range order {
		idx := groups[r]
		var rated []int
		for _, i := range idx {
			if trusted(out[i]) {
				rated = append(rated, i)
			} else {
				out[i].Tier = 0
			}
		}
		n := float64(len(rated))
		for pos, i := range rated {
			p := float64(pos) / n
			switch {
			case p < 0.12:
				out[i].Tier = 1
			case p < 0.35:
				out[i].Tier = 2
			case p < 0.65:
				out[i].Tier = 3
			case p < 0.85:
				out[i].Tier = 4
			default:
				out[i].Tier = 5
			}
		}
	}
}

func rarityRank(r string) int {
	switch r {
	case "prismatic":
		return 0
	case "gold":
		return 1
	case "silver":
		return 2
	case "bronze":
		return 3
	default:
		return 4
	}
}

// ---- Arena (op.gg per-champion + blitz global augment stats) ----

type opggPlace struct {
	IDs        []int   `json:"ids"`
	Play       int     `json:"play"`
	Win        int     `json:"win"`
	TotalPlace int     `json:"total_place"`
	FirstPlace int     `json:"first_place"`
	PickRate   float64 `json:"pick_rate"`
}

type opggArena struct {
	Meta struct {
		Version string `json:"version"`
	} `json:"meta"`
	Data struct {
		Summary struct {
			AverageStats struct {
				Play       int     `json:"play"`
				Win        int     `json:"win"`
				TotalPlace int     `json:"total_place"`
				FirstPlace int     `json:"first_place"`
				PickRate   float64 `json:"pick_rate"`
				Tier       int     `json:"tier"`
				Rank       int     `json:"rank"`
			} `json:"average_stats"`
		} `json:"summary"`
		CoreItems    []opggPlace `json:"core_items"`
		Boots        []opggPlace `json:"boots"`
		StarterItems []opggPlace `json:"starter_items"`
		LastItems    []opggPlace `json:"last_items"`
		PrismItems   []opggPlace `json:"prism_items"`
		Skills       []struct {
			Order      []string `json:"order"`
			Play       int      `json:"play"`
			Win        int      `json:"win"`
			TotalPlace int      `json:"total_place"`
		} `json:"skills"`
		SkillMasteries []struct {
			IDs  []string `json:"ids"`
			Play int      `json:"play"`
			Win  int      `json:"win"`
		} `json:"skill_masteries"`
		AugmentGroup []struct {
			Rarity   int `json:"rarity"`
			Augments []struct {
				ID         int     `json:"id"`
				Win        int     `json:"win"`
				Play       int     `json:"play"`
				TotalPlace int     `json:"total_place"`
				FirstPlace int     `json:"first_place"`
				PickRate   float64 `json:"pick_rate"`
			} `json:"augments"`
		} `json:"augment_group"`
		Synergies []struct {
			ChampionID int     `json:"champion_id"`
			Play       int     `json:"play"`
			Win        int     `json:"win"`
			TotalPlace int     `json:"total_place"`
			FirstPlace int     `json:"first_place"`
			PickRate   float64 `json:"pick_rate"`
		} `json:"synergies"`
	} `json:"data"`
}

// Synergy is a partner champion that places well with this one.
type Synergy struct {
	Champion ddragon.Champion `json:"champion"`
	Games    int              `json:"games"`
	WinRate  float64          `json:"winRate"`
	AvgPlace float64          `json:"avgPlace"`
	Top1     float64          `json:"top1"`
}

func (c *Community) arenaRaw(ctx context.Context, champID int) (*opggArena, error) {
	url := fmt.Sprintf("https://lol-api-champion.op.gg/api/global/champions/arena/%d?tier=all", champID)
	b, err := c.fetch(ctx, fmt.Sprintf("opgg-arena-%d.json", champID), url, 3*time.Hour)
	if err != nil {
		return nil, err
	}
	var r opggArena
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("op.gg arena: %w", err)
	}
	return &r, nil
}

// ArenaBuild returns the op.gg Arena build for a champion.
func (c *Community) ArenaBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion) (*Build, error) {
	r, err := c.arenaRaw(ctx, champ.ID)
	if err != nil {
		return nil, err
	}
	item := func(id int) ItemRef {
		it := d.Items[id]
		return ItemRef{ID: id, Name: it.Name, Image: it.Image}
	}
	sets := func(rows []opggPlace, n int, skip map[int]bool) []ItemSet {
		var out []ItemSet
		for _, row := range rows {
			if len(out) >= n {
				break
			}
			s := ItemSet{Count: Count{Games: row.Play, Wins: row.Win}}
			if row.Play > 0 {
				s.AvgPlace = float64(row.TotalPlace) / float64(row.Play)
			}
			for _, id := range row.IDs {
				if id == 0 || skip[id] {
					continue
				}
				s.Items = append(s.Items, item(id))
			}
			if len(s.Items) > 0 {
				out = append(out, s)
			}
		}
		return out
	}
	st := r.Data.Summary.AverageStats
	out := &Build{Champion: champ, Patch: r.Meta.Version + "-arena", Role: "NONE", Mode: "arena", Source: "opgg",
		Total: Count{Games: st.Play, Wins: st.Win}, Tier: st.Tier, Rank: st.Rank, PickRate: st.PickRate}
	if st.Play > 0 {
		out.AvgPlace = float64(st.TotalPlace) / float64(st.Play)
		out.Top1 = float64(st.FirstPlace) / float64(st.Play)
	}
	out.Starting = sets(r.Data.StarterItems, 3, nil)
	out.Core = sets(r.Data.CoreItems, 4, nil)
	out.Boots = sets(r.Data.Boots, 3, nil)
	inCore := map[int]bool{}
	for _, cs := range out.Core[:min(1, len(out.Core))] {
		for _, it := range cs.Items {
			inCore[it.ID] = true
		}
	}
	out.Late = sets(r.Data.LastItems, 8, inCore)
	out.Prismatic = sets(r.Data.PrismItems, 8, nil)
	for i, sm := range r.Data.SkillMasteries {
		if i >= 3 {
			break
		}
		out.SkillOrder = append(out.SkillOrder, Skills{Order: strings.Join(sm.IDs, ">"), Count: Count{Games: sm.Play, Wins: sm.Win}})
	}
	if len(r.Data.Skills) > 0 {
		out.SkillPath = r.Data.Skills[0].Order
	}
	for i, sy := range r.Data.Synergies {
		if i >= 8 {
			break
		}
		ch, ok := d.Champions[sy.ChampionID]
		if !ok || sy.Play == 0 {
			continue
		}
		out.Synergies = append(out.Synergies, Synergy{Champion: ch, Games: sy.Play, WinRate: float64(sy.Win) / float64(sy.Play),
			AvgPlace: float64(sy.TotalPlace) / float64(sy.Play), Top1: float64(sy.FirstPlace) / float64(sy.Play)})
	}
	return out, nil
}

type blitzArenaAug struct {
	Tier     int
	AvgPlace float64
	Top1     float64
	Games    int
	Stages   map[int]struct {
		Tier     int
		AvgPlace float64
	}
}

// arenaGlobal loads blitz's global Arena augment stats (tier, avg placement, per stage).
func (c *Community) arenaGlobal(ctx context.Context) map[int]blitzArenaAug {
	out := map[int]blitzArenaAug{}
	b, err := c.fetch(ctx, "blitz-arena-augments.json", "https://data.v2.iesdev.com/api/v1/query_objects/prod/lol/arena_augments", 6*time.Hour)
	if err != nil {
		return out
	}
	var doc struct {
		Data []struct {
			AugmentID string `json:"augment_id"`
			Stats     struct {
				Tier         int     `json:"tier"`
				AvgPlacement float64 `json:"avg_placement"`
				Top1         float64 `json:"top_1_percent"`
				NumGames     int     `json:"num_games"`
				Stages       []struct {
					Stage        string  `json:"augment_stage"`
					Tier         int     `json:"tier"`
					AvgPlacement float64 `json:"avg_placement"`
				} `json:"augment_stage_stats"`
			} `json:"stats"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, row := range doc.Data {
		a := blitzArenaAug{Tier: row.Stats.Tier, AvgPlace: row.Stats.AvgPlacement, Top1: row.Stats.Top1, Games: row.Stats.NumGames,
			Stages: map[int]struct {
				Tier     int
				AvgPlace float64
			}{}}
		for _, st := range row.Stats.Stages {
			a.Stages[atoi(st.Stage)] = struct {
				Tier     int
				AvgPlace float64
			}{st.Tier, st.AvgPlacement}
		}
		out[atoi(row.AugmentID)] = a
	}
	return out
}

// arenaMeta loads Arena augment names/descriptions from blitz (keyed by id) and
// icons/rarity from CommunityDragon.
func (c *Community) arenaMeta(ctx context.Context, patch string) *augmentMeta {
	m := c.loadMeta(ctx, patch) // CDragon names/icons/rarity cover Arena ids too
	for _, loc := range blitzLocales {
		b, err := c.fetch(ctx, "blitz-arena-meta-"+loc+"-"+patch+".json", "https://utils.iesdev.com/static/json/lol/arena/"+patch+"/augments_"+loc, 24*time.Hour)
		if err != nil {
			continue
		}
		c.mu.Lock()
		m.mergeBlitz(b, loc)
		m.fillNames()
		c.mu.Unlock()
	}
	return m
}

// ArenaAugments returns Arena augment stats for a champion (op.gg, champion-specific)
// merged with blitz global tiers; sorted by rarity then average placement.
func (c *Community) ArenaAugments(ctx context.Context, patch string, champID int) ([]Augment, string, error) {
	meta := c.arenaMeta(ctx, patch)
	global := c.arenaGlobal(ctx)
	var out []Augment
	scope := "champion"
	if r, err := c.arenaRaw(ctx, champID); err == nil {
		for _, g := range r.Data.AugmentGroup {
			for _, a := range g.Augments {
				if a.Play == 0 {
					continue
				}
				aug := Augment{ID: a.ID, Name: meta.names[a.ID], NameEN: meta.namesEN[a.ID], Desc: meta.descs[a.ID], Rarity: meta.rarity[a.ID], Icon: meta.icons[a.ID],
					WinRate: float64(a.Win) / float64(a.Play), PickRate: a.PickRate, Games: a.Play, Scope: scope,
					AvgPlace: float64(a.TotalPlace) / float64(a.Play), Top1: float64(a.FirstPlace) / float64(a.Play)}
				if gb, ok := global[a.ID]; ok {
					aug.Tier = gb.Tier
				}
				if aug.Rarity == "" {
					aug.Rarity = map[int]string{1: "silver", 2: "gold", 3: "prismatic"}[g.Rarity]
				}
				out = append(out, aug)
			}
		}
	}
	if len(out) == 0 {
		scope = "global"
		for id, gb := range global {
			out = append(out, Augment{ID: id, Name: meta.names[id], NameEN: meta.namesEN[id], Desc: meta.descs[id], Rarity: meta.rarity[id], Icon: meta.icons[id],
				Tier: gb.Tier, AvgPlace: gb.AvgPlace, Top1: gb.Top1, Games: gb.Games, Scope: scope})
		}
	}
	for i := range out {
		if out[i].Name == "" {
			out[i].Name = "强化 #" + strconv.Itoa(out[i].ID)
		}
		if out[i].Rarity == "" {
			out[i].Rarity = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rarity != out[j].Rarity {
			return rarityRank(out[i].Rarity) < rarityRank(out[j].Rarity)
		}
		ai, aj := out[i].AvgPlace, out[j].AvgPlace
		if ai == 0 {
			ai = 9
		}
		if aj == 0 {
			aj = 9
		}
		if ai != aj {
			return ai < aj
		}
		return out[i].Games > out[j].Games
	})
	rateAugments(out, func(a Augment) bool { return a.AvgPlace > 0 })
	return out, scope, nil
}

// ArenaTier is a champion's standing in the Arena tier list.
type ArenaTier struct {
	Tier     int     `json:"tier"`
	Rank     int     `json:"rank"`
	AvgPlace float64 `json:"avgPlace"`
	Top1     float64 `json:"top1"`
	PickRate float64 `json:"pickRate"`
}

// ArenaTiers returns the op.gg Arena tier list keyed by champion id.
func (c *Community) ArenaTiers(ctx context.Context) (map[int]ArenaTier, error) {
	b, err := c.fetch(ctx, "opgg-arena-tiers.json", "https://lol-api-champion.op.gg/api/global/champions/arena", 3*time.Hour)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data []struct {
			ID           int `json:"id"`
			AverageStats struct {
				Play       int     `json:"play"`
				TotalPlace int     `json:"total_place"`
				FirstPlace int     `json:"first_place"`
				PickRate   float64 `json:"pick_rate"`
				Tier       int     `json:"tier"`
				Rank       int     `json:"rank"`
			} `json:"average_stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("op.gg arena tiers: %w", err)
	}
	out := map[int]ArenaTier{}
	for _, row := range doc.Data {
		if row.ID > 10000 || row.AverageStats.Play == 0 { // op.gg lists variant ids above 60000
			continue
		}
		st := row.AverageStats
		out[row.ID] = ArenaTier{Tier: st.Tier, Rank: st.Rank, PickRate: st.PickRate,
			AvgPlace: float64(st.TotalPlace) / float64(st.Play), Top1: float64(st.FirstPlace) / float64(st.Play)}
	}
	return out, nil
}
