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

func TestTokenNeverEchoed(t *testing.T) {
	s := Settings{GitHubToken: "github_pat_secret1234"}
	m := s.Masked()
	for _, v := range m {
		if str, ok := v.(string); ok && str == s.GitHubToken {
			t.Fatal("raw token in the settings view")
		}
	}
	if m["githubTokenSet"] != true || m["githubTokenHint"] != "••••1234" {
		t.Fatalf("view: %v", m)
	}
}
