package registry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nickheyer/distroface/pkg/logger"
)

// Extracts repo name from OCI path, filters OCI keywords
var apiRoutePattern = regexp.MustCompile(`^/v2/(.+)/((?:manifests|tags|referrers|blobs)/.*)$`)

// Extracts the artifact repo name from the v1 data plane path
var artifactRoutePattern = regexp.MustCompile(`^/api/v1/artifacts/([^/]+)/(.+)$`)

// First segment control-plane keywords never namespace rewritten
var artifactReservedRepo = map[string]bool{"repos": true, "search": true}

// Rewrites a repository name
type MappingRule struct {
	Pattern string `json:"pattern"` // Regex, anchored to the full name
	Replace string `json:"replace"` // Supports go regexp expansion ($1, ${0}, ${name})
}

type mappingRule struct {
	pattern *regexp.Regexp
	replace string
}

// Ordered first-match-wins rule set for rewriting repo names
type PathMapper struct {
	rules []mappingRule
	log   *logger.Logger
}

// Compiles mapping rules, nil when no rules are given
func NewPathMapper(rules []MappingRule, log *logger.Logger) (*PathMapper, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	m := &PathMapper{log: log}
	for i, r := range rules {
		if r.Pattern == "" || r.Replace == "" {
			return nil, fmt.Errorf("rules[%d]: pattern and replace are both required", i)
		}
		re, err := regexp.Compile("^(?:" + r.Pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("rules[%d]: invalid pattern %q: %w", i, r.Pattern, err)
		}
		m.rules = append(m.rules, mappingRule{pattern: re, replace: r.Replace})
	}
	return m, nil
}

// Applies first matching rule, else return name given
func (m *PathMapper) MapName(name string) string {
	if m == nil {
		return name
	}
	for _, rule := range m.rules {
		if !rule.pattern.MatchString(name) {
			continue
		}
		mapped := rule.pattern.ReplaceAllString(name, rule.replace)
		if !validRepoName(mapped) {
			m.log.Error("path mapping produced invalid repository name %q from %q - rule skipped", mapped, name)
			return name
		}
		return mapped
	}
	return name
}

func validRepoName(name string) bool {
	if name == "" {
		return false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" {
			return false
		}
	}
	return true
}
