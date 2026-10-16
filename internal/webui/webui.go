// Package webui embeds production frontend assets in the server executable.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:assets
var assets embed.FS

func Handler() http.Handler {
	dist, _ := fs.Sub(assets, "assets/dist")
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, err := fs.Stat(dist, "index.html"); err != nil {
			http.Error(w, "UI is not built; run make build or use the Vite development server", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/assets/") && r.URL.Path != "/favicon.svg" {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
