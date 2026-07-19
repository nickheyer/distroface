package portal

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

type mappingRule struct {
	pattern *regexp.Regexp
	replace string
}

// Ordered first-match-wins rule set for rewriting repo names
type pathMapper struct {
	rules []mappingRule
	log   *logger.Logger
}

// Compiles mapping rules, nil when no rules are given
func newPathMapper(rules []*v1.PortalRule, log *logger.Logger) (*pathMapper, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	m := &pathMapper{log: log}
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

// Validates that rules compile
func ValidateRules(rules []*v1.PortalRule, log *logger.Logger) error {
	_, err := newPathMapper(rules, log)
	return err
}

// Applies first matching rule, else return name given
func (m *pathMapper) MapName(name string) string {
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
