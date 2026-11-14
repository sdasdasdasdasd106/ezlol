package api

import (
	"strings"
	"testing"

	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/lcu"
)

func brow(id int, name string, wr float64, games int) benchRow {
	return benchRow{Champion: ddragon.Champion{ID: id, Name: name}, HasData: games > 0, WinRate: wr, Games: games}
}

func withPoints(r benchRow, pts int) benchRow {
	r.Mastery = &lcu.Mastery{ChampionID: r.Champion.ID, ChampionPoints: pts}
	return r
}

func order(a benchAdvice) []int {
	var ids []int
	for _, r := range a.Rows {
		ids = append(ids, r.Champion.ID)
	}
	return ids
}

func TestBenchSuggestsBetterChampion(t *testing.T) {
	a := adviseBench(benchInput{
		Current: brow(1, "艾希", 0.509, 500000),
		Bench:   []benchRow{brow(2, "安妮", 0.495, 400000), brow(3, "蕾欧娜", 0.541, 300000)},
		Average: 0.5, Rerolls: 2,
	})
	if a.Kind != "swap" || a.Target != 3 {
		t.Fatalf("kind %q target %d, want swap to 3", a.Kind, a.Target)
	}
	if a.Verdict != "建议换成 蕾欧娜：胜率 54.1%，比你现在的 艾希 高 3.2%" {
		t.Fatalf("verdict %q", a.Verdict)
	}
	if got := order(a); len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("order %v, want [3 1 2]", got)
	}
	if !a.Rows[1].Current || a.Rows[0].Current {
		t.Fatal("current champion not marked")
	}
	if a.Note != "" {
		t.Fatalf("unexpected note %q", a.Note)
	}
}

func TestBenchSwapWarnsWhenUnfamiliar(t *testing.T) {
	a := adviseBench(benchInput{
		Current: withPoints(brow(1, "艾希", 0.50, 500000), 120000),
		Bench:   []benchRow{withPoints(brow(3, "蕾欧娜", 0.53, 300000), 0)},
		Average: 0.5, Rerolls: 0,
	})
	if a.Kind != "swap" || !strings.Contains(a.Note, "没怎么玩过 蕾欧娜") {
		t.Fatalf("kind %q note %q", a.Kind, a.Note)
	}
	// Recent games on it count as having played it.
	b := withPoints(brow(3, "蕾欧娜", 0.53, 300000), 0)
	b.Record = &Record{Games: 5, Wins: 3}
	a = adviseBench(benchInput{Current: withPoints(brow(1, "艾希", 0.50, 500000), 120000), Bench: []benchRow{b}, Average: 0.5})
	if a.Note != "" {
		t.Fatalf("note %q despite recent games", a.Note)
	}
}

func TestBenchGapTooSmall(t *testing.T) {
	a := adviseBench(benchInput{
		Current: brow(1, "艾希", 0.520, 500000),
		Bench:   []benchRow{brow(2, "安妮", 0.531, 400000)},
		Average: 0.5, Rerolls: 1,
	})
	if a.Kind != "keep" || a.Target != 0 {
		t.Fatalf("kind %q target %d, want keep", a.Kind, a.Target)
	}
	if !strings.HasPrefix(a.Verdict, "留着 艾希 就行，板凳上没有明显更好的") || !strings.Contains(a.Verdict, "安妮 只高 1.1%") {
		t.Fatalf("verdict %q", a.Verdict)
	}
	if a.Rows[0].Champion.ID != 2 {
		t.Fatal("higher win rate should still be listed first")
	}
}

