// Package adminnext embeds the React 19 + Ant Design 6 server console that is
// mounted at /next/ while the legacy Vue console keeps serving /admin/.
//
// Both consoles are built from web/ and committed as dist snapshots, so a plain
// `go build` produces a binary that serves both without Node installed.
package adminnext

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the console assets rooted at the Vite build directory.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
