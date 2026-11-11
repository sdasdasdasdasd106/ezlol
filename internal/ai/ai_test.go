package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDeepSeek answers chat and search calls and records the requests.
type fakeDeepSeek struct {
	mu      sync.Mutex
	chats   []map[string]any
	answer  string // JSON the "model" returns
	results []SearchResult
}

func (f *fakeDeepSeek) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer sk-test" {
		w.WriteHeader(401)
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/chat/completions":
		f.chats = append(f.chats, body)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": f.answer}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1000, "completion_tokens": 100, "prompt_cache_hit_tokens": 0},
		})
	case "/v1/tools/search":
		json.NewEncoder(w).Encode(map[string]any{"results": f.results})
	default:
		w.WriteHeader(404)
	}
}

func newFake(t *testing.T, answer string) (*fakeDeepSeek, *Client) {
	f := &fakeDeepSeek{answer: answer}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })
	return f, &Client{Key: func() string { return "sk-test" }, Usage: &Usage{}}
}

func TestReviewChoice(t *testing.T) {
	f, c := newFake(t, `{"pick":"死亡循环","reason":"对面三个坦克，持续伤害更好","agree":false,"confidence":"中"}`)
	in := ReviewInput{Champion: "艾希", Level: 7, Stage: "7 级", DataPick: "返璞归真",
		Enemies: []Player{{Champion: "奥恩", Items: []string{"日炎圣盾"}}},
		Cards:   []Card{{Name: "返璞归真", Reasons: []string{"胜率 54%"}}, {Name: "死亡循环"}, {Name: "赴险如夷"}}}
	r, err := c.ReviewChoice(context.Background(), in)
	if err != nil || r.Pick != "死亡循环" || r.Agree {
		t.Fatalf("review: %+v %v", r, err)
	}
	req := f.chats[0]
	if req["model"] != Model || req["thinking"].(map[string]any)["type"] != "disabled" || req["response_format"].(map[string]any)["type"] != "json_object" {
		t.Fatalf("request: %v", req)
	}
	user := req["messages"].([]any)[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "奥恩（日炎圣盾）") || !strings.Contains(user, "json") {
		t.Fatalf("prompt: %s", user)
	}
	if u := c.Usage.Snapshot(); u.Calls != 1 || u.USD <= 0 {
		t.Fatalf("usage: %+v", u)
	}

	// Picking the statistics' choice counts as agreeing, whatever the flag says.
	f.answer = `{"pick":"返璞归真","reason":"x","agree":false,"confidence":"高"}`
	if r, err := c.ReviewChoice(context.Background(), in); err != nil || !r.Agree {
		t.Fatalf("same pick should agree: %+v %v", r, err)
	}

	// A pick that is not one of the options is rejected.
	f.answer = `{"pick":"大法师","reason":"x","agree":false,"confidence":"低"}`
	if _, err := c.ReviewChoice(context.Background(), in); err == nil {
		t.Fatal("accepted an augment that was not offered")
	}
}

func TestResearchKeepsKnownNames(t *testing.T) {
	f, c := newFake(t, `{"summary":"走攻速","prefer":[{"name":"返璞归真","why":"a"},{"name":"不存在的强化","why":"b"}],"avoid":[],"build":["先出无尽"],"sources":["https://x"]}`)
	f.results = []SearchResult{{Title: "攻略", URL: "https://x", Summary: "艾希海克斯乱斗推荐返璞归真"}}
	s, err := c.Research(context.Background(), ResearchInput{Champion: "艾希", ChampionEN: "Ashe", Patch: "16.22", WinRate: 0.43,
		Augments: []AugStat{{Name: "返璞归真", Games: 500, WinRate: 0.5}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Prefer) != 1 || s.Prefer[0].Name != "返璞归真" {
		t.Fatalf("prefer: %+v", s.Prefer)
	}
	req := f.chats[0]
	if req["thinking"].(map[string]any)["type"] != "enabled" {
		t.Fatalf("research should think: %v", req["thinking"])
	}
	if u := c.Usage.Snapshot(); u.Searches != 2 {
		t.Fatalf("searches: %+v", u)
	}
}

func TestReadOCRMapsToCandidates(t *testing.T) {
	f, c := newFake(t, `{"choosing":true,"names":["返璞归真","死亡循环","瞎编的"]}`)
	lines := []OCRLine{{Text: "返 璞 归 具", X: 0.3, Y: 0.42}, {Text: "死亡循坏", X: 0.5, Y: 0.42}, {Text: "赴险如", X: 0.7, Y: 0.42}}
	r, err := c.ReadOCR(context.Background(), lines, []string{"返璞归真", "死亡循环", "赴险如夷"})
	if err != nil || !r.Choosing || len(r.Names) != 3 {
		t.Fatalf("read: %+v %v", r, err)
	}
	if r.Names[2] != "" {
		t.Fatalf("a name that is not a candidate must be dropped: %+v", r.Names)
	}
	prompt := f.chats[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(prompt, "(x=0.30, y=0.42) 返 璞 归 具") || !strings.Contains(prompt, "候选强化：返璞归真、死亡循环、赴险如夷") {
		t.Fatalf("prompt: %s", prompt)
	}
	if f.chats[0]["thinking"].(map[string]any)["type"] != "disabled" {
		t.Fatal("real-time call should not think")
	}
}

func TestNoKeyAndCost(t *testing.T) {
	c := &Client{Key: func() string { return " " }}
	if c.Ready() {
		t.Fatal("blank key counts as ready")
	}
	if _, err := c.ReviewChoice(context.Background(), ReviewInput{}); err != ErrNoKey {
		t.Fatalf("no key: %v", err)
	}
	offPeak := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) // Saturday
	onPeak := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)   // Wednesday 07:00 UTC
	if got := Cost(1_000_000, 0, 0, offPeak); got != 0.15 {
		t.Fatalf("off-peak input: %v", got)
	}
	if got := Cost(0, 0, 1_000_000, onPeak); got != 1.2 {
		t.Fatalf("peak output: %v", got)
	}
}
