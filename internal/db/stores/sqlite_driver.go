package stores

import (
	"context"
	"database/sql/driver"
	"reflect"
	"time"
)

// Text the cgo driver wrote, old rows compare against it
const sqliteTimeFormat = "2006-01-02 15:04:05.999999999-07:00"

// Rewrites time params before the driver formats them
type sqliteConnector struct{ driver.Connector }

func (c sqliteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &sqliteConn{conn}, nil
}

type sqliteConn struct{ driver.Conn }

type sqliteStmt struct{ driver.Stmt }

var (
	_ driver.ConnPrepareContext = (*sqliteConn)(nil)
	_ driver.ConnBeginTx        = (*sqliteConn)(nil)
	_ driver.ExecerContext      = (*sqliteConn)(nil)
	_ driver.NamedValueChecker  = (*sqliteConn)(nil)
	_ driver.StmtExecContext    = (*sqliteStmt)(nil)
	_ driver.StmtQueryContext   = (*sqliteStmt)(nil)
	_ driver.NamedValueChecker  = (*sqliteStmt)(nil)
)

func (c *sqliteConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &sqliteStmt{stmt}, nil
}

func (c *sqliteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &sqliteStmt{stmt}, nil
}

func (c *sqliteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *sqliteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *sqliteConn) CheckNamedValue(nv *driver.NamedValue) error {
	return checkSQLiteArg(nv, c.Conn)
}

func (s *sqliteStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *sqliteStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

func (s *sqliteStmt) CheckNamedValue(nv *driver.NamedValue) error {
	return checkSQLiteArg(nv, s.Stmt)
}

// Times become legacy text, everything else keeps the driver's rules
func checkSQLiteArg(nv *driver.NamedValue, inner any) error {
	if t, ok := timeArg(nv.Value); ok {
		nv.Value = t.Format(sqliteTimeFormat)
		return nil
	}
	if checker, ok := inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// Looks through pointers and valuers for a time
func timeArg(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case *time.Time:
		if x == nil {
			return time.Time{}, false
		}
		return *x, true
	case driver.Valuer:
		rv := reflect.ValueOf(x)
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return time.Time{}, false
		}
		val, err := x.Value()
		if err != nil {
			return time.Time{}, false
		}
		t, ok := val.(time.Time)
		return t, ok
	}
	return time.Time{}, false
}
