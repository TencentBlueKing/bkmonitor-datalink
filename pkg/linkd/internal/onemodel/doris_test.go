// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type dorisQueryFunc func(context.Context, string, []driver.NamedValue) (driver.Rows, error)

type dorisTestConnector struct{ query dorisQueryFunc }

func (c dorisTestConnector) Connect(context.Context) (driver.Conn, error) {
	return dorisTestConn(c), nil
}

func (c dorisTestConnector) Driver() driver.Driver { return dorisTestDriver(c) }

type dorisTestDriver dorisTestConnector

func (d dorisTestDriver) Open(string) (driver.Conn, error) { return dorisTestConn(d), nil }

type dorisTestConn dorisTestConnector

func (c dorisTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (c dorisTestConn) Close() error { return nil }

func (c dorisTestConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }

func (c dorisTestConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, q, args)
}

type dorisTestRows struct {
	columns []string
	items   [][]driver.Value
	failure error
	closed  *atomic.Int32
}

func (r *dorisTestRows) Columns() []string { return r.columns }

func (r *dorisTestRows) Close() error {
	if r.closed != nil {
		r.closed.Add(1)
	}
	return nil
}

func (r *dorisTestRows) Next(dest []driver.Value) error {
	if len(r.items) == 0 {
		if r.failure != nil {
			return r.failure
		}
		return io.EOF
	}
	copy(dest, r.items[0])
	r.items = r.items[1:]
	return nil
}

func dorisInstanceRow(tenant, model, id string) []driver.Value {
	return []driver.Value{tenant, []byte(model), []byte(id), []byte(model + "|" + id), "cmdb", "host", "[2]", `{"cpu":0,"enabled":false,"text":"x"}`, `[]`, "r1", "2026-10-08 00:00:00.000000"}
}

func dorisTestClient(t *testing.T, query dorisQueryFunc) *Client {
	t.Helper()
	db := sql.OpenDB(dorisTestConnector{query})
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	c, err := NewDorisClient(DorisConfig{Reader: db, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDorisQueryUsesKACSchemaAndBoundTenantValues(t *testing.T) {
	var closed atomic.Int32
	literal := `x' OR 1=1 --`
	c := dorisTestClient(t, func(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(q, literal) || !strings.Contains(q, "FROM `kingeye_instance`") || !strings.Contains(q, "`bk_tenant_id` = ? AND `model_id` = ?") || !strings.Contains(q, "JSON_EXTRACT_BIGINT") || !strings.Contains(q, "JSON_EXTRACT_BOOL") {
			t.Fatal("unsafe or untyped query", q)
		}
		if args[0].Value != "tenant" || args[1].Value != "cw-Host" {
			t.Fatal("tenant scope lost")
		}
		var number, boolean, escaped bool
		for _, a := range args {
			if a.Value == int64(0) {
				number = true
			}
			if a.Value == false {
				boolean = true
			}
			if a.Value == literal {
				escaped = true
			}
		}
		if !number || !boolean || !escaped {
			t.Fatal("valid zero/false/string lost", args)
		}
		return &dorisTestRows{columns: instanceColumns, items: [][]driver.Value{dorisInstanceRow("tenant", "cw-Host", "101")}, closed: &closed}, nil
	})
	rows, err := c.Search(t.Context(), "tenant", Query{ModelID: "cw-Host", Limit: 2, Where: Filter{All: []Filter{{Field: "attributes.cpu", Type: InstanceAttributeLong, Operator: "eq", Value: 0}, {Field: "attributes.enabled", Type: InstanceAttributeBoolean, Operator: "eq", Value: false}, {Field: "attributes.text", Type: InstanceAttributeKeyword, Operator: "eq", Value: literal}}}})
	if err != nil || len(rows) != 1 || rows[0].Attributes["cpu"] != json.Number("0") || rows[0].Attributes["enabled"] != false || closed.Load() != 1 {
		t.Fatal("row normalization/cleanup", rows, err, closed.Load())
	}
}

func TestDorisRejectsIncompleteForeignAndOversizedRows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		row     []driver.Value
		failure error
		count   int
	}{
		{"foreign", dorisInstanceRow("other", "cw-Host", "1"), nil, 1},
		{"ambiguous", dorisInstanceRow("tenant", "cw-Host", "1"), nil, 2},
		{"partial", dorisInstanceRow("tenant", "cw-Host", "1"), errors.New("private payload"), 1},
		{"bad json", func() []driver.Value { r := dorisInstanceRow("tenant", "cw-Host", "1"); r[7] = `[]`; return r }(), nil, 1},
		{"large", func() []driver.Value {
			r := dorisInstanceRow("tenant", "cw-Host", "1")
			r[5] = strings.Repeat("a", maxOneModelResponseBytes+1)
			return r
		}(), nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dorisTestClient(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				items := [][]driver.Value{}
				for range tc.count {
					items = append(items, tc.row)
				}
				return &dorisTestRows{columns: instanceColumns, items: items, failure: tc.failure}, nil
			})
			_, found, err := c.FindInstance(t.Context(), "tenant", InstanceQuery{ModelCode: "cw-Host", InstanceID: "1"})
			if err == nil || found || strings.Contains(err.Error(), "private payload") {
				t.Fatal("invalid result treated as found/empty", found, err)
			}
		})
	}
}

