package rpc

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/nickheyer/distroface/pkg/utils"
)

// Serves uploaded profile images, public like the usernames they belong to
func (s *Server) avatarHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		row, err := s.Store.GetUserAvatar(r.Context(), id)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if row == nil {
			http.NotFound(w, r)
			return
		}
		etag := `"` + strconv.FormatInt(row.UpdatedAt.Unix(), 10) + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		utils.HardenUserContent(w.Header(), row.ContentType)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Length", strconv.Itoa(len(row.Data)))
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(row.Data))
	})
}
