//go:build !windows

package screen

import "context"

func windowsHelper() func(ctx context.Context, dump string) ([]byte, error) { return nil }
