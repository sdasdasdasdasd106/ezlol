package screen

import (
	"image"
	"testing"
)

func TestMatch(t *testing.T) {
	cands := map[string]string{
		Normalize("Infinite Recursion"): "1001",
		Normalize("High Roller"):        "1002",
		Normalize("Circle of Death"):    "1003",
		Normalize("Eureka"):             "1004",
	}
	lines := []Line{
		{Text: "CHOOSE AN AUGMENT", Conf: 0.9, X: 0.5, Y: 0.2},
		{Text: "Circle of Deatn", Conf: 0.6, X: 0.75, Y: 0.4}, // OCR typo, edit distance 1
		{Text: "INFINITE RECURSION", Conf: 0.8, X: 0.25, Y: 0.4},
		{Text: "Eureka", Conf: 0.9, X: 0.5, Y: 0.4},
		{Text: "High", Conf: 0.9, X: 0.1, Y: 0.9}, // too short / partial
	}
	got := OfferLayout(Match(lines, cands))
	want := []string{"1001", "1004", "1003"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// Chat announcements bottom-left, stacked: must not look like an offer.
	chat := []Line{
		{Text: "Rumble chose Escape Plan", Conf: 0.9, X: 0.12, Y: 0.82},
		{Text: "Alistar chose High Roller", Conf: 0.9, X: 0.12, Y: 0.86},
		{Text: "Twitch chose Eureka", Conf: 0.9, X: 0.12, Y: 0.9},
	}
	cands["escapeplan"] = "2001"
	if got := OfferLayout(Match(chat, cands)); len(got) != 0 {
		t.Fatalf("chat lines matched as offer: %v", got)
	}
}

func TestMatchChinese(t *testing.T) {
	cands := map[string]string{
		Normalize("无尽递归"):   "1001",
		Normalize("豪掷千金"):   "1002",
		Normalize("死亡之环"):   "1003",
		Normalize("尤里卡"):    "1004",
		Normalize("逃跑计划"):   "2001",
		Normalize("珠光护手之力"): "2002",
	}
	lines := []Line{
		{Text: "选择一个强化符文", Conf: 1, X: 0.5, Y: 0.2},
		{Text: "死 亡 之 坏", Conf: 1, X: 0.75, Y: 0.4}, // Windows OCR spaces + one wrong character
		{Text: "无 尽 递 归", Conf: 1, X: 0.25, Y: 0.4},
		{Text: "尤里卡", Conf: 1, X: 0.5, Y: 0.4},
		// A long description that merely mentions a name must not count.
		{Text: "你的技能在命中时会触发逃跑计划并获得大量移动速度", Conf: 1, X: 0.25, Y: 0.5},
	}
	got := OfferLayout(Match(lines, cands))
	want := []string{"1001", "1004", "1003"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// Three characters must not be fuzzily matched to something else.
	if hits := Match([]Line{{Text: "尤里斯", Conf: 1, X: 0.5, Y: 0.4}}, cands); len(hits) != 0 {
		t.Fatalf("fuzzy 3-character match: %v", hits)
	}
}

func TestIsBlackAndBand(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	if !IsBlack(img) {
		t.Fatal("empty frame should be black")
	}
	// A dark game scene with some bright UI is not black.
	for y := 200; y < 260; y++ {
		for x := 100; x < 900; x++ {
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 220, 200, 120, 255
		}
	}
	if IsBlack(img) {
		t.Fatal("frame with content reported black")
	}
	band, cx, cy := OfferBand(img)
	if cx != 80 || cy != 60 || band.Bounds().Dx() != 840 || band.Bounds().Dy() != 390 {
		t.Fatalf("band %v %d %d", band.Bounds(), cx, cy)
	}
}
