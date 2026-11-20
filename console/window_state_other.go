//go:build !windows

package main

import "github.com/egoist/mygo"

func windowPixelBounds(*mygo.Window) (x, y, w, h int, ok bool) {
	return 0, 0, 0, 0, false
}
