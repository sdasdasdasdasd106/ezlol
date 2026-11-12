package builds

import (
	"math"
	"strings"
	"testing"

	"github.com/aarlint/ezlol/internal/ddragon"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestShrink(t *testing.T) {
	if got := Shrink(0.8, 0, 0.5); got != 0.5 {
		t.Fatalf("no games: %v", got)
	}
	// 200 games at 60% next to a 50% average lands halfway.
	if got := Shrink(0.6, 200, 0.5); !near(got, 0.55) {
		t.Fatalf("shrink: %v", got)
	}
	// Big samples barely move.
	if got := Shrink(0.6, 100000, 0.5); got < 0.599 {
		t.Fatalf("big sample moved too much: %v", got)
	}
}

func fixture() (*ddragon.Data, *AKChampion, map[int]*AKAugmentBuild) {
	d := &ddragon.Data{Items: map[int]ddragon.Item{
		3006: {ID: 3006, Name: "狂战士胫甲", Tags: []string{"Boots"}},
		3031: {ID: 3031, Name: "无尽之刃"},
		3032: {ID: 3032, Name: "云台"},
		3085: {ID: 3085, Name: "卢安娜的飓风"},
		3046: {ID: 3046, Name: "幻影之舞"},
		1038: {ID: 1038, Name: "暴风之剑"}, // component: not in the data
	}}
	ch := &AKChampion{
		WinRate: 0.45,
		Stages: map[string][]AKStat{
			"2": {{ID: 100, Games: 5000, WinRate: 0.55}, {ID: 200, Games: 50, WinRate: 0.70}},
		},
		Augments: []AKStat{{ID: 100, Games: 9000, WinRate: 0.53}, {ID: 200, Games: 100, WinRate: 0.6}, {ID: 300, Games: 4000, WinRate: 0.40}},
		Pairs:    []AKPair{{AugmentIDs: []int{300, 100}, Games: 800, WinRate: 0.7}},
		Items: AKItems{All: []AKStat{
			{ID: 3031, Games: 50000, WinRate: 0.46}, {ID: 3032, Games: 50000, WinRate: 0.45}, {ID: 3006, Games: 80000, WinRate: 0.45},
		}},
	}
	abs := map[int]*AKAugmentBuild{
		100: {AugmentID: 100, Games: 9000,
			Items: AKItems{
				All: []AKStat{{ID: 3031, Games: 3000, WinRate: 0.56}, {ID: 3032, Games: 3000, WinRate: 0.55}, {ID: 3006, Games: 5000, WinRate: 0.52}, {ID: 3085, Games: 2000, WinRate: 0.54}, {ID: 3046, Games: 900, WinRate: 0.5}},
				Slots: map[string][]AKStat{
					"3": {{ID: 3085, Games: 1500, WinRate: 0.58}, {ID: 3046, Games: 800, WinRate: 0.51}, {ID: 3031, Games: 400, WinRate: 0.6}, {ID: 3006, Games: 600, WinRate: 0.7}},
				},
			},
			Builds: []AKArchetype{{Key: "crit", Profiles: []AKProfile{
				{Games: 2000, WinRate: 0.55, ItemSet: []AKRef{{3031}, {3085}, {3046}}},
				{Games: 1500, WinRate: 0.54, ItemSet: []AKRef{{3032}, {3031}, {3085}},
					Routes: []AKRoute{{PurchaseOrder: []AKRef{{3032}, {3031}, {3085}}}}},
			}}},
		},
	}
	return d, ch, abs
}

func TestStageAndPair(t *testing.T) {
	_, ch, _ := fixture()
	if r, ok := ch.StageStat(2, 100); !ok || r.WinRate != 0.55 {
		t.Fatalf("stage stat: %v %v", r, ok)
	}
	if r, ok := ch.StageStat(4, 300); !ok || r.WinRate != 0.40 { // no stage 4 rows: all-stage record
		t.Fatalf("fallback: %v %v", r, ok)
	}
	if _, _, ok := ch.BestPair(300, nil); ok {
		t.Fatal("pair without picks")
	}
	if p, partner, ok := ch.BestPair(300, []int{100}); !ok || partner != 100 || p.WinRate != 0.7 {
		t.Fatalf("pair: %v %v %v", p, partner, ok)
	}
}

func TestItemFit(t *testing.T) {
	_, ch, abs := fixture()
	fit, n, ok := ItemFit(ch, abs[100], []int{3031, 3032, 1038})
	if !ok || n != 6000 || fit < 0.05 {
		t.Fatalf("fit: %v %v %v", fit, n, ok)
	}
	if _, _, ok := ItemFit(ch, abs[100], []int{1038}); ok {
		t.Fatal("components alone must not count")
	}
}

// routeFixture: champion with one augment (2129) whose players mostly go AP,
// some go bruiser with Trinity Force (3078).
func routeFixture() (*ddragon.Data, *AKChampion, map[int]*AKAugmentBuild) {
	d := &ddragon.Data{Items: map[int]ddragon.Item{
		3078: {ID: 3078, Name: "三相之力"}, 3053: {ID: 3053, Name: "斯特拉克的挑战护手"}, 6631: {ID: 6631, Name: "挺进破坏者"},
		4005: {ID: 4005, Name: "帝王命令"}, 2503: {ID: 2503, Name: "黑焰火炬"}, 6653: {ID: 6653, Name: "兰德里的折磨"},
		3047: {ID: 3047, Name: "铁板靴", Tags: []string{"Boots"}}, 3020: {ID: 3020, Name: "法师之靴", Tags: []string{"Boots"}},
		4403: {ID: 4403, Name: "黄金锅铲"}, 3065: {ID: 3065, Name: "振奋盔甲"},
	}}
	ch := &AKChampion{WinRate: 0.51, Items: AKItems{Slots: map[string][]AKStat{"2": {{ID: 1, Games: 1000, WinRate: 0.52}}}}}
	route := func(n int, wr float64, order []int, later [][2]int, boots []int) AKRoute {
		r := AKRoute{Games: n, WinRate: wr}
		for _, id := range order {
			r.PurchaseOrder = append(r.PurchaseOrder, AKRef{id})
		}
		for _, l := range later {
			r.LaterItems = append(r.LaterItems, AKPick{Games: l[1], WinRate: wr, Item: AKRef{l[0]}})
		}
		for _, b := range boots {
			r.Boots = append(r.Boots, AKPick{Games: n / 2, WinRate: wr, Item: AKRef{b}})
		}
		return r
	}
	ab := &AKAugmentBuild{AugmentID: 2129, Games: 50000,
		Items: AKItems{
			All: []AKStat{{ID: 3078, Games: 5000}, {ID: 4005, Games: 30000}, {ID: 3047, Games: 9000}, {ID: 3020, Games: 20000}, {ID: 2503, Games: 20000}, {ID: 3053, Games: 2000}, {ID: 6631, Games: 900}},
			// Slot 2 overall: dominated by AP items, plus a 65-game fluke.
			Slots: map[string][]AKStat{"2": {{ID: 2503, Games: 15000, WinRate: 0.58}, {ID: 4403, Games: 65, WinRate: 0.8}, {ID: 3053, Games: 600, WinRate: 0.55}}},
		},
		Builds: []AKArchetype{
			{Key: "ap", Games: 40000, WinRate: 0.55, Profiles: []AKProfile{
				{Games: 30000, WinRate: 0.56, ItemSet: []AKRef{{4005}, {2503}, {6653}}, Routes: []AKRoute{route(30000, 0.56, []int{4005, 2503, 6653}, nil, []int{3020})}},
			}},
			{Key: "bruiser", Games: 8000, WinRate: 0.53, Profiles: []AKProfile{
				{Games: 3000, WinRate: 0.54, ItemSet: []AKRef{{3078}, {3053}, {6631}}, Routes: []AKRoute{
					route(2000, 0.545, []int{3078, 3053, 6631}, [][2]int{{3065, 900}}, []int{3047}),
					route(800, 0.53, []int{3078, 6631, 3053}, nil, []int{3047}),
				}},
			}},
		},
	}
	return d, ch, map[int]*AKAugmentBuild{2129: ab}
}

func TestPlanFollowsYourPath(t *testing.T) {
	d, ch, abs := routeFixture()
	// Trinity Force + boots bought: next comes from players who also did that
	// (bruiser routes), not from the AP majority, and boots are not repeated.
	p := PlanAugBuild(d, ch, abs, []int{2129}, []int{3078, 3047})
	if p.Basis != "path" || p.Slot != 3 {
		t.Fatalf("basis %s slot %d", p.Basis, p.Slot)
	}
	if len(p.Next) == 0 || p.Next[0].Item.ID != 3053 {
		t.Fatalf("next: %+v", p.Next)
	}
	for _, it := range p.Next {
		if it.Item.ID == 2503 || it.Item.ID == 4005 {
			t.Fatalf("AP item suggested on a bruiser path: %+v", p.Next)
		}
	}
	if len(p.Boots) != 0 {
		t.Fatalf("boots suggested while owning boots: %+v", p.Boots)
	}
	// Direction: players mostly go AP; your items are bruiser, which wins less here.
	dir := p.Direction
	if dir == nil || dir.Popular.Style != "ap" || dir.Best.Style != "ap" || dir.Yours == nil || dir.Yours.Style != "bruiser" || !dir.Conflict {
		t.Fatalf("direction: %+v", dir)
	}
}

func TestPlanCoreDoneUsesLaterItems(t *testing.T) {
	d, ch, abs := routeFixture()
	p := PlanAugBuild(d, ch, abs, []int{2129}, []int{3078, 3053, 6631, 3047})
	if p.Basis != "path" || len(p.Next) == 0 || p.Next[0].Item.ID != 3065 {
		t.Fatalf("after the core: %s %+v", p.Basis, p.Next)
	}
}

func TestPlanNothingBoughtYet(t *testing.T) {
	d, ch, abs := routeFixture()
	p := PlanAugBuild(d, ch, abs, []int{2129}, nil)
	// First items of all routes, weighted by games: AP's first item leads; boots offered.
	if p.Basis != "path" || p.Next[0].Item.ID != 4005 || len(p.Boots) == 0 {
		t.Fatalf("start: %s %+v boots %+v", p.Basis, p.Next, p.Boots)
	}
	if p.Direction.Yours != nil || p.Direction.Note == "" {
		t.Fatalf("direction with no items: %+v", p.Direction)
	}
}

func TestPlanFallbackSkipsSmallSamples(t *testing.T) {
	d, ch, abs := routeFixture()
	// A finished item that no route contains: fall back to everyone with the
	// augment (slot 2); the 65-game 80% fluke must not be recommended.
	abs[2129].Items.All = append(abs[2129].Items.All, AKStat{ID: 9999, Games: 300})
	p := PlanAugBuild(d, ch, abs, []int{2129}, []int{9999})
	if p.Basis != "all" || p.Slot != 2 || len(p.Next) == 0 || p.Next[0].Item.ID != 2503 {
		t.Fatalf("fallback: %s slot %d %+v", p.Basis, p.Slot, p.Next)
	}
	for _, it := range p.Next {
		if it.Item.ID == 4403 {
			t.Fatal("small-sample item recommended")
		}
	}
	if empty := PlanAugBuild(d, ch, abs, []int{999}, nil); len(empty.Next) != 0 || empty.Slot != 0 {
		t.Fatalf("unknown augment: %+v", empty)
	}
}

func TestStripTags(t *testing.T) {
	cases := map[string]string{
		"<crit>暴击</crit><lifeSteal>造成伤害的12%会等额治疗你自身</lifeSteal>。<br><br>获得<crit>%i:scaleCrit% 25%暴击几率</crit>。": "暴击造成伤害的12%会等额治疗你自身。 获得 25%暴击几率。",
		"Gain <attackSpeed>%i:scaleAS% Attack Speed</attackSpeed> per @Stacks@ stacks.":                        "Gain Attack Speed per X stacks.",
	}
	for in, want := range cases {
		if got := stripTags(in); got != want {
			t.Fatalf("stripTags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanFallsBackToChampionRoutes(t *testing.T) {
	d, ch, abs := routeFixture()
	// Nobody with this augment goes bruiser; this champion normally does.
	abs[2129].Builds = abs[2129].Builds[:1] // AP only
	ch.Builds = []AKArchetype{{Key: "bruiser", Games: 200000, WinRate: 0.54, Profiles: []AKProfile{
		{Games: 13000, WinRate: 0.57, ItemSet: []AKRef{{3078}, {3053}, {6631}}, Routes: []AKRoute{
			{Games: 9000, WinRate: 0.57, PurchaseOrder: []AKRef{{3078}, {3053}, {6631}}},
		}},
	}}}
	p := PlanAugBuild(d, ch, abs, []int{2129}, []int{3078, 3047})
	if p.Basis != "champion" || len(p.Next) == 0 || p.Next[0].Item.ID != 3053 {
		t.Fatalf("champion fallback: %s %+v", p.Basis, p.Next)
	}
	dir := p.Direction
	if dir == nil || !dir.Conflict || dir.Yours == nil || dir.Yours.Style != "bruiser" || !strings.Contains(dir.Note, "几乎没人") {
		t.Fatalf("direction: %+v", dir)
	}
}

// No augment taken yet (start of a game): the plan uses the champion's own builds.
func TestPlanWithoutAugments(t *testing.T) {
	d, ch, abs := fixture()
	if p := PlanAugBuild(d, ch, abs, nil, nil); len(p.Cores) != 0 {
		t.Fatalf("champion without builds should give no plan: %+v", p.Cores)
	}
	ch.Builds = abs[100].Builds
	p := PlanAugBuild(d, ch, abs, nil, nil)
	if len(p.Cores) == 0 || p.Cores[0].Items[0].ID == 0 {
		t.Fatalf("no cores from the champion's builds: %+v", p)
	}
}
