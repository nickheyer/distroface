package stores

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
	if got := sqliteDSN("a.db?_journal_mode=DELETE"); got != "a.db?_journal_mode=DELETE" {
		t.Errorf("explicit options rewritten: %q", got)
	}
	if got := sqliteDSN("a.db"); got != "a.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_txlock=immediate" {
		t.Errorf("default options missing: %q", got)
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
