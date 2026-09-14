package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// MountStatic wires SPA serving. fs can be an http.Dir (dev, STATIC_DIR) or
// an embed.FS wrapper (release build). When index.html is missing the app
// stays API-only (Python skipped the static mount entirely).
//
// Semantics mirror app/main.py: /assets gets immutable caching; any other
// GET resolves to the file if it exists, else index.html (SPA history-mode
// fallback; path-escape attempts also land on index.html). Unknown /api/*,
// /rclone/* and non-GET requests get JSON 404s.
func MountStatic(r *gin.Engine, fs http.FileSystem) {
	if fs == nil {
		return
	}
	if f, err := fs.Open("/index.html"); err != nil {
		return // no built frontend → API-only
	} else {
		_ = f.Close()
	}

	r.GET("/assets/*filepath", func(c *gin.Context) {
		serveStaticFile(c, fs, "/assets"+c.Param("filepath"))
	})
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if c.Request.Method != http.MethodGet ||
			strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/rclone") || p == "/healthz" {
			AbortDetail(c, http.StatusNotFound, "Not Found")
			return
		}
		serveStaticFile(c, fs, p)
	})
}

func serveStaticFile(c *gin.Context, fs http.FileSystem, name string) {
	clean := path.Clean("/" + name)
	f, err := fs.Open(clean)
	if err != nil {
		// Missing or escaping → SPA fallback.
		serveIndex(c, fs)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		serveIndex(c, fs)
		return
	}
	if strings.HasPrefix(clean, "/assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(c.Writer, c.Request, stat.Name(), stat.ModTime(), f)
}

func serveIndex(c *gin.Context, fs http.FileSystem) {
	f, err := fs.Open("/index.html")
	if err != nil {
		AbortDetail(c, http.StatusNotFound, "Not Found")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		AbortDetail(c, http.StatusNotFound, "Not Found")
		return
	}
	http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), f)
}

// DiskStaticFS returns an http.FileSystem for STATIC_DIR, or nil when the
// directory lacks index.html (dev override; release builds use embed).
func DiskStaticFS(dir string) http.FileSystem {
	if dir == "" {
		return nil
	}
	if info, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || info.IsDir() {
		return nil
	}
	return http.Dir(dir)
}
