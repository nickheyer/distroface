package stores

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/postgres"
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
		connector, err := (&sqlite3.SQLite{}).OpenConnector(sqliteDSN(dsn))
		if err != nil {
			return nil, err
		}
		return sqliteDialector{gormlite.OpenDB(sql.OpenDB(sqliteConnector{connector}))}, nil
	case DriverPostgres:
		return postgres.Open(dsn), nil
	}
	return nil, fmt.Errorf("unsupported database driver %q", driver)
}

// Keeps the driver detail behind the gorm sentinel
type sqliteDialector struct{ gorm.Dialector }

func (d sqliteDialector) Translate(err error) error {
	translator, ok := d.Dialector.(gorm.ErrorTranslator)
	if !ok {
		return err
	}
	translated := translator.Translate(err)
	if translated == nil || errors.Is(err, translated) {
		return err
	}
	return fmt.Errorf("%w: %w", translated, err)
}

func (d sqliteDialector) SavePoint(tx *gorm.DB, name string) error {
	return d.Dialector.(gorm.SavePointerDialectorInterface).SavePoint(tx, name)
}

func (d sqliteDialector) RollbackTo(tx *gorm.DB, name string) error {
	return d.Dialector.(gorm.SavePointerDialectorInterface).RollbackTo(tx, name)
}

// Busy timeout goes first so the WAL switch waits
const sqliteDefaultOptions = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"

// Plain paths gain the defaults, an explicit query string wins
func sqliteDSN(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	if strings.Contains(path, "?") {
		return "file:" + path
	}
	return "file:" + sqliteURIPath(path) + "?" + sqliteDefaultOptions
}

// Only these bytes change meaning inside a sqlite uri
var sqliteURIEscaper = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

func sqliteURIPath(path string) string {
	return sqliteURIEscaper.Replace(path)
}

func (s *Store) Driver() Driver {
	return s.driver
}

func (s *Store) isSQLite() bool {
	return s.driver == DriverSQLite
}
