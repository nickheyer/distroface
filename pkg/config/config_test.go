package config

import (
	"strings"
	"testing"
)

func TestRedactedHidesEveryPasswordForm(t *testing.T) {
	cases := []string{
		"postgres://df:secret@db:5432/df?sslmode=disable",
		"postgres://df@db:5432/df?password=secret&sslmode=disable",
		"postgres://df@db:5432/df?PASSWORD=secret",
		"host=db user=df password=secret dbname=df",
		"host=db user=df passfile=secret dbname=df",
	}
	for _, dsn := range cases {
		got := DatabaseConfig{Driver: DatabasePostgres, DSN: dsn}.Redacted()
		if strings.Contains(got, "secret") {
			t.Errorf("Redacted(%q) = %q leaks the password", dsn, got)
		}
		if !strings.Contains(got, "db") {
			t.Errorf("Redacted(%q) = %q lost the host", dsn, got)
		}
	}
}

func TestRedactedSQLiteShowsPath(t *testing.T) {
	got := DatabaseConfig{Driver: DatabaseSQLite, Path: "/srv/df/app.db", DSN: "/srv/df/app.db"}.Redacted()
	if got != "/srv/df/app.db" {
		t.Errorf("Redacted() = %q", got)
	}
}
