package migrate

import (
	"fmt"
	"regexp"
	"strings"
)

// namespaceRegex mirrors usernameRegex in internal/rpc/services/auth.go: org and
// user names must satisfy it, and repo namespaces are org/user names.
var namespaceRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,38}[a-z0-9]$`)

// MapRepoName maps a v1 repo name to its v2 namespace/name form. Flat names are
// prefixed into the legacy org namespace; two-level names pass through untouched.
// This matches the portal `map_unqualified` semantics that legacy CI hosts use.
func MapRepoName(v1Name, legacyNS string) string {
	if strings.Contains(v1Name, "/") {
		return v1Name
	}
	return legacyNS + "/" + v1Name
}

// SplitRepoName splits a mapped name into namespace and name.
func SplitRepoName(mapped string) (namespace, name string) {
	parts := strings.SplitN(mapped, "/", 2)
	if len(parts) != 2 {
		return "", mapped
	}
	return parts[0], parts[1]
}

// ValidateNamespace checks a namespace against v2's org/user name rules.
func ValidateNamespace(ns string) error {
	if !namespaceRegex.MatchString(ns) {
		return fmt.Errorf("namespace %q does not satisfy v2 name rules (%s)", ns, namespaceRegex)
	}
	return nil
}

// V1Namespaces returns the set of two-level namespace prefixes present in names.
func V1Namespaces(names []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, n := range names {
		if i := strings.Index(n, "/"); i > 0 {
			ns := n[:i]
			if !seen[ns] {
				seen[ns] = true
				out = append(out, ns)
			}
		}
	}
	return out
}
