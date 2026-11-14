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
	d := &ddaDevice{outputs: map[uint]*ddaOutput{}}
	for req := range c.reqs {
		req.resp <- d.grabRetrying(req.target)
	}
}

// ddaDevice is the D3D device and the duplicated outputs, rebuilt when they break.
type ddaDevice struct {
	device  *d3d11.ID3D11Device
	ctx     *d3d11.ID3D11DeviceContext
	outputs map[uint]*ddaOutput
}

func (d *ddaDevice) open(i uint) (*ddaOutput, error) {
	if d.device == nil {
		dev, ctx, err := d3d11.NewD3D11Device()
		if err != nil {
			return nil, fmt.Errorf("d3d11 device: %w", err)
		}
		d.device, d.ctx = dev, ctx
	}
	if o := d.outputs[i]; o != nil {
		return o, nil
	}
	dup, err := outputduplication.NewIDXGIOutputDuplication(d.device, d.ctx, i)
	if err != nil {
		return nil, err
	}
	b, err := dup.GetBounds()
	if err != nil {
		dup.Release()
		return nil, err
	}
	o := &ddaOutput{dup: dup, bounds: b}
	d.outputs[i] = o
	return o, nil
}

func (d *ddaDevice) drop(i uint) {
	if o := d.outputs[i]; o != nil {
		o.dup.Release()
		delete(d.outputs, i)
	}
}

// reset releases everything; the next open starts from a new device.
func (d *ddaDevice) reset() {
	for i := range d.outputs {
		d.drop(i)
	}
	if d.ctx != nil {
		d.ctx.Release()
	}
	if d.device != nil {
		d.device.Release()
	}
	d.device, d.ctx = nil, nil
}

// grabRetrying captures, reopening what broke. Switching out of an exclusive-
// fullscreen game and back (to read the recommendation, then reroll) makes
// Windows revoke the duplication: the next capture fails with "access lost".
// Reopen at once and capture again rather than fail this scan; if reopening
// keeps failing, start over with a new device.
func (d *ddaDevice) grabRetrying(target image.Rectangle) ddaResp {
	var r ddaResp
	for attempt := 0; attempt < 4; attempt++ {
		if attempt == 2 {
			d.reset()
		}
		if attempt > 0 {
			time.Sleep(time.Duration(150*attempt) * time.Millisecond)
		}
		r = d.grab(target)
		if r.err == nil {
			return r
		}
	}
	return r
}

func (d *ddaDevice) grab(target image.Rectangle) ddaResp {
	// Find the monitor the game is on (by the window's centre); else the first one.
	var out *ddaOutput
	var idx uint
	var firstErr error
	center := image.Pt((target.Min.X+target.Max.X)/2, (target.Min.Y+target.Max.Y)/2)
	for i := uint(0); i < 6; i++ {
		o, err := d.open(i)
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
			// Lost access (alt-tab out of fullscreen, resolution change, UAC prompt): reopen.
			d.drop(idx)
			return ddaResp{err: err}
		}
		// Nothing changed on screen since the last frame we took: it is still current.
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
