// Throwaway stores for tests, sqlite unless a server is set
package storetest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/db/stores"
)

// Postgres dsn that switches every store test onto a server
const EnvDSN = "DISTROFACE_TEST_DATABASE_DSN"

// Fresh store per test, postgres tests get a private schema
func New(tb testing.TB) *stores.Store {
	tb.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		store, err := stores.NewSQLiteStore(filepath.Join(tb.TempDir(), "test.db"))
		if err != nil {
			tb.Fatalf("open sqlite store: %v", err)
		}
		tb.Cleanup(func() { _ = store.Close() })
		return store
	}

	driver := stores.DetectDriver(dsn)
	if driver != stores.DriverPostgres {
		tb.Fatalf("%s must be a postgres dsn, got %q", EnvDSN, dsn)
	}
	admin, _, err := stores.OpenDB(stores.Options{Driver: driver, DSN: dsn, DBConfig: stores.DBConfig{MaxOpenConns: 1}})
	if err != nil {
		tb.Fatalf("connect %s: %v", EnvDSN, err)
	}
	schema := "t_" + randomHex(8)
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		tb.Fatalf("create schema %s: %v", schema, err)
	}
	store, err := stores.Open(stores.Options{
		Driver:   driver,
		DSN:      withSearchPath(dsn, schema),
		DBConfig: stores.DBConfig{MaxOpenConns: 8, MaxIdleConns: 2},
	})
	if err != nil {
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		tb.Fatalf("open postgres store: %v", err)
	}
	tb.Cleanup(func() {
		_ = store.Close()
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return store
}

// Pins the connection to one schema through a runtime parameter
func withSearchPath(dsn, schema string) string {
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
