package stores

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

func TestParseDriver(t *testing.T) {
	cases := map[string]Driver{
		"":           DriverSQLite,
		"sqlite":     DriverSQLite,
		"SQLite3":    DriverSQLite,
		"postgres":   DriverPostgres,
		"postgresql": DriverPostgres,
		" pg ":       DriverPostgres,
	}
	for in, want := range cases {
		got, err := ParseDriver(in)
		if err != nil || got != want {
			t.Errorf("ParseDriver(%q) = %q, %v want %q", in, got, err, want)
		}
	}
	if _, err := ParseDriver("oracle"); err == nil {
		t.Error("ParseDriver(oracle) accepted an unsupported backend")
	}
}

func TestDetectDriver(t *testing.T) {
	cases := map[string]Driver{
		"./data/distroface.db":                      DriverSQLite,
		"/srv/df/app.db?_busy_timeout=1":            DriverSQLite,
		"postgres://u:p@db:5432/df?sslmode=disable": DriverPostgres,
		"postgresql://db/df":                        DriverPostgres,
		"host=db user=df dbname=df sslmode=disable": DriverPostgres,
	}
	for in, want := range cases {
		if got := DetectDriver(in); got != want {
			t.Errorf("DetectDriver(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSQLiteDSNKeepsExplicitOptions(t *testing.T) {
	if got := sqliteDSN("a.db?_pragma=journal_mode(DELETE)"); got != "file:a.db?_pragma=journal_mode(DELETE)" {
		t.Errorf("explicit options rewritten: %q", got)
	}
	if got := sqliteDSN("file:a.db?mode=ro"); got != "file:a.db?mode=ro" {
		t.Errorf("uri rewritten: %q", got)
	}
	if got := sqliteDSN("a.db"); got != "file:a.db?"+sqliteDefaultOptions {
		t.Errorf("default options missing: %q", got)
	}
	if got := sqliteDSN("./data/50% full#1.db"); got != "file:./data/50%25 full%231.db?"+sqliteDefaultOptions {
		t.Errorf("uri bytes not escaped: %q", got)
	}
}

// Pragmas from the dsn must land on every connection
func TestSQLiteDSNAppliesPragmas(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "odd name%.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var journal string
	var foreignKeys, busy int
	store.db.Raw("PRAGMA journal_mode").Scan(&journal)
	store.db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys)
	store.db.Raw("PRAGMA busy_timeout").Scan(&busy)
	if journal != "wal" || foreignKeys != 1 || busy != 5000 {
		t.Fatalf("journal=%q foreign_keys=%d busy_timeout=%d", journal, foreignKeys, busy)
	}
}

// New rows keep the old text shape so comparisons hold
func TestSQLiteTimeTextMatchesLegacyRows(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "times.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Exec("CREATE TABLE stamps (label TEXT PRIMARY KEY, at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 890123456, time.UTC)
	legacy := "2026-03-04 05:06:07.890123456+00:00"
	rows := []struct {
		label string
		at    any
	}{
		{"param", stamp},
		{"pointer", &stamp},
		{"valuer", sql.NullTime{Time: stamp, Valid: true}},
		{"legacy", legacy},
		{"bare", "2026-03-04 05:06:07"},
	}
	for _, r := range rows {
		if err := store.db.Exec("INSERT INTO stamps (label, at) VALUES (?, ?)", r.label, r.at).Error; err != nil {
			t.Fatalf("%s: %v", r.label, err)
		}
	}
	for _, label := range []string{"param", "pointer", "valuer"} {
		var text string
		store.db.Raw("SELECT CAST(at AS TEXT) FROM stamps WHERE label = ?", label).Scan(&text)
		if text != legacy {
			t.Errorf("%s stored as %q want %q", label, text, legacy)
		}
	}
	var got []struct {
		Label string
		At    time.Time
	}
	if err := store.db.Raw("SELECT label, at FROM stamps ORDER BY label").Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		want := stamp
		if g.Label == "bare" {
			want = stamp.Truncate(time.Second)
		}
		if !g.At.Equal(want) {
			t.Errorf("%s read back as %v want %v", g.Label, g.At, want)
		}
	}
	var later int64
	store.db.Raw("SELECT COUNT(*) FROM stamps WHERE at > ?", stamp.Add(-time.Nanosecond)).Scan(&later)
	if later != 4 {
		t.Errorf("text comparison against a time param matched %d rows, want 4", later)
	}
}

func TestSQLiteDuplicateKeepsDriverDetail(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Create(&db.Role{ID: "r1", Name: "dup"}).Error; err != nil {
		t.Fatal(err)
	}
	err = store.db.Create(&db.Role{ID: "r2", Name: "dup"}).Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("duplicate not translated: %v", err)
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("driver detail dropped: %v", err)
	}
}