func TestDorisPaginationBindsQueryTenantBackendAndExpiry(t *testing.T) {
	calls := 0
	c := dorisTestClient(t, func(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
		calls++
		items := [][]driver.Value{dorisInstanceRow("tenant", "cw-Host", "a"), dorisInstanceRow("tenant", "cw-Host", "b")}
		if calls == 2 {
			if !strings.Contains(q, "`model_inst_id` > ?") || args[len(args)-2].Value != "a" {
				t.Fatal("keyset missing", q, args)
			}
			items = items[1:]
		}
		return &dorisTestRows{columns: instanceColumns, items: items}, nil
	})
	p, _ := NewPager(c)
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	q := PageQuery{ModelID: "cw-Host", Limit: 1}
	first, err := p.Search(t.Context(), "tenant", q)
	if err != nil || len(first.Instances) != 1 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	q.Cursor = first.NextCursor
	if _, err = p.Search(t.Context(), "other", q); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("cross tenant cursor", err)
	}
	altered := q
	altered.ModelID = "cw-Module"
	if _, err = p.Search(t.Context(), "tenant", altered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("cross model cursor", err)
	}
	tampered := q
	tampered.Cursor += "x"
	if _, err = p.Search(t.Context(), "tenant", tampered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("tampered cursor", err)
	}
	second, err := p.Search(t.Context(), "tenant", q)
	if err != nil || len(second.Instances) != 1 || second.Instances[0].InstanceID != "b" || second.NextCursor != "" || calls != 2 {
		t.Fatal(second, err)
	}
	if err = p.Close(t.Context(), "tenant", q.Cursor); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err = p.Search(t.Context(), "tenant", q); !errors.Is(err, ErrCursorExpired) {
		t.Fatal("expired cursor accepted", err)
	}
}

