package actions

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five field schedule
type Cron struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

var cronAliases = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var cronMonths = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var cronDays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseCron accepts standard five field syntax and the @ aliases
func ParseCron(spec string) (*Cron, error) {
	spec = strings.TrimSpace(spec)
	if alias, ok := cronAliases[strings.ToLower(spec)]; ok {
		spec = alias
	}
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, errors.New("cron needs five fields")
	}
	c := &Cron{}
	var err error
	if c.minute, _, err = cronField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hour, _, err = cronField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.dom, c.domStar, err = cronField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if c.month, _, err = cronField(fields[3], 1, 12, cronMonths); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dow, c.dowStar, err = cronField(fields[4], 0, 7, cronDays); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1
	}
	return c, nil
}

// Matches reports whether the minute containing t fires
func (c *Cron) Matches(t time.Time) bool {
	t = t.UTC()
	if c.minute&(1<<uint(t.Minute())) == 0 || c.hour&(1<<uint(t.Hour())) == 0 || c.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	}
	return domOK || dowOK
}

func cronField(f string, lo, hi int, names map[string]int) (uint64, bool, error) {
	var bits uint64
	star := false
	for _, part := range strings.Split(f, ",") {
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
			step = s
			part = part[:i]
		}
		start, end := lo, hi
		switch {
		case part == "*":
			if step == 1 {
				star = true
			}
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if start, err = cronAtom(a, names); err != nil {
				return 0, false, err
			}
			if end, err = cronAtom(b, names); err != nil {
				return 0, false, err
			}
		default:
			v, err := cronAtom(part, names)
			if err != nil {
				return 0, false, err
			}
			start = v
			if !strings.Contains(f, "/") {
				end = v
			}
		}
		if start < lo || end > hi || start > end {
			return 0, false, fmt.Errorf("value out of range in %q", part)
		}
		for v := start; v <= end; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, star, nil
}

func cronAtom(s string, names map[string]int) (int, error) {
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return v, nil
}
