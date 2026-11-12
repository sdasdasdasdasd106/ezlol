package api

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aarlint/ezlol/internal/builds"
	"github.com/aarlint/ezlol/internal/screen"
	"github.com/aarlint/ezlol/internal/settings"
	"github.com/aarlint/ezlol/internal/testlog"
	"github.com/aarlint/ezlol/internal/watcher"
)

func testServer(t *testing.T) *Server {
	t.Setenv("EZLOL_OCR_FAKE", "/nonexistent") // OCR "available" without a screen
	cfg := settings.Load("")
	off := func() bool { return false }
	return &Server{log: slog.Default(), settings: cfg, watcher: watcher.New(slog.Default(), time.Second, cfg), tlog: testlog.New(t.TempDir(), off, off), ocr: screen.New()}
}

func testLive(level int) *liveResponse {
	return &liveResponse{InGame: true, Started: true, GameTime: 600, Mode: "aram", Me: &livePlayer{Level: level}}
}

func TestManualLevel(t *testing.T) {
	for lv, want := range map[int]int{1: 1, 6: 1, 7: 7, 10: 7, 11: 11, 15: 15, 18: 15} {
		if got := manualLevel(lv); got != want {
			t.Errorf("level %d: got %d want %d", lv, got, want)
		}
	}
}

// A pick that was never seen must not hold back the next one: at level 7 an
// unseen start pick gives way, so a scan is read as the level 7 choice.
func TestSyncGameMovesPastMissedPick(t *testing.T) {
	s := testServer(t)
	l := testLive(3)
	l.GameTime = 60
	s.offer.mu.Lock()
	defer s.offer.mu.Unlock()
	if _, _, why, stop := s.syncGame(l); stop {
		t.Fatalf("stopped: %s", why)
	}
	if s.offer.pending != 1 {
		t.Fatalf("pending %d, want 1", s.offer.pending)
	}
	l = testLive(7)
	l.GameTime = 400
	s.syncGame(l)
	if s.offer.pending != 7 {
		t.Fatalf("pending %d, want 7", s.offer.pending)
	}
}

// The 1/2/3 hotkey during an offer records the pick at once and ends the offer.
func TestPickSlotRecordsAndCloses(t *testing.T) {
	s := testServer(t)
	l := testLive(7)
	s.offer.mu.Lock()
	s.syncGame(l)
	a, b, c := builds.Augment{ID: 1, Name: "大法师"}, builds.Augment{ID: 2, Name: "屹立不倒"}, builds.Augment{ID: 3, Name: "灵魂虹吸"}
	s.offer.last = &augmentOffer{Active: true, Level: 7, Stage: 2, Screen: []int{3, 1, 2},
		Offered: []offerCard{{Augment: a}, {Augment: b}, {Augment: c}}}
	s.offer.pending = 7
	s.offer.mu.Unlock()

	w := httptest.NewRecorder()
	s.pickSlot(w, httptest.NewRequest("POST", "/api/game/pick-slot", strings.NewReader(`{"slot":1}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if s.offer.last != nil || s.offer.pending != 0 || !s.offer.seenLevel[7] {
		t.Fatalf("offer not closed: last=%v pending=%d", s.offer.last, s.offer.pending)
	}
	if len(s.offer.picks) != 1 || s.offer.picks[0].Name != "灵魂虹吸" || s.offer.picks[0].Assumed {
		t.Fatalf("picks %+v", s.offer.picks)
	}

	// Reading the same stage again (a manual re-scan) replaces its record.
	s.offer.mu.Lock()
	s.recordPick(&augmentOffer{Active: true, Level: 7, Stage: 2, Screen: []int{2}, Offered: []offerCard{{Augment: b}}}, "")
	s.offer.mu.Unlock()
	if len(s.offer.picks) != 1 || s.offer.picks[0].Name != "屹立不倒" {
		t.Fatalf("picks after re-read %+v", s.offer.picks)
	}
}

// Nothing captures in the background: an offer read with the hotkey stays up
// until the next pick level, then the recommended augment is recorded.
func TestOfferClosesAtNextLevel(t *testing.T) {
	s := testServer(t)
	s.offer.mu.Lock()
	s.syncGame(testLive(1))
	a := builds.Augment{ID: 1, Name: "大法师"}
	s.offer.last = &augmentOffer{Active: true, Level: 1, Stage: 1, Screen: []int{1}, Offered: []offerCard{{Augment: a}}, ScannedAt: time.Now()}
	s.offer.mu.Unlock()

	if off, _ := s.detectOffer(t.Context(), testLive(3)); off == nil || !off.Active {
		t.Fatalf("offer should stay up: %+v", off)
	}
	off, _ := s.detectOffer(t.Context(), testLive(7))
	if len(s.offer.picks) != 1 || !s.offer.picks[0].Assumed || s.offer.picks[0].Stage != 1 {
		t.Fatalf("picks %+v", s.offer.picks)
	}
	if off == nil || off.Active || !off.Pending || off.Level != 7 {
		t.Fatalf("level 7 pick should be due: %+v", off)
	}

	// An offer left alone past offerTTL is recorded too.
	s.offer.mu.Lock()
	s.offer.last = &augmentOffer{Active: true, Level: 7, Stage: 2, Screen: []int{1}, Offered: []offerCard{{Augment: a}}, ScannedAt: time.Now().Add(-offerTTL - time.Second)}
	s.offer.mu.Unlock()
	if off, _ := s.detectOffer(t.Context(), testLive(8)); off != nil {
		t.Fatalf("expired offer still shown: %+v", off)
	}
	if len(s.offer.picks) != 2 {
		t.Fatalf("picks %+v", s.offer.picks)
	}
}
