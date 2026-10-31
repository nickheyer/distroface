package stores

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// Text shapes sqlite returns when aggregates drop the decltype
var textTimeFormats = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// Aggregate columns may arrive typed or as raw text
type sqlTime struct{ time.Time }

func (t sqlTime) Value() (driver.Value, error) { return t.Time, nil }

func (t *sqlTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case time.Time:
		t.Time = x
		return nil
	case string:
		return t.parse(x)
	case []byte:
		return t.parse(string(x))
	}
	return fmt.Errorf("cannot scan %T into time", v)
}

func (t *sqlTime) parse(s string) error {
	s = strings.TrimSuffix(s, "Z")
	for _, f := range textTimeFormats {
		if v, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			t.Time = v
			return nil
		}
	}
	return fmt.Errorf("unrecognized time %q", s)
}
