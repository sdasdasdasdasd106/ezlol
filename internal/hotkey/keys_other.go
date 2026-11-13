//go:build !windows

package hotkey

// Polling is Windows only; elsewhere the desktop shell's hotkeys are used.
var keyDown func(vk int) bool
