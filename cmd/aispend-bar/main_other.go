//go:build !darwin

// aispend-bar is a macOS menu-bar app: it links the Cocoa-based menuet library (cgo),
// so it only builds and runs on macOS. This stub keeps `go build ./...` and the test
// gate green on Linux/Windows and explains why the real binary is macOS-only.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "aispend-bar is a macOS menu-bar app; build and run it on macOS (GOOS=darwin).")
	os.Exit(1)
}
