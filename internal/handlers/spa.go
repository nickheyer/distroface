package handlers

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/nickheyer/distroface/internal/config"
)

type SPAHandler struct {
	StaticPath string
	IndexPath  string
	config     *config.Config
}

// MIME TYPES MAP
var MimeTypes = map[string]string{
	".css":  "text/css",
	".js":   "application/javascript",
	".json": "application/json",
	".html": "text/html",
	".ico":  "image/x-icon",
	".png":  "image/png",
	".svg":  "image/svg+xml",
}

func NewSPAHandler(cfg *config.Config, staticPath, indexPath string) SPAHandler {
	return SPAHandler{
		StaticPath: staticPath,
		IndexPath:  indexPath,
		config:     cfg,
	}
}

func (h SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// GET ABSOLUTE PATH TO REQUESTED FILE
	path := filepath.Join(h.StaticPath, r.URL.Path)

	// CHECK IF PATH EXISTS
	stat, err := os.Stat(path)
	if os.IsNotExist(err) || stat.IsDir() {
		// SERVE INDEX.HTML FOR ALL ROUTES
		indexFile := filepath.Join(h.StaticPath, h.IndexPath)
		http.ServeFile(w, r, indexFile)
		return
	}

	// SET PROPER MIME TYPE
	ext := filepath.Ext(path)
	if mimeType, ok := MimeTypes[ext]; ok {
		w.Header().Set("Content-Type", mimeType)
	}

	// SERVE THE STATIC FILE
	http.ServeFile(w, r, path)
}
