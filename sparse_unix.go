//go:build unix

package main

import (
	"os"
	"syscall"
)

// hasHoles reports whether the file uses fewer disk blocks than its size,
// which means it contains holes.
func hasHoles(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int64(st.Blocks)*512 < info.Size()
}
