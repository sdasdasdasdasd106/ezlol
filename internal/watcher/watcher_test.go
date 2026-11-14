package watcher

import "testing"

func TestShortPatch(t *testing.T) {
	if got := shortPatch("16.17.8104348+branch"); got != "16.17" {
		t.Fatalf("got %s", got)
	}
	if got := shortPatch("weird"); got != "weird" {
		t.Fatalf("got %s", got)
	}
}

func TestStatusChangedOnBench(t *testing.T) {
	a := Status{Phase: "ChampSelect", ChampSelect: &ChampSelectState{BenchEnabled: true, Bench: []int{1, 2}, Rerolls: 1}}
	b := a
	b.ChampSelect = &ChampSelectState{BenchEnabled: true, Bench: []int{1, 2}, Rerolls: 1}
	if statusChanged(a, b) {
		t.Fatal("same bench reported as a change")
	}
	b.ChampSelect = &ChampSelectState{BenchEnabled: true, Bench: []int{1, 3}, Rerolls: 1}
	if !statusChanged(a, b) {
		t.Fatal("bench swap not reported")
	}
	b.ChampSelect = &ChampSelectState{BenchEnabled: true, Bench: []int{1, 2}, Rerolls: 0}
	if !statusChanged(a, b) {
		t.Fatal("reroll not reported")
	}
	b.ChampSelect = nil
	if !statusChanged(a, b) {
		t.Fatal("leaving champ select not reported")
	}
}
