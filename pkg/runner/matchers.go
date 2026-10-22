package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Group names a matcher pattern may bind
var matcherGroupNames = []string{"file", "fromPath", "line", "column", "endLine", "endColumn", "location", "severity", "code", "message"}

// One problem matcher registered through add-matcher
type problemMatcher struct {
	owner    string
	severity string
	patterns []*matcherPattern
	// Values gathered by the patterns before the last one
	state []map[string]string
}

// One regexp of a matcher with the groups it binds
type matcherPattern struct {
	re     *regexp.Regexp
	groups map[string]int
	loop   bool
}

// Shape of a matcher json file
type matcherFile struct {
	ProblemMatcher []struct {
		Owner    string           `json:"owner"`
		Severity string           `json:"severity"`
		Pattern  []map[string]any `json:"pattern"`
	} `json:"problemMatcher"`
}

// Parses a matcher file with the hosted validations
func parseMatchers(data []byte) ([]*problemMatcher, error) {
	var f matcherFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if len(f.ProblemMatcher) == 0 {
		return nil, errors.New("no problemMatcher entries")
	}
	var out []*problemMatcher
	for _, raw := range f.ProblemMatcher {
		m := &problemMatcher{owner: strings.TrimSpace(raw.Owner), severity: strings.ToLower(strings.TrimSpace(raw.Severity))}
		if m.owner == "" {
			return nil, errors.New("matcher owner is required")
		}
		if len(raw.Pattern) == 0 {
			return nil, fmt.Errorf("matcher %s has no patterns", m.owner)
		}
		hasMessage := false
		for i, rp := range raw.Pattern {
			p, err := parseMatcherPattern(rp)
			if err != nil {
				return nil, fmt.Errorf("matcher %s pattern %d: %w", m.owner, i+1, err)
			}
			last := i == len(raw.Pattern)-1
			if p.loop && (!last || len(raw.Pattern) == 1) {
				return nil, fmt.Errorf("matcher %s: loop is only allowed on the last of several patterns", m.owner)
			}
			if _, ok := p.groups["message"]; ok {
				hasMessage = true
			}
			m.patterns = append(m.patterns, p)
		}
		if !hasMessage {
			return nil, fmt.Errorf("matcher %s binds no message group", m.owner)
		}
		m.state = make([]map[string]string, len(m.patterns))
		out = append(out, m)
	}
	return out, nil
}

func parseMatcherPattern(raw map[string]any) (*matcherPattern, error) {
	src, _ := raw["regexp"].(string)
	if src == "" {
		return nil, errors.New("regexp is required")
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, fmt.Errorf("regexp: %w", err)
	}
	p := &matcherPattern{re: re, groups: map[string]int{}}
	if v, ok := raw["loop"].(bool); ok {
		p.loop = v
	}
	for _, name := range matcherGroupNames {
		v, ok := raw[name]
		if !ok || v == nil {
			continue
		}
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) || int(f) < 0 || int(f) > re.NumSubexp() {
			return nil, fmt.Errorf("%s must name a capture group of the regexp", name)
		}
		p.groups[name] = int(f)
	}
	if _, loc := p.groups["location"]; loc {
		for _, k := range []string{"line", "column", "endLine", "endColumn"} {
			if _, ok := p.groups[k]; ok {
				return nil, fmt.Errorf("location and %s cannot both be set", k)
			}
		}
	}
	return p, nil
}

// Group values of a match layered over the running values
func (p *matcherPattern) values(sm []string, running map[string]string) map[string]string {
	out := make(map[string]string, len(running)+len(p.groups))
	for k, v := range running {
		out[k] = v
	}
	for name, idx := range p.groups {
		if idx < len(sm) {
			out[name] = sm[idx]
		}
	}
	return out
}

func (m *problemMatcher) reset() {
	for i := range m.state {
		m.state[i] = nil
	}
}

