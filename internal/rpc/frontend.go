package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Sveltekit owns this directory, no client route lives under it
const appDir = "/_app"

// Hashed build output never changes under its name
const immutablePrefix = appDir + "/immutable/"

// Precompressed siblings the frontend build writes next to each asset
var precompressed = []struct{ coding, ext string }{{"br", ".br"}, {"gzip", ".gz"}}

// Serves the embedded spa with validators and precompressed bodies
type frontendHandler struct {
	fs    http.FileSystem
	etags map[string]string
}

func newFrontendHandler(fsys fs.FS) *frontendHandler {
	h := &frontendHandler{fs: http.FS(fsys), etags: map[string]string{}}
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if tag, err := hashFile(fsys, name); err == nil {
			h.etags["/"+name] = tag
		}
		return nil
	})
	return h
}

func (h *frontendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isConnectPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	name := path.Clean("/" + r.URL.Path)
	file, stat, err := h.open(name)
	if err != nil || !stat.Mode().IsRegular() {
		if err == nil {
			_ = file.Close()
		}
		// Stale clients asking for vanished chunks get a real 404
		if name == appDir || strings.HasPrefix(name, appDir+"/") {
			http.NotFound(w, r)
			return
		}
		// Client side routes all boot from the shell
		name = "/index.html"
		if file, stat, err = h.open(name); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer func() { _ = file.Close() }()

	served := name
	for _, enc := range precompressed {
		if !accepts(r.Header.Get("Accept-Encoding"), enc.coding) {
			continue
		}
		f, st, err := h.open(name + enc.ext)
		if err != nil {
			continue
		}
		_ = file.Close()
		file, stat, served = f, st, name+enc.ext
		w.Header().Set("Content-Encoding", enc.coding)
		break
	}

	hdr := w.Header()
	hdr.Add("Vary", "Accept-Encoding")
	if strings.HasPrefix(name, immutablePrefix) {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Unhashed files revalidate, the etag turns a hit into a 304
		hdr.Set("Cache-Control", "no-cache")
	}
	if tag := h.etags[served]; tag != "" {
		hdr.Set("ETag", tag)
	}
	if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
		hdr.Set("Content-Type", ctype)
	}
	http.ServeContent(w, r, name, stat.ModTime(), file)
}

func (h *frontendHandler) open(name string) (http.File, fs.FileInfo, error) {
	f, err := h.fs.Open(name)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// Strong validator over the bytes, the embedded build never changes
func hashFile(fsys fs.FS, name string) (string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return `"` + hex.EncodeToString(sum.Sum(nil)[:16]) + `"`, nil
}

// Reports whether an Accept-Encoding header admits the coding
func accepts(header, coding string) bool {
	wildcard := false
	for part := range strings.SplitSeq(header, ",") {
		token, params, _ := strings.Cut(part, ";")
		token = strings.TrimSpace(token)
		switch {
		case strings.EqualFold(token, coding):
			return !zeroQ(params)
		case token == "*":
			wildcard = !zeroQ(params)
		}
	}
	return wildcard
}

// Reports whether the params carry an explicit q=0
func zeroQ(params string) bool {
	q, ok := strings.CutPrefix(strings.TrimSpace(params), "q=")
	if !ok {
		return false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(q), 64)
	return err == nil && v == 0
}
