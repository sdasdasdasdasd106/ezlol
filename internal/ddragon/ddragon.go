// Package ddragon loads static game data from Riot's Data Dragon CDN and caches
// it on disk per version.
package ddragon

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
)

const cdn = "https://ddragon.leagueoflegends.com"

// locale is the Data Dragon language: champion, item, rune and spell names in
// Simplified Chinese. Keys and ids stay the same in every language.
const locale = "zh_CN"

// Champion is a trimmed champion record.
type Champion struct {
	ID    int      `json:"id"`
	Key   string   `json:"key"` // e.g. "Ahri"
	Name  string   `json:"name"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Image string   `json:"image"`
	// Riot's 0-10 damage profile hints.
	Attack int `json:"attack"`
	Magic  int `json:"magic"`
}

// Item is a trimmed item record.
type Item struct {
	ID        int                `json:"id"`
	Name      string             `json:"name"`
	Plaintext string             `json:"plaintext"`
	Gold      int                `json:"gold"`
	Into      []string           `json:"-"`
	From      []string           `json:"-"`
	Tags      []string           `json:"tags"`
	Depth     int                `json:"-"`
	Image     string             `json:"image"`
	Maps      map[string]bool    `json:"-"`
	Stats     map[string]float64 `json:"-"`
}

// Rune is a single perk.
type Rune struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortDesc string `json:"shortDesc"`
	Icon      string `json:"icon"`
	StyleID   int    `json:"styleId"`
	Slot      int    `json:"slot"`
}

// Style is a rune tree.
type Style struct {
	ID    int      `json:"id"`
	Key   string   `json:"key"`
	Name  string   `json:"name"`
	Icon  string   `json:"icon"`
	Slots [][]Rune `json:"slots"`
}

// Spell is a summoner spell.
type Spell struct {
	ID       int     `json:"id"`
	Key      string  `json:"key"`
	Name     string  `json:"name"`
	Image    string  `json:"image"`
	Cooldown float64 `json:"cooldown"`
}

// Data is one version's worth of static data.
type Data struct {
	Version   string
	Champions map[int]Champion
	Items     map[int]Item
	Runes     map[int]Rune
	Styles    map[int]Style
	Spells    map[int]Spell
}

// Store loads and caches Data Dragon data.
type Store struct {
	dir  string
	http *http.Client
	mu   sync.RWMutex
	data *Data
}

// New creates a store caching under dir.
func New(dir string) *Store {
	return &Store{dir: dir, http: &http.Client{Timeout: 30 * time.Second}}
}

// Data returns the loaded data (nil before Load).
func (s *Store) Data() *Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// LatestVersion queries the CDN for the newest version string.
func (s *Store) LatestVersion(ctx context.Context) (string, error) {
	var versions []string
	if err := s.getJSON(ctx, cdn+"/api/versions.json", &versions); err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("ddragon: empty version list")
	}
	return versions[0], nil
}

// Load fetches the latest version (or serves from disk cache) and parses it.
func (s *Store) Load(ctx context.Context) error {
	ver, err := s.LatestVersion(ctx)
	if err != nil {
		// offline: fall back to newest cached version
		ver, err = s.newestCached()
		if err != nil {
			return err
		}
	}
	if d := s.Data(); d != nil && d.Version == ver {
		return nil
	}
	d, err := s.load(ctx, ver)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.data = d
	s.mu.Unlock()
	return nil
}

func (s *Store) newestCached() (string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return "", fmt.Errorf("ddragon: no network and no cache: %w", err)
	}
	var vers []string
	for _, e := range entries {
		if e.IsDir() {
			vers = append(vers, e.Name())
		}
	}
	if len(vers) == 0 {
		return "", fmt.Errorf("ddragon: no network and no cache")
	}
	sort.Strings(vers)
	return vers[len(vers)-1], nil
}

func (s *Store) load(ctx context.Context, ver string) (*Data, error) {
	d := &Data{Version: ver, Champions: map[int]Champion{}, Items: map[int]Item{}, Runes: map[int]Rune{}, Styles: map[int]Style{}, Spells: map[int]Spell{}}
	base := fmt.Sprintf("%s/cdn/%s/data/%s/", cdn, ver, locale)

	var champ struct {
		Data map[string]struct {
			Key   string   `json:"key"`
			ID    string   `json:"id"`
			Name  string   `json:"name"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
			Info  struct {
				Attack int `json:"attack"`
				Magic  int `json:"magic"`
			} `json:"info"`
			Image struct {
				Full string `json:"full"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := s.cached(ctx, ver, "champion."+locale+".json", base+"champion.json", &champ); err != nil {
		return nil, err
	}
	for _, c := range champ.Data {
		id, _ := strconv.Atoi(c.Key)
		d.Champions[id] = Champion{ID: id, Key: c.ID, Name: c.Name, Title: c.Title, Tags: c.Tags, Attack: c.Info.Attack, Magic: c.Info.Magic,
			Image: fmt.Sprintf("%s/cdn/%s/img/champion/%s", cdn, ver, c.Image.Full)}
	}

	var items struct {
		Data map[string]struct {
			Name      string             `json:"name"`
			Plaintext string             `json:"plaintext"`
			Into      []string           `json:"into"`
			From      []string           `json:"from"`
			Tags      []string           `json:"tags"`
			Depth     int                `json:"depth"`
			Maps      map[string]bool    `json:"maps"`
			Stats     map[string]float64 `json:"stats"`
			Gold      struct {
				Total       int  `json:"total"`
				Purchasable bool `json:"purchasable"`
			} `json:"gold"`
			Image struct {
				Full string `json:"full"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := s.cached(ctx, ver, "item."+locale+".json", base+"item.json", &items); err != nil {
		return nil, err
	}
	for k, it := range items.Data {
		id, _ := strconv.Atoi(k)
		d.Items[id] = Item{ID: id, Name: it.Name, Plaintext: it.Plaintext, Gold: it.Gold.Total, Into: it.Into, From: it.From,
			Tags: it.Tags, Depth: it.Depth, Maps: it.Maps, Stats: it.Stats,
			Image: fmt.Sprintf("%s/cdn/%s/img/item/%s", cdn, ver, it.Image.Full)}
	}

	var runes []struct {
		ID    int    `json:"id"`
		Key   string `json:"key"`
		Name  string `json:"name"`
		Icon  string `json:"icon"`
		Slots []struct {
			Runes []struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				Icon      string `json:"icon"`
				ShortDesc string `json:"shortDesc"`
			} `json:"runes"`
		} `json:"slots"`
	}
	if err := s.cached(ctx, ver, "runesReforged."+locale+".json", base+"runesReforged.json", &runes); err != nil {
		return nil, err
	}
	for _, st := range runes {
		style := Style{ID: st.ID, Key: st.Key, Name: st.Name, Icon: cdn + "/cdn/img/" + st.Icon}
		for si, slot := range st.Slots {
			var row []Rune
			for _, r := range slot.Runes {
				rn := Rune{ID: r.ID, Name: r.Name, ShortDesc: r.ShortDesc, Icon: cdn + "/cdn/img/" + r.Icon, StyleID: st.ID, Slot: si}
				d.Runes[r.ID] = rn
				row = append(row, rn)
			}
			style.Slots = append(style.Slots, row)
		}
		d.Styles[st.ID] = style
	}
	for id, sh := range statShards {
		d.Runes[id] = sh
	}

	var spells struct {
		Data map[string]struct {
			Key      string    `json:"key"`
			ID       string    `json:"id"`
			Name     string    `json:"name"`
			Cooldown []float64 `json:"cooldown"`
			Image    struct {
				Full string `json:"full"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := s.cached(ctx, ver, "summoner."+locale+".json", base+"summoner.json", &spells); err != nil {
		return nil, err
	}
	for _, sp := range spells.Data {
		id, _ := strconv.Atoi(sp.Key)
		cd := 0.0
		if len(sp.Cooldown) > 0 {
			cd = sp.Cooldown[0]
		}
		d.Spells[id] = Spell{ID: id, Key: sp.ID, Name: sp.Name, Cooldown: cd, Image: fmt.Sprintf("%s/cdn/%s/img/spell/%s", cdn, ver, sp.Image.Full)}
	}
	return d, nil
}

// Stat shards are not in runesReforged.json; icons come from the perk-images tree.
var statShards = map[int]Rune{
	5001: {ID: 5001, Name: "成长生命值", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsHealthScalingIcon.png"},
	5002: {ID: 5002, Name: "护甲", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsArmorIcon.png"},
	5003: {ID: 5003, Name: "魔法抗性", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsMagicResIcon.MagicResist_Fix.png"},
	5005: {ID: 5005, Name: "攻击速度", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsAttackSpeedIcon.png"},
	5007: {ID: 5007, Name: "技能急速", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsCDRScalingIcon.png"},
	5008: {ID: 5008, Name: "适应之力", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsAdaptiveForceIcon.png"},
	5010: {ID: 5010, Name: "移动速度", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsMovementSpeedIcon.png"},
	5011: {ID: 5011, Name: "生命值", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsHealthPlusIcon.png"},
	5013: {ID: 5013, Name: "韧性与减速抗性", Icon: cdn + "/cdn/img/perk-images/StatMods/StatModsTenacityIcon.png"},
}

func (s *Store) cached(ctx context.Context, ver, name, url string, out any) error {
	path := filepath.Join(s.dir, ver, name)
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, out) == nil {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("ddragon %s: %w", name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ddragon %s: status %d", name, res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("ddragon %s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
	return nil
}

func (s *Store) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ddragon: %s status %d", url, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// IsCompleted reports whether an item is a finished (legendary/mythic-tier) item
// on Summoner's Rift: purchasable, builds into nothing, and costs enough to not
// be a component. Boots are excluded; see IsBoots.
func (d *Data) IsCompleted(id int) bool {
	it, ok := d.Items[id]
	if !ok || d.IsBoots(id) {
		return false
	}
	if len(it.Into) > 0 || it.Gold < 2000 {
		return false
	}
	for _, t := range it.Tags {
		if t == "Consumable" || t == "Trinket" {
			return false
		}
	}
	return true
}

// IsBoots reports whether an item is a tier-2+ boot.
func (d *Data) IsBoots(id int) bool {
	it, ok := d.Items[id]
	if !ok {
		return false
	}
	hasTag := false
	for _, t := range it.Tags {
		if t == "Boots" {
			hasTag = true
		}
	}
	return hasTag && it.Gold >= 900
}

// ChampionByKey finds a champion by its string key, case-insensitively.
func (d *Data) ChampionByKey(key string) (Champion, bool) {
	for _, c := range d.Champions {
		if strings.EqualFold(c.Key, key) {
			return c, true
		}
	}
	return Champion{}, false
}

// SpellByName finds a summoner spell by display name (e.g. "Flash").
func (d *Data) SpellByName(name string) (Spell, bool) {
	for _, s := range d.Spells {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Spell{}, false
}

// ChampionSpell is one ability with its per-rank cooldowns.
type ChampionSpell struct {
	Key       string    `json:"key"` // Q W E R
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	Cooldowns []float64 `json:"cooldowns"`
	Costs     []float64 `json:"costs"`
	MaxRank   int       `json:"maxRank"`
	Tooltip   string    `json:"tooltip"`
}

// ChampionPassive is the passive ability.
type ChampionPassive struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	Desc  string `json:"desc"`
}

// ChampionDetail is the per-champion file's ability data.
type ChampionDetail struct {
	Passive ChampionPassive `json:"passive"`
	Spells  []ChampionSpell `json:"spells"`
}

// ChampionSpells loads champion/{Key}.json (cached on disk) and returns abilities.
func (s *Store) ChampionSpells(ctx context.Context, key string) (*ChampionDetail, error) {
	d := s.Data()
	if d == nil {
		return nil, fmt.Errorf("ddragon: not loaded")
	}
	var raw struct {
		Data map[string]struct {
			Passive struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Image       struct {
					Full string `json:"full"`
				} `json:"image"`
			} `json:"passive"`
			Spells []struct {
				ID       string    `json:"id"`
				Name     string    `json:"name"`
				Tooltip  string    `json:"tooltip"`
				Maxrank  int       `json:"maxrank"`
				Cooldown []float64 `json:"cooldown"`
				Cost     []float64 `json:"cost"`
				Image    struct {
					Full string `json:"full"`
				} `json:"image"`
			} `json:"spells"`
		} `json:"data"`
	}
	url := fmt.Sprintf("%s/cdn/%s/data/%s/champion/%s.json", cdn, d.Version, locale, key)
	if err := s.cached(ctx, d.Version, "champion-"+key+"."+locale+".json", url, &raw); err != nil {
		return nil, err
	}
	c, ok := raw.Data[key]
	if !ok {
		return nil, fmt.Errorf("ddragon: no data for %s", key)
	}
	out := &ChampionDetail{Passive: ChampionPassive{Name: c.Passive.Name, Desc: c.Passive.Description,
		Image: fmt.Sprintf("%s/cdn/%s/img/passive/%s", cdn, d.Version, c.Passive.Image.Full)}}
	keys := []string{"Q", "W", "E", "R"}
	for i, sp := range c.Spells {
		k := ""
		if i < len(keys) {
			k = keys[i]
		}
		out.Spells = append(out.Spells, ChampionSpell{Key: k, Name: sp.Name, Cooldowns: sp.Cooldown, Costs: sp.Cost, MaxRank: sp.Maxrank, Tooltip: sp.Tooltip,
			Image: fmt.Sprintf("%s/cdn/%s/img/spell/%s", cdn, d.Version, sp.Image.Full)})
	}
	return out, nil
}
