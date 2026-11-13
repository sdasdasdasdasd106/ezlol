package hotkey

import (
	"sync"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]Combo{
		"CommandOrControl+Alt+S": {Mods: []int{vkControl, vkAlt}, Key: 'S'},
		"Ctrl+Alt+1":             {Mods: []int{vkControl, vkAlt}, Key: '1'},
		"F8":                     {Key: 0x77},
		"F11":                    {Key: 0x7A},
		"num3":                   {Key: 0x63},
		"Mouse4":                 {Key: vkXButton1},
		"Shift+Mouse5":           {Mods: []int{vkShift}, Key: vkXButton2},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got.Key != want.Key || len(got.Mods) != len(want.Mods) {
			t.Errorf("%s: got %+v %v want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Ctrl+", "Hyper+S", "F25", "F0", "Ctrl+Alt"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestWatcherFiresOncePerPress(t *testing.T) {
	var mu sync.Mutex
	keys := map[int]bool{}
	var fired []string
	w := &Watcher{binds: map[string]Combo{}, held: map[string]bool{},
		fire: func(a string) { mu.Lock(); fired = append(fired, a); mu.Unlock() },
		down: func(vk int) bool { mu.Lock(); defer mu.Unlock(); return keys[vk] }}
	w.Set(map[string]string{"scan": "CommandOrControl+Alt+S", "pick1": "Alt+1"})
	press := func(vks ...int) {
		mu.Lock()
		keys = map[int]bool{}
		for _, k := range vks {
			keys[k] = true
		}
		mu.Unlock()
		w.poll()
	}
	press(vkControl, vkAlt, 'S')
	press(vkControl, vkAlt, 'S') // still held: no repeat
	press()
	press(vkControl, vkAlt, 'S')
	press(vkControl, vkAlt, '1') // Ctrl+Alt+1 is not Alt+1
	press(vkAlt, '1')
	w.Suspend(true)
	press()
	press(vkAlt, '1')
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 3 {
		t.Fatalf("fired %v", fired)
	}
}
