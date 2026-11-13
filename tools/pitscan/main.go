// pitscan finds augments that champions cannot make use of ("坑"): augments
// that raise win rates on average but clearly lower this champion's, over
// enough ARAM Mayhem games (aramkit). It writes the table the app ships with
// (internal/api/pitdata.json) and a readable list (docs/避坑清单.md).
//
//	go run ./tools/pitscan -data ~/.config/ezlol
//
// Run it again after a big patch. Downloads every champion's and augment's
// statistics the first time (about 100 MB, cached under -data).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/ddragon"
)

// The thresholds of a "可能是坑" in the app (see checkPitfall).
const (
	minGap   = 0.05 // average lift minus this champion's lift
	maxMine  = -0.03
	minGames = 3000
)

type pit struct {
	Champion int     `json:"c"`
	Augment  int     `json:"a"`
	Mine     float64 `json:"mine"` // this champion's win rate with it minus its average (shrunk)
	Avg      float64 `json:"avg"`  // all champions' win rate with it minus 50%
	Games    int     `json:"g"`
}

type table struct {
	Version   string `json:"version"`
	Generated string `json:"generated"`
	Pits      []pit  `json:"pits"`
}

func main() {
	data := flag.String("data", "", "ezlol data directory (with ddragon/ and community/)")
	out := flag.String("out", "internal/api/pitdata.json", "table to write")
	doc := flag.String("doc", "docs/避坑清单.md", "readable list to write")
	flag.Parse()
	if *data == "" {
		fmt.Fprintln(os.Stderr, "need -data")
		os.Exit(2)
	}
	ctx := context.Background()
	dd := ddragon.New(filepath.Join(*data, "ddragon"))
	if err := dd.Load(ctx); err != nil {
		panic(err)
	}
	d := dd.Data()
	c := builds.NewCommunity(filepath.Join(*data, "community"))
	details := map[int]*builds.AKAugmentDetail{}
	var pits []pit
	version := ""
	for id := range d.Champions {
		ak, err := c.AramkitChampion(ctx, id)
		if err != nil || ak == nil {
			continue
		}
		version = ak.Version
		for _, a := range ak.Augments {
			if a.Games < minGames {
				continue
			}
			det, ok := details[a.ID]
			if !ok {
				det, _ = c.AramkitAugmentDetail(ctx, a.ID)
				details[a.ID] = det
			}
			if det == nil || det.WinRate == 0 {
				continue
			}
			mine := builds.Shrink(a.WinRate, a.Games, ak.WinRate) - ak.WinRate
			avg := det.WinRate - 0.5
			if avg-mine >= minGap && mine <= maxMine {
				pits = append(pits, pit{Champion: id, Augment: a.ID, Mine: round(mine), Avg: round(avg), Games: a.Games})
			}
		}
	}
	sort.Slice(pits, func(i, j int) bool {
		if pits[i].Augment != pits[j].Augment {
			return pits[i].Augment < pits[j].Augment
		}
		return pits[i].Champion < pits[j].Champion
	})
	t := table{Version: version, Generated: time.Now().Format("2006-01-02"), Pits: pits}
	b, _ := json.Marshal(t)
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		panic(err)
	}
	ids := make([]int, 0, len(details))
	for id := range details {
		ids = append(ids, id)
	}
	writeDoc(*doc, t, d, c.AugmentInfo(ctx, "", ids))
	fmt.Printf("%d pits, %d champions, %d augments, version %s\n", len(pits), len(d.Champions), len(details), version)
}

func round(f float64) float64 { return float64(int(f*10000)) / 10000 }

func writeDoc(path string, t table, d *ddragon.Data, info map[int]builds.Augment) {
	by := map[int][]pit{}
	for _, p := range t.Pits {
		by[p.Augment] = append(by[p.Augment], p)
	}
	augs := make([]int, 0, len(by))
	for a := range by {
		augs = append(augs, a)
	}
	sort.Slice(augs, func(i, j int) bool {
		if len(by[augs[i]]) != len(by[augs[j]]) {
			return len(by[augs[i]]) > len(by[augs[j]])
		}
		return augs[i] < augs[j]
	})
	rarity := map[string]string{"prismatic": "棱彩", "gold": "金色", "silver": "银色"}
	var b strings.Builder
	fmt.Fprintf(&b, "# 海克斯乱斗避坑清单（%s 版本，%s 生成）\n\n", t.Version, t.Generated)
	b.WriteString("「坑」= 这个强化在所有英雄身上平均是加分（或不怎么减分），这个英雄用它却明显减分：")
	fmt.Fprintf(&b, "所有英雄的平均提升比这个英雄的高 %.0f%% 以上，这个英雄本身降 %.0f%% 以上，并且至少 %d 场。", minGap*100, -maxMine*100, minGames)
	b.WriteString("多半是这个英雄的技能触发不了它，或者路线完全不搭。数据来自 aramkit 的海克斯乱斗对局。\n\n")
	b.WriteString("括号里是这个英雄用它的胜率和这个英雄平均胜率的差。用 `go run ./tools/pitscan -data <ezlol 数据目录>` 重新生成。\n\n")
	fmt.Fprintf(&b, "共 %d 个组合，%d 个强化。\n\n", len(t.Pits), len(augs))
	for _, a := range augs {
		ps := by[a]
		sort.Slice(ps, func(i, j int) bool { return ps[i].Mine < ps[j].Mine })
		in := info[a]
		desc := strings.Join(strings.Fields(in.Desc), " ")
		if r := []rune(desc); len(r) > 80 {
			desc = string(r[:80]) + "…"
		}
		fmt.Fprintf(&b, "## %s（%s，所有英雄平均 %+.1f%%）\n\n", in.Name, rarity[in.Rarity], ps[0].Avg*100)
		if desc != "" && !strings.Contains(desc, "{{") {
			fmt.Fprintf(&b, "%s\n\n", desc)
		}
		names := make([]string, 0, len(ps))
		for _, p := range ps {
			names = append(names, fmt.Sprintf("%s（%+.1f%%）", d.Champions[p.Champion].Name, p.Mine*100))
		}
		b.WriteString(strings.Join(names, "、") + "\n\n")
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}