func TestBenchLowSampleNotRecommended(t *testing.T) {
	a := adviseBench(benchInput{
		Current: brow(1, "艾希", 0.50, 500000),
		Bench:   []benchRow{brow(2, "新英雄", 0.60, 800), brow(3, "安妮", 0.505, 400000)},
		Average: 0.5, Rerolls: 0,
	})
	if a.Kind != "keep" {
		t.Fatalf("kind %q, want keep (the 60%% one has 800 games)", a.Kind)
	}
	for _, r := range a.Rows {
		if r.Champion.ID == 2 && !r.LowSample {
			t.Fatal("thin sample not flagged")
		}
		if r.Champion.ID == 3 && r.LowSample {
			t.Fatal("solid sample flagged")
		}
	}
	// A champion with no numbers at all sorts last and is flagged.
	a = adviseBench(benchInput{Current: brow(1, "艾希", 0.50, 500000), Bench: []benchRow{brow(4, "无数据", 0, 0)}, Average: 0.5})
	if last := a.Rows[len(a.Rows)-1]; last.Champion.ID != 4 || !last.LowSample || last.HasData {
		t.Fatalf("no-data row %+v", last)
	}
	// Your own champion on a thin sample: no comparison.
	a = adviseBench(benchInput{Current: brow(1, "艾希", 0.45, 500), Bench: []benchRow{brow(3, "安妮", 0.52, 400000)}, Average: 0.5, Rerolls: 1})
	if a.Kind != "unsure" || !strings.Contains(a.Verdict, "安妮（52.0%）") {
		t.Fatalf("kind %q verdict %q", a.Kind, a.Verdict)
	}
}

func TestBenchEmpty(t *testing.T) {
	a := adviseBench(benchInput{Current: brow(1, "艾希", 0.52, 500000), Average: 0.5, Rerolls: 1})
	if a.Kind != "empty" || len(a.Rows) != 1 || !a.Rows[0].Current {
		t.Fatalf("kind %q rows %d", a.Kind, len(a.Rows))
	}
	if a.Verdict != "板凳席还是空的，艾希 胜率 52.0%" {
		t.Fatalf("verdict %q", a.Verdict)
	}
	// Empty bench and a below-average champion with rerolls left: reroll.
	a = adviseBench(benchInput{Current: brow(1, "艾希", 0.47, 500000), Average: 0.5, Rerolls: 1})
	if a.Kind != "reroll" || !strings.HasPrefix(a.Verdict, "板凳席还是空的，可以骰一次（还剩 1 次）") {
		t.Fatalf("kind %q verdict %q", a.Kind, a.Verdict)
	}
}

func TestBenchSuggestsReroll(t *testing.T) {
	in := benchInput{
		Current: brow(1, "艾希", 0.470, 500000),
		Bench:   []benchRow{brow(2, "安妮", 0.480, 400000)},
		Average: 0.496, Rerolls: 2,
	}
	a := adviseBench(in)
	if a.Kind != "reroll" || !strings.HasPrefix(a.Verdict, "可以骰一次（还剩 2 次）") || !strings.Contains(a.Verdict, "低于平均 49.6%") {
		t.Fatalf("kind %q verdict %q", a.Kind, a.Verdict)
	}
	// No rerolls left, or the count unknown: no reroll line.
	for _, n := range []int{0, -1} {
		in.Rerolls = n
		if a := adviseBench(in); a.Kind != "keep" {
			t.Fatalf("rerolls %d: kind %q", n, a.Kind)
		}
	}
	// Above average: keep, even with rerolls.
	in.Rerolls, in.Current.WinRate = 2, 0.51
	if a := adviseBench(in); a.Kind != "keep" {
		t.Fatalf("above average: kind %q", a.Kind)
	}
	// A clearly better bench champion wins over rerolling.
	in.Current.WinRate = 0.45
	if a := adviseBench(in); a.Kind != "swap" || a.Target != 2 {
		t.Fatalf("better bench: kind %q", a.Kind)
	}
}

func TestBenchNoData(t *testing.T) {
	a := adviseBench(benchInput{
		Current: brow(1, "艾希", 0, 0),
		Bench:   []benchRow{brow(2, "安妮", 0, 0)},
		Average: 0.5, Rerolls: 1,
	})
	if a.Kind != "" || a.Verdict != "" || a.Target != 0 {
		t.Fatalf("kind %q verdict %q with no data", a.Kind, a.Verdict)
	}
	if len(a.Rows) != 2 || !a.Rows[0].Current {
		t.Fatalf("rows %v: current first when nothing has data", order(a))
	}
	// No champion yet (just entered champ select): list the bench, no verdict.
	a = adviseBench(benchInput{Bench: []benchRow{brow(2, "安妮", 0.52, 400000)}, Average: 0.5})
	if a.Kind != "" || len(a.Rows) != 1 {
		t.Fatalf("kind %q rows %d", a.Kind, len(a.Rows))
	}
}
