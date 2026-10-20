package actions

import (
	"regexp"
	"strings"
	"sync"
)

var globCache sync.Map

// MatchGlob implements the workflow filter glob syntax
func MatchGlob(pattern, s string) bool {
	re := globRegexp(pattern)
	return re.MatchString(s)
}

func globRegexp(pattern string) *regexp.Regexp {
	if v, ok := globCache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					sb.WriteString("(?:.*/)?")
				} else {
					sb.WriteString(".*")
				}
			} else {
				sb.WriteString("[^/]*")
			}
		case '?':
			sb.WriteString("[^/]")
		case '+':
			sb.WriteString("[^/]+")
		case '[':
			j := strings.IndexByte(pattern[i:], ']')
			if j < 0 {
				sb.WriteString(regexp.QuoteMeta("["))
				continue
			}
			sb.WriteString(pattern[i : i+j+1])
			i += j
		case '\\':
			if i+1 < len(pattern) {
				i++
				sb.WriteString(regexp.QuoteMeta(string(pattern[i])))
			}
		default:
			sb.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(pattern) + "$")
	}
	globCache.Store(pattern, re)
	return re
}

// MatchFilter applies an ordered include list with ! negations
func MatchFilter(patterns []string, s string) bool {
	matched := false
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			if matched && MatchGlob(p[1:], s) {
				matched = false
			}
			continue
		}
		if MatchGlob(p, s) {
			matched = true
		}
	}
	return matched
}

// Ref filter semantics shared by branches and tags
func refAllowed(include, ignore []string, name string) bool {
	if len(include) > 0 && !MatchFilter(include, name) {
		return false
	}
	if len(ignore) > 0 && MatchFilter(ignore, name) {
		return false
	}
	return true
}

// Path filter semantics, any changed file may satisfy it
func pathsAllowed(include, ignore []string, changed []string, known bool) bool {
	if len(include) == 0 && len(ignore) == 0 {
		return true
	}
	if !known {
		return true
	}
	for _, p := range changed {
		if len(include) > 0 && !MatchFilter(include, p) {
			continue
		}
		if len(ignore) > 0 && MatchFilter(ignore, p) {
			continue
		}
		return true
	}
	return false
}
