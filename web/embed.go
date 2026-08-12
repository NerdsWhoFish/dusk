// Package web carries the built UI into the binary.
//
// It lives beside the sources it embeds because go:embed cannot reach outside
// its own directory, so the alternative is copying the build output into a Go
// package, which is a step that gets forgotten and ships a stale UI.
package web

import (
	"embed"
	"io/fs"
)

// bundle is Vite's output, plus a placeholder so an unbuilt checkout compiles.
//
//go:embed all:dist
var bundle embed.FS

// Bundle returns the built UI, or false when this binary has none.
func Bundle() (fs.FS, bool) {
	root, err := fs.Sub(bundle, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return nil, false
	}
	return root, true
}
