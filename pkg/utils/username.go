package utils

import (
	"crypto/sha256"
	"encoding/hex"
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
			return s
		}
	}
	if s := SlugUsername(seed); s != "" {
		return s
	}
	sum := sha256.Sum256([]byte(seed))
	return "user-" + hex.EncodeToString(sum[:4])
}
