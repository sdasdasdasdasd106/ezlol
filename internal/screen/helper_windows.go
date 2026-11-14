//go:build windows

package screen

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ocr.ps1
var ocrScript []byte

// psHelper keeps one hidden PowerShell process running ocr.ps1 and talks to
// it line by line, so each scan does not pay PowerShell's start-up cost.
type psHelper struct {
	exe    string
	script string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	starting bool
	lastErr  error
	failedAt time.Time

	dda      *ddaCapturer
	ddaOff   bool // Desktop Duplication does not work here: GDI only
	ddaFails int  // consecutive Desktop Duplication failures
	ddaOffN  int  // consecutive scans without a D3D11 device
}

func windowsHelper() func(ctx context.Context, dump string) ([]byte, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(exe); err != nil {
		p, err := exec.LookPath("powershell.exe")
		if err != nil {
			return nil
		}
		exe = p
	}
	script := os.Getenv("EZLOL_OCR")
	if script == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "ezlol")
		script = filepath.Join(dir, "ezlol-ocr.ps1")
		// Windows PowerShell 5.1 reads BOM-less scripts in the ANSI code page;
		// the script is pure ASCII, so that is fine.
		if old, err := os.ReadFile(script); err != nil || !bytes.Equal(old, ocrScript) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil
			}
			if err := os.WriteFile(script, ocrScript, 0o644); err != nil {
				return nil
			}
		}
	}
	h := &psHelper{exe: exe, script: script, dda: newDDA()}
	return h.scan
}

// launch starts the helper and waits for its ready line. It does not hold h.mu.
func (h *psHelper) launch() (*exec.Cmd, io.WriteCloser, chan string, error) {
	cmd := exec.Command(h.exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", h.script)
	// No console window flashing up while the game runs.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr := &limited{n: 4 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	lines := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			t := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(t, "{") {
				lines <- t
			}
		}
		close(lines)
		_ = cmd.Wait()
	}()
	kill := func() { _ = cmd.Process.Kill() }

	// First line: {"ready":true,...} or {"error":"init: ..."}. Compiling the
	// helper types takes a few seconds on first start.
	select {
	case l, ok := <-lines:
		if !ok {
			kill()
			return nil, nil, nil, fmt.Errorf("ocr helper exited: %s", stderr.String())
		}
		if strings.Contains(l, `"error"`) {
			kill()
			return nil, nil, nil, errors.New(l)
		}
		return cmd, stdin, lines, nil
	case <-time.After(45 * time.Second):
		kill()
		return nil, nil, nil, errors.New("ocr helper did not start in time")
	}
}

// startAsync launches the helper in the background; callers see "starting"
// until it is ready instead of blocking for PowerShell's start-up.
func (h *psHelper) startAsync() {
	h.starting = true
	go func() {
		cmd, stdin, lines, err := h.launch()
		h.mu.Lock()
		defer h.mu.Unlock()
		h.starting = false
		if err != nil {
			h.lastErr, h.failedAt = err, time.Now()
			return
		}
		h.cmd, h.stdin, h.lines, h.lastErr = cmd, stdin, lines, nil
	}()
}

func (h *psHelper) stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.cmd, h.stdin, h.lines = nil, nil, nil
}

func (h *psHelper) scan(ctx context.Context, dump string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		switch {
		case h.starting:
		case h.lastErr != nil && time.Since(h.failedAt) < 30*time.Second:
			return nil, h.lastErr
		default:
			h.startAsync()
		}
		return nil, errors.New("文字识别正在启动…")
	}
	if strings.ContainsAny(dump, "\t\r\n") {
		dump = ""
	}
	// Only ever capture the game window: if it is not there (minimized,
	// loading, closed), there is nothing to read.
	target, ok := gameRect()
	if !ok {
		return nil, ErrNoGame
	}
	// Desktop Duplication (sees fullscreen games). A transient failure
	// (fullscreen switch, alt-tab) is retried on the next scan; GDI is only the
	// fallback when Desktop Duplication does not work on this machine at all
	// or keeps failing.
	cmd, note := h.ddaCommand(target, dump)
	if cmd == "" {
		h.ddaFails++
		if !h.ddaOff && h.ddaFails < 5 {
			return nil, fmt.Errorf("截屏暂时失败，稍后重试（%s）", note)
		}
		gdiDump := dump
		for _, r := range gdiDump {
			if r > 127 { // the helper's stdin may not carry non-ASCII paths
				gdiDump = ""
				break
			}
		}
		cmd = "scan\t" + gdiDump + "\t" + note + "\n"
	} else {
		h.ddaFails = 0
	}
	if _, err := io.WriteString(h.stdin, cmd); err != nil {
		h.stop()
		return nil, err
	}
	select {
	case l, ok := <-h.lines:
		if !ok {
			h.stop()
			return nil, errors.New("ocr helper exited")
		}
		return []byte(l), nil
	case <-ctx.Done():
		// A late answer would be read as the reply to the next scan: restart.
		h.stop()
		return nil, ctx.Err()
	}
}

// limited keeps at most n bytes of what is written to it.
type limited struct {
	mu  sync.Mutex
	buf bytes.Buffer
	n   int
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n > 0 {
		k := min(len(p), l.n)
		l.buf.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

func (l *limited) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(l.buf.String())
}

// ddaCommand captures the game window with Desktop Duplication, crops it to the
// middle band where augment cards appear and returns the helper command that
// carries it (base64 PNG). The screenshot for the test log, if asked for, is
// written here. On failure it returns "" and why.
func (h *psHelper) ddaCommand(target image.Rectangle, dump string) (cmd, note string) {
	if h.ddaOff {
		return "", "dda-off"
	}
	img, area, err := h.dda.capture(target, 5*time.Second)
	if err != nil {
		// Only a machine without a D3D11 device (remote desktop, no GPU
		// driver) never works; anything else can be passing (a fullscreen
		// switch), so Desktop Duplication is tried again on the next scan.
		if strings.Contains(err.Error(), "d3d11 device") {
			h.ddaOffN++
			if h.ddaOffN >= 3 {
				h.ddaOff = true
			}
		}
		return "", "dda-error: " + err.Error()
	}
	h.ddaOffN = 0
	if IsBlack(img) {
		return "", "dda-black"
	}
	band, cx, cy := OfferBand(img)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, band); err != nil {
		return "", "dda-encode: " + err.Error()
	}
	if dump != "" {
		_ = os.WriteFile(dump, buf.Bytes(), 0o644)
	}
	return fmt.Sprintf("b64\t%d\t%d\t%d\t%d\tdda\t%s\n", cx, cy, area.Dx(), area.Dy(), base64.StdEncoding.EncodeToString(buf.Bytes())), ""
}
