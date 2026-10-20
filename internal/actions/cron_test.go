package actions

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestCronMatches(t *testing.T) {
	cases := []struct {
		spec string
		when string
		want bool
	}{
		{"* * * * *", "2026-10-14 09:30", true},
		{"30 9 * * *", "2026-10-14 09:30", true},
		{"30 9 * * *", "2026-10-14 09:31", false},
		{"*/15 * * * *", "2026-10-14 09:45", true},
		{"*/15 * * * *", "2026-10-14 09:50", false},
		{"0 0 1 * *", "2026-11-01 00:00", true},
		{"0 0 1 * *", "2026-11-02 00:00", false},
		{"0 12 * * 1-5", "2026-10-14 12:00", true},
		{"0 12 * * 1-5", "2026-10-17 12:00", false},
		{"0 12 * * mon,wed", "2026-10-14 12:00", true},
		{"0 12 * jan *", "2026-10-14 12:00", false},
		{"0 12 * oct *", "2026-10-14 12:00", true},
		{"0 0 * * 7", "2026-10-18 00:00", true},
		{"0 0 15 * 3", "2026-10-15 00:00", true},
		{"0 0 15 * 3", "2026-10-14 00:00", true},
		{"0 0 15 * 3", "2026-10-16 00:00", false},
		{"5-10/5 * * * *", "2026-10-14 09:10", true},
		{"5-10/5 * * * *", "2026-10-14 09:07", false},
		{"@hourly", "2026-10-14 09:00", true},
		{"@daily", "2026-10-14 00:00", true},
		{"@daily", "2026-10-14 01:00", false},
	}
	for _, c := range cases {
		cr, err := ParseCron(c.spec)
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if got := cr.Matches(at(c.when)); got != c.want {
			t.Errorf("%q at %s = %v, want %v", c.spec, c.when, got, c.want)
		}
	}
}

func TestCronErrors(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 32 * *", "* * * 13 *", "* * * * 8", "a * * * *", "*/0 * * * *", "10-5 * * * *"} {
		if _, err := ParseCron(spec); err == nil {
			t.Errorf("%q should fail", spec)
		}
	}
}