// Feeds a line, returns values when an issue completes
func (m *problemMatcher) match(line string) map[string]string {
	if len(m.patterns) == 1 {
		sm := m.patterns[0].re.FindStringSubmatch(line)
		if sm == nil {
			return nil
		}
		return m.patterns[0].values(sm, nil)
	}
	for i := len(m.patterns) - 1; i >= 0; i-- {
		var running map[string]string
		if i > 0 {
			if running = m.state[i-1]; running == nil {
				continue
			}
		}
		p := m.patterns[i]
		last := i == len(m.patterns)-1
		sm := p.re.FindStringSubmatch(line)
		if sm == nil {
			// Loop tail stopped, earlier patterns may take the line
			if last && p.loop {
				m.state[i-1] = nil
			}
			continue
		}
		vals := p.values(sm, running)
		if last {
			if !p.loop {
				m.reset()
			}
			return vals
		}
		m.state[i] = vals
		for k := i + 1; k < len(m.state); k++ {
			m.state[k] = nil
		}
		return nil
	}
	return nil
}

// Annotation for bound values, nil without a message
func (m *problemMatcher) issue(vals map[string]string, fileBase string) *Annotation {
	msg := strings.TrimSpace(vals["message"])
	if msg == "" {
		return nil
	}
	a := &Annotation{Level: matcherSeverity(vals["severity"], m.severity), Message: msg, Title: strings.TrimSpace(vals["code"])}
	if loc := strings.TrimSpace(vals["location"]); loc != "" {
		parts := strings.Split(loc, ",")
		switch len(parts) {
		case 1:
			a.Line = atoi(parts[0])
		case 2:
			a.Line, a.Col = atoi(parts[0]), atoi(parts[1])
		case 4:
			a.Line, a.Col, a.EndLine, a.EndCol = atoi(parts[0]), atoi(parts[1]), atoi(parts[2]), atoi(parts[3])
		}
	} else {
		a.Line, a.Col, a.EndLine, a.EndCol = atoi(vals["line"]), atoi(vals["column"]), atoi(vals["endLine"]), atoi(vals["endColumn"])
	}
	if file := strings.TrimSpace(vals["file"]); file != "" {
		file = strings.ReplaceAll(file, "\\", "/")
		if from := strings.TrimSpace(vals["fromPath"]); from != "" && !path.IsAbs(file) {
			file = path.Join(path.Dir(strings.ReplaceAll(from, "\\", "/")), file)
		}
		if fileBase != "" {
			if rel, ok := strings.CutPrefix(file, strings.TrimSuffix(fileBase, "/")+"/"); ok {
				file = rel
			}
		}
		a.File = file
	}
	return a
}

func matcherSeverity(bound, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(bound)) {
	case "error":
		return "error"
	case "warning":
		return "warning"
	case "notice", "info":
		return "notice"
	}
	switch fallback {
	case "warning":
		return "warning"
	case "notice", "info":
		return "notice"
	}
	return "error"
}

// Matchers of one job, first matching owner wins a line
type matcherSet struct {
	mu       sync.Mutex
	matchers []*problemMatcher
}

func newMatcherSet() *matcherSet {
	return &matcherSet{}
}

// Registers matchers, replacing any with the same owner
func (s *matcherSet) add(list []*problemMatcher) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range list {
		s.removeLocked(m.owner)
		s.matchers = append(s.matchers, m)
	}
}

func (s *matcherSet) remove(owner string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(owner)
}

func (s *matcherSet) removeLocked(owner string) {
	kept := s.matchers[:0]
	for _, m := range s.matchers {
		if !strings.EqualFold(m.owner, owner) {
			kept = append(kept, m)
		}
	}
	s.matchers = kept
}

func (s *matcherSet) owners() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.matchers))
	for _, m := range s.matchers {
		out = append(out, m.owner)
	}
	return out
}

// First matcher producing an issue wins the line
func (s *matcherSet) match(line, fileBase string) *Annotation {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.matchers {
		if vals := m.match(line); vals != nil {
			if a := m.issue(vals, fileBase); a != nil {
				return a
			}
		}
	}
	return nil
}

func quoteOwners(owners []string) string {
	quoted := make([]string, len(owners))
	for i, o := range owners {
		quoted[i] = strconv.Quote(o)
	}
	return strings.Join(quoted, ", ")
}
