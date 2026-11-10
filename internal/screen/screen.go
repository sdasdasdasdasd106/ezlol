// Package screen detects the augment selection screen by OCR-ing the display:
// macOS via the ezlol-ocr helper built from tools/ocr, Windows via the built-in
// Windows OCR driven by an embedded PowerShell script (ocr.ps1).
package screen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Line is one recognized text line with its normalized position (0..1, top-left origin).
type Line struct {
	Text string  `json:"text"`
	Conf float64 `json:"conf"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
}

// ErrUnavailable means no OCR helper exists on this platform/build.
var ErrUnavailable = errors.New("screen ocr unavailable")

// Reader runs the OCR helper: a one-shot binary on macOS (tools/ocr), a
// long-running PowerShell process using Windows OCR on Windows.
type Reader struct {
	mu      sync.Mutex
	scan    func(ctx context.Context, dump string) ([]byte, error) // nil = unavailable
	lang    string                                                 // OCR language the helper reported, if any
	capture string                                                 // how the last frame was captured: dda | gdi (why)
}

// ErrBlack means the captured frame was black: the game is probably in
// exclusive fullscreen and the screen cannot be read.
var ErrBlack = errors.New("截到的是黑屏：这台电脑在全屏模式下截不到游戏画面，请检查「全屏优化」或改用无边框模式")

// Capture says how the last frame was captured ("dda", or "gdi (reason)").
func (r *Reader) Capture() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capture
}

// New locates the helper for this platform.
func New() *Reader {
	// Testing aid: EZLOL_OCR_FAKE=file.json answers every scan with that file's
	// contents, so the whole pipeline can run without a game on screen.
	if p := os.Getenv("EZLOL_OCR_FAKE"); p != "" {
		return &Reader{scan: func(context.Context, string) ([]byte, error) { return os.ReadFile(p) }}
	}
	switch runtime.GOOS {
	case "darwin":
		if p := macHelper(); p != "" {
			return &Reader{scan: func(ctx context.Context, dump string) ([]byte, error) { return runOnce(ctx, p, dump) }}
		}
	case "windows":
		if scan := windowsHelper(); scan != nil {
			return &Reader{scan: scan}
		}
	}
	return &Reader{}
}

// macHelper finds ezlol-ocr: $EZLOL_OCR, then next to the executable, then bin/.
func macHelper() string {
	if p := os.Getenv("EZLOL_OCR"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "ezlol-ocr")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat("bin/ezlol-ocr"); err == nil {
		return "bin/ezlol-ocr"
	}
	return ""
}

func runOnce(ctx context.Context, path, dump string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path)
	if dump != "" {
		cmd.Env = append(os.Environ(), "EZLOL_OCR_DUMP="+dump)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// Available reports whether OCR can run.
func (r *Reader) Available() bool { return r.scan != nil }

// Lang is the OCR language reported by the helper ("" until the first scan,
// or when the helper does not say). On Windows, Chinese names are only
// recognised when this starts with "zh".
func (r *Reader) Lang() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lang
}

// Read captures the screen and returns recognized lines.
func (r *Reader) Read(ctx context.Context) ([]Line, error) { return r.ReadDump(ctx, "") }

// ReadDump is Read that also saves the captured image as a PNG at dump (when not "").
func (r *Reader) ReadDump(ctx context.Context, dump string) ([]Line, error) {
	if r.scan == nil {
		return nil, ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := r.scan(ctx, dump)
	if err != nil {
		return nil, err
	}
	var res struct {
		Lines   []Line `json:"lines"`
		Lang    string `json:"lang"`
		Error   string `json:"error"`
		Capture string `json:"capture"`
		Black   bool   `json:"black"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	r.capture = res.Capture
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	if res.Lang != "" {
		r.lang = res.Lang
	}
	if res.Black {
		return nil, ErrBlack
	}
	return res.Lines, nil
}

// Normalize lowercases and strips everything but letters and digits.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Hit is a matched candidate with the position of the line it was found on.
type Hit struct {
	Key string
	X   float64
	Y   float64
}

// Match finds which of the candidate names appear in the OCR lines. It returns
// hits in reading order (left to right). A candidate matches when a line equals
// it, contains it, or is within a small edit distance.
func Match(lines []Line, candidates map[string]string) []Hit {
	var hits []Hit
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Conf < 0.3 {
			continue
		}
		t := Normalize(l.Text)
		if len(t) < 4 { // bytes: 4 Latin letters, or 2 Chinese characters
			continue
		}
		for norm, key := range candidates {
			if seen[key] {
				continue
			}
			if matches(t, norm) {
				seen[key] = true
				hits = append(hits, Hit{key, l.X, l.Y})
			}
		}
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].X < hits[j-1].X; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	return hits
}

// matches reports whether OCR text t (normalised) names candidate norm.
func matches(t, norm string) bool {
	if t == norm {
		return true
	}
	if hasHan(norm) {
		// Chinese names are short (2-6 characters) and every character counts,
		// so compare in characters and keep the tolerance tight.
		rt, rn := len([]rune(t)), len([]rune(norm))
		d := rt - rn
		if d < 0 {
			d = -d
		}
		switch {
		case rn >= 3 && rt <= rn+4 && strings.Contains(t, norm):
			return true
		case rn >= 4 && d <= 1 && lev(t, norm) <= 1:
			return true
		case rn >= 7 && d <= 2 && lev(t, norm) <= 2:
			return true
		}
		return false
	}
	return (len(norm) >= 6 && strings.Contains(t, norm)) || (len(norm) >= 8 && len(t) >= 6 && lev(t, norm) <= 2)
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// OfferLayout keeps only hits that look like the augment selection cards: in
// the middle band of the screen, on roughly one row, spread horizontally.
// Chat announcements ("X chose Escape Plan") sit bottom-left and stack
// vertically, so they fail the spread/row test.
func OfferLayout(hits []Hit) []Hit {
	var band []Hit
	for _, h := range hits {
		if h.Y >= 0.2 && h.Y <= 0.85 && h.X >= 0.12 && h.X <= 0.88 {
			band = append(band, h)
		}
	}
	if len(band) < 2 {
		return nil
	}
	// Largest group of hits sharing a row (|dy| <= 0.12).
	best := []Hit{}
	for _, a := range band {
		var row []Hit
		for _, b := range band {
			if abs(a.Y-b.Y) <= 0.12 {
				row = append(row, b)
			}
		}
		if len(row) > len(best) {
			best = row
		}
	}
	if len(best) < 2 || best[len(best)-1].X-best[0].X < 0.2 {
		return nil
	}
	return best
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func lev(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
