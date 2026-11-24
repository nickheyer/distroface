package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Canonical hash of a property set, empty keys skipped
func PropsFingerprint(properties map[string]string) string {
	keys := make([]string, 0, len(properties))
	for k := range properties {
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write(fmt.Appendf(nil, "%d:%s%d:%s;", len(k), k, len(properties[k]), properties[k]))
	}
	return hex.EncodeToString(h.Sum(nil))
}
