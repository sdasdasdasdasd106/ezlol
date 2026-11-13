//go:build windows

package screen

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")

// GameVisible reports whether the game window is open and not minimized
// (an exclusive-fullscreen game minimizes when you switch out of it).
func GameVisible() bool {
	_, ok := gameRect()
	return ok
}

// GameNeedsAdmin reports whether the game runs as administrator while ezlol
// does not. Windows then keeps the game's keyboard input from ezlol, so the
// hotkeys cannot work in game until ezlol runs as administrator too.
func GameNeedsAdmin() bool {
	if windows.GetCurrentProcessToken().IsElevated() {
		return false
	}
	title, _ := windows.UTF16PtrFromString("League of Legends (TM) Client")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if h == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		// A process we may not even look into runs at a higher level than us.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer tok.Close()
	return tok.IsElevated()
}
