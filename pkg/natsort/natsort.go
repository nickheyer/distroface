package natsort

import (
	"sort"
	"strconv"
)

// Compare orders digit runs numerically and the rest lexically
func Compare(a, b string) int {
	for a != "" && b != "" {
		an, arest, aIsNum := chunk(a)
		bn, brest, bIsNum := chunk(b)
		if aIsNum && bIsNum {
			ai, _ := strconv.ParseInt(an, 10, 64)
			bi, _ := strconv.ParseInt(bn, 10, 64)
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		} else if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
		a, b = arest, brest
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return 0
}

// SortDesc puts version like strings newest first
func SortDesc(items []string) {
	sort.Slice(items, func(i, j int) bool {
		return Compare(items[i], items[j]) > 0
	})
}

// SortAsc puts version like strings oldest first
func SortAsc(items []string) {
	sort.Slice(items, func(i, j int) bool {
		return Compare(items[i], items[j]) < 0
	})
}

// Leading digit run or leading non digit run
func chunk(s string) (head, rest string, isNum bool) {
	if s == "" {
		return "", "", false
	}
	isNum = s[0] >= '0' && s[0] <= '9'
	i := 0
	for i < len(s) {
		d := s[i] >= '0' && s[i] <= '9'
		if d != isNum {
			break
		}
		i++
	}
	return s[:i], s[i:], isNum
}
