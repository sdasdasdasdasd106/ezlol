// Package ai asks DeepSeek (api.deepseek.com) for a second opinion on augment
// picks, researches a champion on the web before a game, and works out the
// augment cards from garbled OCR text when name matching fails (text only). Everything here is advisory: the
// statistics stay the primary recommendation.
//
// DeepSeek has no official Go SDK; its API is OpenAI-compatible JSON over
// HTTPS, called directly here.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Model is DeepSeek's Flash model, the cheapest.
const Model = "deepseek-flash"

// BaseURL of the DeepSeek API (a variable so tests can point it elsewhere).
var BaseURL = "https://api.deepseek.com"

// Testing aid: EZLOL_DEEPSEEK_BASE points the client at a local fake server.
// Only loopback addresses are accepted, so the key cannot be sent elsewhere.
func init() {
	if v := os.Getenv("EZLOL_DEEPSEEK_BASE"); strings.HasPrefix(v, "http://127.0.0.1:") {
		BaseURL = strings.TrimRight(v, "/")
	}
}

// Client calls DeepSeek with the user's API key and tracks what it spends.
type Client struct {
	Key   func() string // read on every call, so settings changes apply at once
	HTTP  *http.Client
	Usage *Usage
}

// Message is a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions tune one request.
type ChatOptions struct {
	Think     bool   // thinking mode (slower, more careful); off for real-time calls
	Effort    string // with Think: low | high | max
	MaxTokens int
}

type chatRequest struct {
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Thinking        map[string]string `json:"thinking"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	ResponseFormat  map[string]string `json:"response_format"`
	MaxTokens       int               `json:"max_tokens"`
	Stream          bool              `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens   int `json:"prompt_tokens"`
		Completion     int `json:"completion_tokens"`
		CacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
}

// ErrNoKey means no DeepSeek API key is configured.
var ErrNoKey = errors.New("还没有填 DeepSeek API key")

func (c *Client) key() string {
	if c == nil || c.Key == nil {
		return ""
	}
	return strings.TrimSpace(c.Key())
}

// Ready reports whether a key is configured.
func (c *Client) Ready() bool { return c.key() != "" }

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	key := c.key()
	if key == "" {
		return ErrNoKey
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errors.New("DeepSeek 拒绝了这个 API key")
	case http.StatusPaymentRequired:
		return errors.New("DeepSeek 账户余额不足")
	case http.StatusTooManyRequests:
		return errors.New("DeepSeek 请求太频繁，稍后再试")
	default:
		var e struct {
			Error apiError `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error.Message == "" {
			e.Error.Message = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("DeepSeek %d：%s", res.StatusCode, e.Error.Message)
	}
	return json.Unmarshal(data, out)
}

// ChatJSON sends messages in JSON-output mode and decodes the answer into out.
// The prompt must mention "json" and show the expected shape.
func (c *Client) ChatJSON(ctx context.Context, msgs []Message, opt ChatOptions, out any) error {
	req := chatRequest{Model: Model, Messages: msgs, ResponseFormat: map[string]string{"type": "json_object"}, MaxTokens: opt.MaxTokens}
	if req.MaxTokens == 0 {
		req.MaxTokens = 1000
	}
	if opt.Think {
		req.Thinking = map[string]string{"type": "enabled"}
		req.ReasoningEffort = opt.Effort
	} else {
		req.Thinking = map[string]string{"type": "disabled"}
	}
	var res chatResponse
	if err := c.post(ctx, "/chat/completions", req, &res); err != nil {
		return err
	}
	c.Usage.addChat(res.Usage.PromptTokens, res.Usage.CacheHitTokens, res.Usage.Completion, time.Now())
	if len(res.Choices) == 0 {
		return errors.New("DeepSeek 没有返回内容")
	}
	content := strings.TrimSpace(res.Choices[0].Message.Content)
	if content == "" {
		return errors.New("DeepSeek 返回了空内容")
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("DeepSeek 返回的不是有效 JSON：%w", err)
	}
	return nil
}

// SearchResult is one web search hit.
type SearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Summary string  `json:"summary"`
	Date    string  `json:"date"`
	Score   float64 `json:"rerank_score"`
}

// Search runs DeepSeek's web search (billed per successful request).
func (c *Client) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	var res struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.post(ctx, "/v1/tools/search", map[string]any{"query": query, "count": count}, &res); err != nil {
		return nil, err
	}
	if len(res.Results) > 0 {
		c.Usage.addSearch(time.Now())
	}
	return res.Results, nil
}

// ---- spending ----

// Prices of deepseek-flash in USD per million tokens, off-peak; peak doubles
// (peak: 01:00-04:00 and 06:00-10:00 UTC, Monday to Friday). Search: per request.
const (
	priceInHit  = 0.003
	priceInMiss = 0.15
	priceOut    = 0.60
	priceSearch = 0.00075
)

func peak(t time.Time) bool {
	u := t.UTC()
	if u.Weekday() == time.Saturday || u.Weekday() == time.Sunday {
		return false
	}
	h := u.Hour()
	return (h >= 1 && h < 4) || (h >= 6 && h < 10)
}

// Cost is the USD price of one chat call.
func Cost(prompt, cacheHit, completion int, at time.Time) float64 {
	miss := prompt - cacheHit
	c := (float64(cacheHit)*priceInHit + float64(miss)*priceInMiss + float64(completion)*priceOut) / 1e6
	if peak(at) {
		c *= 2
	}
	return c
}

// Totals is spending in one calendar month.
type Totals struct {
	Month    string  `json:"month"`
	Calls    int     `json:"calls"`
	Searches int     `json:"searches"`
	Tokens   int     `json:"tokens"`
	USD      float64 `json:"usd"`
}

// Usage tracks spending per calendar month (shown in Settings).
type Usage struct {
	mu       sync.Mutex
	t        Totals
	OnChange func(Totals) // e.g. persist; called outside the lock
}

// Restore sets saved totals (ignored if from an earlier month).
func (u *Usage) Restore(t Totals) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.t = t
	u.roll(time.Now())
}

func (u *Usage) roll(at time.Time) {
	if m := at.Format("2006-01"); u.t.Month != m {
		u.t = Totals{Month: m}
	}
}

func (u *Usage) add(at time.Time, f func(*Totals)) {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.roll(at)
	f(&u.t)
	snap := u.t
	u.mu.Unlock()
	if u.OnChange != nil {
		u.OnChange(snap)
	}
}

func (u *Usage) addChat(prompt, hit, completion int, at time.Time) {
	u.add(at, func(t *Totals) {
		t.Calls++
		t.Tokens += prompt + completion
		t.USD += Cost(prompt, hit, completion, at)
	})
}

func (u *Usage) addSearch(at time.Time) {
	u.add(at, func(t *Totals) {
		t.Searches++
		t.USD += priceSearch
	})
}

// Snapshot returns this month's totals.
func (u *Usage) Snapshot() Totals {
	if u == nil {
		return Totals{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.roll(time.Now())
	return u.t
}
