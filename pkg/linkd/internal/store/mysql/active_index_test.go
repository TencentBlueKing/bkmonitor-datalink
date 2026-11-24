// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package mysqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/store"
)

type indexSQLFixture struct {
	query string
	args  []driver.NamedValue
	err   error
}

type indexSQLConnector struct{ fixture *indexSQLFixture }

func (c indexSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &indexSQLConn{fixture: c.fixture}, nil
}

func (c indexSQLConnector) Driver() driver.Driver { return c }

func (c indexSQLConnector) Open(string) (driver.Conn, error) { return c.Connect(context.Background()) }

type indexSQLConn struct {
	writeSQLConn
	fixture *indexSQLFixture
}

func (c *indexSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.fixture.query = query
	c.fixture.args = args
	if c.fixture.err != nil {
		return nil, c.fixture.err
	}
	return &indexSQLRows{}, nil
}

type indexSQLRows struct{ done bool }

func (*indexSQLRows) Columns() []string {
	return []string{"bk_tenant_id", "event_source_id", "alert_id", "fingerprint", "strategy_id", "subject_id"}
}

func (*indexSQLRows) Close() error { return nil }

func (r *indexSQLRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, []driver.Value{"tenant", "source", "alert", "fp", []byte(`123`), []byte(`"object"`)})
	return nil
}

func TestActiveIndexReadsRawFieldsInScopedBatch(t *testing.T) {
	f := &indexSQLFixture{}
	db := sql.OpenDB(indexSQLConnector{fixture: f})
	t.Cleanup(func() { _ = db.Close() })
	repository, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	q := store.ActiveIndexQuery{Sources: []string{"source"}, BKTenantID: "tenant", Fingerprints: []string{"fp", "second"}, Fields: []string{"labels.strategy_id", "subject_id"}, PageSize: 2, MaxBytes: 4096}
	var rows []store.ActiveIndexRow
	if err := repository.ScanActiveIndex(t.Context(), q, func(page []store.ActiveIndexRow) error { rows = append(rows, page...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SubjectID != "object" {
		t.Fatal(rows)
	}
	if value, _ := rows[0].Labels["strategy_id"].NumberValue(); value != 123 {
		t.Fatal(rows)
	}
	if !strings.Contains(f.query, "JSON_EXTRACT(payload,?)") || !strings.Contains(f.query, "event_source_id IN (?) AND bk_tenant_id=? AND fingerprint IN (?,?)") || strings.Contains(f.query, "enrich") {
		t.Fatal(f.query)
	}
	args := []any{}
	for _, arg := range f.args {
		args = append(args, arg.Value)
	}
	if !reflect.DeepEqual(args, []any{`$.labels."strategy_id"`, "$.subject_id", "source", "tenant", "fp", "second"}) {
		t.Fatal(args)
	}
	f.err = errors.New("query unavailable")
	if err := repository.ScanActiveIndex(t.Context(), q, func([]store.ActiveIndexRow) error { t.Fatal("failed query treated as empty result"); return nil }); !errors.Is(err, f.err) {
		t.Fatal(err)
	}
}
