package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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
	ak   aramkitCache
}

const cdragonBase = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/default/"

// cdragonSEA is the game data in the Simplified Chinese the SEA client uses
// (zh_MY). Most augment names differ from the China server's zh_CN wording
// (e.g. 返璞归真 vs 回归基本功), and the screen shows the SEA ones.
const cdragonSEA = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/zh_my/"

// cdragonCN is the China server's Simplified Chinese (zh_CN): extra names to match.
const cdragonCN = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/zh_cn/"

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
	NameCN   string  `json:"nameCn,omitempty"` // China-server (zh_CN) name, also matched on screen
	NameEN   string  `json:"nameEn,omitempty"` // English name, also matched on screen
	Desc     string  `json:"desc"`
	Rarity   string  `json:"rarity"` // prismatic | gold | silver | bronze
	Icon     string  `json:"icon"`
	Tier     int     `json:"tier"` // 1 best .. 5
	WinRate  float64 `json:"winRate"`
	PickRate float64 `json:"pickRate"`
	Games    int     `json:"games"`
	Scope    string  `json:"scope"` // champion | global
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
	b, err := io.ReadAll(io.LimitReader(res.Body, 48<<20))
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
		CoreItems    []opggCount `json:"core_items"`
		Boots        []opggCount `json:"boots"`
		StarterItems []opggCount `json:"starter_items"`
		LastItems    []opggCount `json:"last_items"`
	} `json:"data"`
}

// opggRole maps our role names onto op.gg's path segment.
// ARAMBuild returns the op.gg ARAM build for a champion resolved against Data Dragon.
func (c *Community) ARAMBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion) (*Build, error) {
	url := fmt.Sprintf("https://lol-api-champion.op.gg/api/global/champions/aram/%d/none?tier=all", champ.ID)
	return c.opggBuild(ctx, d, champ, url, fmt.Sprintf("opgg-aram-%d.json", champ.ID))
}

func (c *Community) opggBuild(ctx context.Context, d *ddragon.Data, champ ddragon.Champion, url, cache string) (*Build, error) {
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
	out := &Build{
		Champion: champ, Patch: r.Meta.Version, Mode: "aram", Source: "opgg",
		Total:    Count{Games: st.Play, Wins: int(float64(st.Play) * st.WinRate)},
		PickRate: st.PickRate,
	}
	out.Notes = append(out.Notes, "这是 op.gg 普通大乱斗的出装（海克斯乱斗数据暂时拿不到）。")
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
	return out, nil
}

// ---- augments ----

type augmentMeta struct {
	names   map[int]string // SEA Simplified Chinese (zh_MY), falling back to zh_CN, then English
	namesCN map[int]string
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
	m := &augmentMeta{names: map[int]string{}, namesCN: map[int]string{}, namesEN: map[int]string{}, icons: map[int]string{}, rarity: map[int]string{}, descs: map[int]string{}, at: time.Now()}
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
	for _, src := range []struct {
		cache, url string
		into       map[int]string
	}{
		{"cherry-augments.zh_my.json", cdragonSEA + "v1/cherry-augments.json", m.names},
		{"cherry-augments.zh_cn.json", cdragonCN + "v1/cherry-augments.json", m.namesCN},
	} {
		b, err := c.fetch(ctx, src.cache, src.url, 24*time.Hour)
		if err != nil {
			continue
		}
		var rows []cherryRow
		if json.Unmarshal(b, &rows) == nil {
			for _, r := range rows {
				if r.Name != "" {
					src.into[r.ID] = r.Name
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
	names := m.namesCN // blitz's Chinese is the China-server wording
	if loc == "en_us" {
		names = m.namesEN
	}
	for _, r := range rows {
		// Untranslated entries carry the string key instead ("Kiwi_TapDancer_Summary").
		if d := stripTags(r.Description); d != "" && m.descs[r.ID] == "" && !reKey.MatchString(d) {
			m.descs[r.ID] = d
		}
		if names[r.ID] == "" && r.DisplayName != "" {
			names[r.ID] = r.DisplayName
		}
	}
}

// fillNames shows the zh_CN, then the English name where no SEA name is known.
func (m *augmentMeta) fillNames() {
	for _, alt := range []map[int]string{m.namesCN, m.namesEN} {
		for id, n := range alt {
			if m.names[id] == "" {
				m.names[id] = n
			}
		}
	}
}

var (
	reTag     = regexp.MustCompile(`<[^>]*>`)
	reKey     = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	reIcon    = regexp.MustCompile(`%i:[A-Za-z0-9_]+%`)
	reCalc    = regexp.MustCompile(`@[A-Za-z0-9_.:*]+@`)
	reSpaceCJ = regexp.MustCompile(`\s+([，。、：；！？）])`)
)

// stripTags drops <tags>, %i:icon% markers and @calc@ placeholders from augment
// descriptions. A plain "12%" stays.
func stripTags(s string) string {
	s = strings.ReplaceAll(s, "<br>", " ")
	s = reTag.ReplaceAllString(s, "")
	s = reIcon.ReplaceAllString(s, "")
	s = reCalc.ReplaceAllString(s, "X")
	s = strings.Join(strings.Fields(s), " ")
	// Chinese texts carry "?" where a number was not filled in ("获得?攻击距离").
	if strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		s = strings.ReplaceAll(s, "?", "")
	}
	return reSpaceCJ.ReplaceAllString(s, "$1")
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
		a := Augment{ID: id, Name: meta.names[id], NameCN: meta.namesCN[id], NameEN: meta.namesEN[id], Desc: meta.descs[id], Rarity: meta.rarity[id], Icon: meta.icons[id],
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

// AugmentInfo returns name, icon, rarity and description (no stats) for augment ids.
func (c *Community) AugmentInfo(ctx context.Context, patch string, ids []int) map[int]Augment {
	out := map[int]Augment{}
	if len(ids) == 0 {
		return out
	}
	m := c.loadMeta(ctx, patch)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		a := Augment{ID: id, Name: m.names[id], NameCN: m.namesCN[id], NameEN: m.namesEN[id], Desc: m.descs[id], Rarity: m.rarity[id], Icon: m.icons[id]}
		if a.Name == "" {
			a.Name = "强化 #" + strconv.Itoa(id)
		}
		if a.Rarity == "" {
			a.Rarity = "unknown"
		}
		out[id] = a
	}
	return out
}

// AllAugmentIDs lists every augment the game data knows (Mayhem and Arena), so
// an augment this champion has no statistics for is still recognised on screen.
func (c *Community) AllAugmentIDs(ctx context.Context, patch string) []int {
	m := c.loadMeta(ctx, patch)
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]int, 0, len(m.names))
	for id := range m.names {
		ids = append(ids, id)
	}
	return ids
}
