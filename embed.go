// Package rclone_sync embeds the built React frontend (web/dist) into the
// release binary — single-binary distribution, no Node runtime at deploy.
// `make all` = npm build then go build. With only the committed .gitkeep
// placeholder, EmbeddedStatic returns nil and the server stays API-only
// (or serves STATIC_DIR from disk).
package rclone_sync

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist/web
var dist embed.FS

// EmbeddedStatic serves dist/web (nil when it holds only the placeholder —
// no index.html to serve).
func EmbeddedStatic() http.FileSystem {
	sub, err := fs.Sub(dist, "dist/web")
	if err != nil {
		return nil
	}
	return http.FS(sub)
}
