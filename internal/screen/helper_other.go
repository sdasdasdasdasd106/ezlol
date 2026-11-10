//go:build !windows

package screen

import "context"

func windowsHelper() func(ctx context.Context) ([]byte, error) { return nil }
