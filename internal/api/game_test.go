package api

import (
	"testing"

	"github.com/aarlint/ezlol/internal/ddragon"
	"github.com/aarlint/ezlol/internal/live"
)

func TestResolveTransformedChampion(t *testing.T) {
	d := &ddragon.Data{Champions: map[int]ddragon.Champion{
		150: {ID: 150, Key: "Gnar", Name: "纳尔"},
		22:  {ID: 22, Key: "Ashe", Name: "艾希"},
	}}
	cases := map[string]int{
		"game_character_displayname_Gnar":    150,
		"game_character_displayname_GnarBig": 150, // Mega Gnar
		"game_character_displayname_Ashe":    22,
	}
	for raw, want := range cases {
		if got := resolveChampion(d, live.Player{RawChampionName: raw, ChampionName: "狂暴纳尔"}); got.ID != want {
			t.Errorf("%s: got %d want %d", raw, got.ID, want)
		}
	}
}
