package stores

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Aggregate columns lose decltype so driver returns raw text
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
	for _, f := range sqlite3.SQLiteTimestampFormats {
		if v, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			t.Time = v
			return nil
		}
	}
	return fmt.Errorf("unrecognized time %q", s)
}
