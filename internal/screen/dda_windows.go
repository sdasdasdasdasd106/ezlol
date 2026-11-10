//go:build windows

package screen

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/kirides/go-d3d/d3d11"
	"github.com/kirides/go-d3d/outputduplication"
	"github.com/kirides/go-d3d/win"
)

// Desktop Duplication (DXGI), the same API OBS's "Display Capture" uses: it
// copies what the GPU sends to the monitor, so it also sees most fullscreen
// games. It only reads the screen; nothing touches the game process.

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procFindWindowW = user32.NewProc("FindWindowW")
	procGetWindowRc = user32.NewProc("GetWindowRect")
	procIsIconic    = user32.NewProc("IsIconic")
	procSetDpiCtx   = user32.NewProc("SetProcessDpiAwarenessContext")
)

func init() {
	// Real pixels, so window and monitor rectangles agree with the captured frame.
	if procSetDpiCtx.Find() == nil {
		_, _, _ = procSetDpiCtx.Call(uintptr(^uintptr(3))) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
}

// gameRect is the League game window in desktop coordinates, if it is open.
func gameRect() (image.Rectangle, bool) {
	title, _ := syscall.UTF16PtrFromString("League of Legends (TM) Client")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if h == 0 {
		return image.Rectangle{}, false
	}
	if iconic, _, _ := procIsIconic.Call(h); iconic != 0 {
		return image.Rectangle{}, false
	}
	var r struct{ Left, Top, Right, Bottom int32 }
	if ok, _, _ := procGetWindowRc.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return image.Rectangle{}, false
	}
	rect := image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
	if rect.Dx() < 300 || rect.Dy() < 200 {
		return image.Rectangle{}, false
	}
	return rect, true
}

type ddaReq struct {
	target image.Rectangle // desktop coordinates; empty = primary output
	resp   chan ddaResp
}

type ddaResp struct {
	img  *image.RGBA // the target area (or the whole output)
	full image.Rectangle
	err  error
}

// ddaCapturer owns the D3D device on one locked OS thread, as D3D/DXGI expect.
type ddaCapturer struct {
	reqs chan ddaReq
}

func newDDA() *ddaCapturer {
	c := &ddaCapturer{reqs: make(chan ddaReq)}
	go c.loop()
	return c
}

// capture returns the pixels of target (clipped to the monitor it is on).
func (c *ddaCapturer) capture(target image.Rectangle, timeout time.Duration) (*image.RGBA, image.Rectangle, error) {
	req := ddaReq{target: target, resp: make(chan ddaResp, 1)}
	select {
	case c.reqs <- req:
	case <-time.After(timeout):
		return nil, image.Rectangle{}, errors.New("desktop duplication busy")
	}
	select {
	case r := <-req.resp:
		return r.img, r.full, r.err
	case <-time.After(timeout):
		return nil, image.Rectangle{}, errors.New("desktop duplication timed out")
	}
}

type ddaOutput struct {
	dup    *outputduplication.OutputDuplicator
	bounds image.Rectangle
	last   *image.RGBA // last good frame: the API only returns changed frames
}

func (c *ddaCapturer) loop() {
	runtime.LockOSThread()
	if win.IsValidDpiAwarenessContext(win.DpiAwarenessContextPerMonitorAwareV2) {
		_, _ = win.SetThreadDpiAwarenessContext(win.DpiAwarenessContextPerMonitorAwareV2)
	}
	device, deviceCtx, err := d3d11.NewD3D11Device()
	if err != nil {
		for req := range c.reqs {
			req.resp <- ddaResp{err: fmt.Errorf("d3d11 device: %w", err)}
		}
		return
	}
	outputs := map[uint]*ddaOutput{}
	open := func(i uint) (*ddaOutput, error) {
		if o := outputs[i]; o != nil {
			return o, nil
		}
		dup, err := outputduplication.NewIDXGIOutputDuplication(device, deviceCtx, i)
		if err != nil {
			return nil, err
		}
		b, err := dup.GetBounds()
		if err != nil {
			dup.Release()
			return nil, err
		}
		o := &ddaOutput{dup: dup, bounds: b}
		outputs[i] = o
		return o, nil
	}
	drop := func(i uint) {
		if o := outputs[i]; o != nil {
			o.dup.Release()
			delete(outputs, i)
		}
	}
	for req := range c.reqs {
		req.resp <- c.grab(req.target, open, drop)
	}
}

func (c *ddaCapturer) grab(target image.Rectangle, open func(uint) (*ddaOutput, error), drop func(uint)) ddaResp {
	// Find the monitor the game is on (by the window's centre); else the first one.
	var out *ddaOutput
	var idx uint
	var firstErr error
	center := image.Pt((target.Min.X+target.Max.X)/2, (target.Min.Y+target.Max.Y)/2)
	for i := uint(0); i < 6; i++ {
		o, err := open(i)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break // outputs are numbered without gaps
		}
		if out == nil || (!target.Empty() && center.In(o.bounds)) {
			out, idx = o, i
		}
		if target.Empty() || center.In(o.bounds) {
			break
		}
	}
	if out == nil {
		return ddaResp{err: fmt.Errorf("no output: %v", firstErr)}
	}
	img := image.NewRGBA(out.bounds)
	var err error
	for try := 0; try < 3; try++ {
		if err = out.dup.GetImage(img, 200); err == nil {
			out.last = img
			break
		}
		if !errors.Is(err, outputduplication.ErrNoImageYet) {
			// Lost access (resolution change, fullscreen switch, UAC prompt): reopen next time.
			drop(idx)
			return ddaResp{err: err}
		}
		if out.last != nil {
			img, err = out.last, nil
			break
		}
	}
	if err != nil {
		return ddaResp{err: err}
	}
	area := out.bounds
	if !target.Empty() {
		area = target.Intersect(out.bounds)
		if area.Empty() {
			area = out.bounds
		}
	}
	sub := image.NewRGBA(image.Rect(0, 0, area.Dx(), area.Dy()))
	for y := 0; y < area.Dy(); y++ {
		src := img.PixOffset(area.Min.X, area.Min.Y+y)
		copy(sub.Pix[y*sub.Stride:y*sub.Stride+area.Dx()*4], img.Pix[src:src+area.Dx()*4])
	}
	return ddaResp{img: sub, full: area}
}
