package gitrepo

import (
	"regexp"
	"sync"
)

var (
	reMu    sync.Mutex
	reCache = map[string]*regexp.Regexp{}
)

// Cached regexp match for glob derived patterns
func regexpMatch(pattern, s string) (bool, error) {
	reMu.Lock()
	re, ok := reCache[pattern]
	reMu.Unlock()
	if !ok {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return false, err
		}
		reMu.Lock()
		if len(reCache) > 512 {
			reCache = map[string]*regexp.Regexp{}
		}
		reCache[pattern] = re
		reMu.Unlock()
	}
	return re.MatchString(s), nil
}
