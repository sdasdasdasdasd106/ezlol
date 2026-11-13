//go:build !windows

package screen

import "context"

func windowsHelper() func(ctx context.Context, dump string) ([]byte, error) { return nil }

// GameVisible: elsewhere the game window cannot be checked; assume it is there.
func GameVisible() bool { return true }

// GameNeedsAdmin is a Windows concern only.
func GameNeedsAdmin() bool { return false }