func TestDorisCancellationDoesNotCancelOtherQueries(t *testing.T) {
	started := make(chan struct{})
	c := dorisTestClient(t, func(ctx context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
		if args[2].Value == "slow" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &dorisTestRows{columns: instanceColumns, items: [][]driver.Value{dorisInstanceRow("tenant", "cw-Host", "fast")}}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := c.FindInstance(ctx, "tenant", InstanceQuery{ModelCode: "cw-Host", InstanceID: "slow"})
		done <- err
	}()
	<-started
	if _, found, err := c.FindInstance(t.Context(), "tenant", InstanceQuery{ModelCode: "cw-Host", InstanceID: "fast"}); err != nil || !found {
		t.Fatal(found, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := c.FindHostTopology(t.Context(), "tenant", "fast"); !errors.Is(err, ErrDataSourceUnavailable) {
		t.Fatal("missing topology invented a result", err)
	}
}

func TestDorisFilterKACContract(t *testing.T) {
	for _, tc := range []struct {
		f    Filter
		sql  string
		args []any
	}{
		{Filter{Field: "attributes.enabled", Type: InstanceAttributeBoolean, Operator: "ne", Value: false}, "NOT COALESCE((JSON_EXTRACT_BOOL(`attributes`, ?) = ?), FALSE)", []any{`$."enabled"`, false}},
		{Filter{Field: "bk_biz_ids", Type: InstanceAttributeLong, Operator: "in", Value: []int{2, 3}}, "(ARRAY_CONTAINS(`bk_biz_ids`, ?) OR ARRAY_CONTAINS(`bk_biz_ids`, ?))", []any{int64(2), int64(3)}},
		{Filter{Field: "attributes.cpu", Type: InstanceAttributeLong, Operator: "exists"}, "JSON_EXTRACT_BIGINT(`attributes`, ?) IS NOT NULL", []any{`$."cpu"`}},
		{Filter{Field: "display_name", Type: InstanceAttributeKeyword, Operator: "contains", Value: "a%_"}, "`display_name` LIKE ? ESCAPE '\\\\'", []any{`%a\%\_%`}},
	} {
		x, err := dorisFilter(tc.f)
		if err != nil || x.text != tc.sql || !reflect.DeepEqual(x.args, tc.args) {
			t.Fatalf("KAC filter mismatch: %s %v (%v)", x.text, x.args, err)
		}
	}
	if _, err := dorisFilter(Filter{Field: "bk_tenant_id", Type: InstanceAttributeKeyword, Operator: "eq", Value: "other"}); err == nil {
		t.Fatal("tenant filter override accepted")
	}
	var calls atomic.Int32
	c := dorisTestClient(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		calls.Add(1)
		return nil, fmt.Errorf("private SQL")
	})
	if _, err := NewDorisClient(DorisConfig{Reader: c.doris.db, Timeout: time.Second, InstanceTable: "a; DROP TABLE b"}); err == nil || calls.Load() != 0 {
		t.Fatal("unsafe table accepted")
	}
}

func TestDorisTimeoutAndRelatedUseSameBackend(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		c := dorisTestClient(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		c.doris.timeout = 5 * time.Millisecond
		if _, _, err := c.FindInstance(t.Context(), "tenant", InstanceQuery{ModelCode: "cw-Host", InstanceID: "1"}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("unbounded query", err)
		}
	})
	t.Run("relation", func(t *testing.T) {
		calls := 0
		c := dorisTestClient(t, func(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
			calls++
			if args[0].Value != "tenant" {
				t.Fatal("relation tenant missing")
			}
			if strings.Contains(q, "kingeye_projection_edge") {
				return &dorisTestRows{columns: edgeColumns, items: [][]driver.Value{{"tenant", "cmdb_fact", "cw-App", "cw-App|app", "cw-Host", "cw-Host|host", "belongs", "belongs", "instance", `{}`, "r1", "2026-10-08 00:00:00"}}}, nil
			}
			return &dorisTestRows{columns: instanceColumns, items: [][]driver.Value{dorisInstanceRow("tenant", "cw-Host", "host")}}, nil
		})
		roots := []Instance{{TenantID: "tenant", ModelCode: "cw-App", InstanceID: "app"}}
		rows, err := c.Related(t.Context(), "tenant", roots, "belongs", "out", Query{ModelID: "cw-Host", Limit: 2})
		if err != nil || len(rows) != 1 || rows[0].InstanceID != "host" || calls != 2 {
			t.Fatal("relation did not use Doris exclusively", err)
		}
	})
}

func TestDorisFirstUsesLimitOneWithoutOverflowProbe(t *testing.T) {
	c := dorisTestClient(t, func(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "ORDER BY `model_inst_id` ASC") || !strings.HasSuffix(q, "LIMIT ?") || args[len(args)-1].Value != int64(1) {
			t.Fatal(q, args)
		}
		return &dorisTestRows{columns: instanceColumns, items: [][]driver.Value{dorisInstanceRow("t", "cw-Host", "101")}, closed: new(atomic.Int32)}, nil
	})
	rows, err := c.Search(t.Context(), "t", Query{ModelID: "cw-Host", Limit: 1, First: true})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
