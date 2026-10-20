package utils

import (
	"net/http"
	"strings"
)

// CSP that keeps user supplied files inert when opened directly
const UserContentCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// Marks a response as user supplied, scriptable types become plain text
func HardenUserContent(h http.Header, contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	switch {
	case mediaType == "text/html", mediaType == "application/xhtml+xml",
		strings.HasSuffix(mediaType, "/xml"), strings.HasSuffix(mediaType, "+xml") && mediaType != "image/svg+xml":
		contentType = "text/plain; charset=utf-8"
	}
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", UserContentCSP)
	return contentType
}
