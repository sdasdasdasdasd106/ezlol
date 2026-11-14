package settings

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	st := Load(p)
	if !st.Get().OCR || !st.Get().TestLog || st.Get().LogShots {
		t.Fatal("defaults")
	}
	if err := st.Update(func(s *Settings) { s.OCR = false; s.LogShots = true }); err != nil {
		t.Fatal(err)
	}
	again := Load(p)
	if again.Get().OCR || !again.Get().LogShots {
		t.Fatalf("got %+v", again.Get())
	}
	if m := again.Get().Masked(); m["ocr"] != false || m["logShots"] != true {
		t.Fatalf("view: %v", m)
	}
}

func TestDeepSeekKeyNeverEchoed(t *testing.T) {
	s := Settings{DeepSeekKey: "sk-abcdef123456"}
	m := s.Masked()
	for _, v := range m {
		if str, ok := v.(string); ok && str == s.DeepSeekKey {
			t.Fatal("raw key in the settings view")
		}
	}
	if m["deepseekKeySet"] != true || m["deepseekKeyHint"] != "••••3456" {
		t.Fatalf("view: %v", m)
	}
}
