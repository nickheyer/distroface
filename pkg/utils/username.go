package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Usernames double as personal registry namespaces
var UsernameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,38}[a-z0-9]$`)

var (
	invalidUserChars = regexp.MustCompile(`[^a-z0-9_.-]+`)
	separatorRuns    = regexp.MustCompile(`[_.-]{2,}`)
	edgeSeparators   = regexp.MustCompile(`^[_.-]+|[_.-]+$`)
)

// Reduces a raw value to a valid namespace or empty
func SlugUsername(candidate string) string {
	s := candidate
	if at := strings.IndexByte(s, '@'); at != -1 {
		s = s[:at]
	}
	s = strings.ToLower(s)
	s = invalidUserChars.ReplaceAllString(s, "-")
	s = separatorRuns.ReplaceAllString(s, "-")
	s = edgeSeparators.ReplaceAllString(s, "")
	if len(s) > 40 {
		s = edgeSeparators.ReplaceAllString(s[:40], "")
	}
	if !UsernameRegex.MatchString(s) {
		return ""
	}
	return s
}

// First candidate that slugs clean else hashed seed
func UsernameFromClaims(seed string, candidates ...string) string {
	for _, c := range candidates {
		if s := SlugUsername(c); s != "" {
			return UnreservedSlug(s)
		}
	}
	if s := SlugUsername(seed); s != "" {
		return UnreservedSlug(s)
	}
	sum := sha256.Sum256([]byte(seed))
	return "user-" + hex.EncodeToString(sum[:4])
}

// First path segments owned by routes, never a user or org
var ReservedNamespaces = map[string]bool{
	"admin": true, "api": true, "artifacts": true, "images": true, "orgs": true,
	"settings": true, "login": true, "logout": true, "register": true,
	"change-password": true, "forgot-password": true, "reset-password": true,
	"verify-email": true, "docs": true, "notifications": true, "explore": true,
	"v2": true, "auth": true, "acme": true, "assets": true, "static": true,
	"favicon.ico": true, "robots.txt": true, "git": true, "new": true,
}

// Reports whether a username or org name may own a namespace
func NamespaceAllowed(name string) bool {
	return UsernameRegex.MatchString(name) && !ReservedNamespaces[name]
}

// Appends a numeric suffix until the slug leaves the reserved set
func UnreservedSlug(s string) string {
	if !ReservedNamespaces[s] {
		return s
	}
	for i := 1; ; i++ {
		candidate := SlugUsername(fmt.Sprintf("%s-%d", s, i))
		if candidate != "" && !ReservedNamespaces[candidate] {
			return candidate
		}
	}
}
