package stores

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Database backend name as written in config
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// Accepts the common spellings, empty means sqlite
func ParseDriver(name string) (Driver, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "sqlite", "sqlite3":
		return DriverSQLite, nil
	case "postgres", "postgresql", "pg", "pgx":
		return DriverPostgres, nil
	}
	return "", fmt.Errorf("unsupported database driver %q, want sqlite or postgres", name)
}

// Guesses the backend from the connection string shape
func DetectDriver(dsn string) Driver {
	lower := strings.ToLower(strings.TrimSpace(dsn))
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return DriverPostgres
	case strings.Contains(lower, "host=") || strings.Contains(lower, "dbname="):
		return DriverPostgres
	}
	return DriverSQLite
}

func newDialector(driver Driver, dsn string) (gorm.Dialector, error) {
	switch driver {
	case DriverSQLite:
		dial := sqlite.Open(sqliteDSN(dsn))
		if inner, ok := dial.(*sqlite.Dialector); ok {
			return sqliteDialector{inner}, nil
		}
		return dial, nil
	case DriverPostgres:
		return postgres.Open(dsn), nil
	}
	return nil, fmt.Errorf("unsupported database driver %q", driver)
}

// Keeps the driver detail behind the gorm sentinel
type sqliteDialector struct {
	*sqlite.Dialector
}

func (d sqliteDialector) Translate(err error) error {
	translated := d.Dialector.Translate(err)
	if translated == nil || errors.Is(err, translated) {
		return err
	}
	return fmt.Errorf("%w: %w", translated, err)
}

// WAL and busy timeout keep concurrent readers and writers civil
func sqliteDSN(path string) string {
	if strings.Contains(path, "?") {
		return path
	}
	return path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_txlock=immediate"
}

func (s *Store) Driver() Driver {
	return s.driver
}

func (s *Store) isSQLite() bool {
	return s.driver == DriverSQLite
}
