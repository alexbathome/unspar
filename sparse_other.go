//go:build !unix

package main

import "os"

// hasHoles can't tell on this platform, so it never blocks a conversion.
func hasHoles(os.FileInfo) bool { return true }
