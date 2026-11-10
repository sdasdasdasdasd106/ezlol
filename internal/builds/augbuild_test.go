package builds

import (
	"math"
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

func TestPlanAugBuild(t *testing.T) {
	d, ch, abs := fixture()
	// Own one finished item, boots and a component: the third completed item is
	// next; owned items and (since boots are owned) other boots are skipped.
	plan := PlanAugBuild(d, ch, abs, []int{100}, []int{3031, 3006, 1038})
	if plan.Slot != 3 || plan.SlotBase != 0.45 { // fixture: no slot-3 rows for the champion, so the overall average

		t.Fatalf("slot: %d", plan.Slot)
	}
	if len(plan.Next) != 2 || plan.Next[0].Item.ID != 3085 || plan.Next[1].Item.ID != 3046 {
		t.Fatalf("next: %+v", plan.Next)
	}
	// Both core sets contain the owned item; the better one leads.
	if len(plan.Cores) != 2 || plan.Cores[0].Owned != 1 || plan.Cores[0].Items[0].ID != 3031 {
		t.Fatalf("cores: %+v", plan.Cores)
	}
	// The set matching more of your items comes first, in purchase order.
	two := PlanAugBuild(d, ch, abs, []int{100}, []int{3032, 3031})
	if two.Cores[0].Owned != 2 || two.Cores[0].Items[0].ID != 3032 {
		t.Fatalf("cores by owned: %+v", two.Cores)
	}
	// Without boots, boots are a fine suggestion.
	if two.Next[0].Item.ID != 3006 {
		t.Fatalf("boots: %+v", two.Next)
	}
	withBoots := PlanAugBuild(d, ch, abs, []int{100}, []int{3031, 3006})
	for _, it := range withBoots.Next {
		if it.Item.ID == 3006 {
			t.Fatal("boots suggested twice")
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

func TestSlotBaseline(t *testing.T) {
	d, ch, abs := fixture()
	// Third items win more often on their own; the lift must be measured against that.
	ch.Items.Slots = map[string][]AKStat{"3": {{ID: 1, Games: 1000, WinRate: 0.60}, {ID: 2, Games: 1000, WinRate: 0.50}}}
	plan := PlanAugBuild(d, ch, abs, []int{100}, []int{3031, 3006})
	if !near(plan.SlotBase, 0.55) {
		t.Fatalf("slot base: %v", plan.SlotBase)
	}
	if !near(plan.Next[0].Lift, plan.Next[0].WinRate-0.55) {
		t.Fatalf("lift: %+v", plan.Next[0])
	}
}
