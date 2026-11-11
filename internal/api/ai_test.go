package api

import (
	"slices"
	"testing"

	"github.com/aarlint/ezlol/internal/screen"
)

func TestTextReadShortlist(t *testing.T) {
	// A card screen with misread titles: 返璞归具 (真 → 具), 死亡循坏 (环 → 坏).
	lines := []screen.Line{
		{Text: "返璞归具", Conf: 0.9, X: 0.25, Y: 0.40, W: 0.1},
		{Text: "死亡循坏", Conf: 0.9, X: 0.45, Y: 0.40, W: 0.1},
		{Text: "赴险如", Conf: 0.9, X: 0.65, Y: 0.40, W: 0.1},
		{Text: "你的普攻会对敌人造成额外伤害", Conf: 0.9, X: 0.25, Y: 0.50, W: 0.2},
		{Text: "击杀", Conf: 0.9, X: 0.02, Y: 0.03, W: 0.05}, // top bar: outside the card area
	}
	band := bandLines(lines)
	if len(band) != 4 || !worthAsking(band) {
		t.Fatalf("band: %+v", band)
	}
	names := map[string]int{"返璞归真": 1, "死亡循环": 2, "赴险如夷": 3, "大法师": 4, "巨人杀手": 5}
	got := shortlist(band, names)
	for _, want := range []string{"返璞归真", "死亡循环", "赴险如夷"} {
		if !slices.Contains(got, want) {
			t.Fatalf("shortlist %v misses %s", got, want)
		}
	}
	if slices.Contains(got, "巨人杀手") {
		t.Fatalf("shortlist %v has an unrelated name", got)
	}

	// The normal game screen (a few scattered words) is not worth a call.
	if worthAsking(bandLines([]screen.Line{{Text: "回城", Conf: 0.9, X: 0.5, Y: 0.8}})) {
		t.Fatal("asked on a screen without cards")
	}
}
