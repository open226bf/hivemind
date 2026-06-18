// Package web embeds the built Angular single-page app and serves it from the
// API's Gin engine, so one container exposes both the UI and the API on a single
// port. The dist directory is filled by the Docker build (the Angular build
// output replaces the committed placeholder); a stub index.html keeps `go build`
// working without a frontend build for local backend development.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var embedded embed.FS

// Register mounts the embedded SPA on the engine. Static assets are served from
// the build output; any other GET that doesn't match a file falls back to
// index.html so client-side routes (deep links, refresh) resolve. API, health
// and swagger routes keep their own handlers, and unmatched /api paths stay JSON
// 404s rather than returning the SPA shell.
func Register(r *gin.Engine) error {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return err
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		req := c.Request
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		rel := strings.TrimPrefix(path.Clean(req.URL.Path), "/")
		// Never shadow the API/infra namespaces with the SPA shell.
		if rel == "api" || strings.HasPrefix(rel, "api/") {
			c.Status(http.StatusNotFound)
			return
		}
		// A real, non-index asset: serve it. Angular hashes filenames
		// (outputHashing: all), so these are safe to cache hard.
		if rel != "" && rel != "index.html" {
			if f, err := dist.Open(rel); err == nil {
				_ = f.Close()
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
				fileServer.ServeHTTP(c.Writer, req)
				return
			}
		}
		// SPA fallback — never cache the shell so a new deploy is picked up.
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
	return nil
}
