// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

// 单测只记录 SQL 边界与故障；真实 SQL 的行更新、约束及 JSON 语义由隔离集成用例验证。
type writeSQLFixture struct {
	mu                 sync.Mutex
	statements         []string
	rows               [][]driver.Value
	execErr, commitErr error
	block              bool
	started            chan struct{}
	once               sync.Once
}

type writeSQLConnector struct{ f *writeSQLFixture }

type writeSQLConn struct{ f *writeSQLFixture }

type writeSQLTx struct{ f *writeSQLFixture }

type writeSQLRows struct {
	rows  [][]driver.Value
	index int
}

func (c writeSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &writeSQLConn{c.f}, nil
}

func (c writeSQLConnector) Driver() driver.Driver { return c }

func (c writeSQLConnector) Open(string) (driver.Conn, error) { return &writeSQLConn{c.f}, nil }

func (c *writeSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (c *writeSQLConn) Close() error { return nil }

func (c *writeSQLConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *writeSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.f.record("BEGIN")
	return writeSQLTx{c.f}, nil
}

func (tx writeSQLTx) Commit() error { tx.f.record("COMMIT"); return tx.f.commitErr }

func (tx writeSQLTx) Rollback() error { tx.f.record("ROLLBACK"); return nil }

func (f *writeSQLFixture) record(q string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statements = append(f.statements, q)
}

func (c *writeSQLConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.f.record(q)
	if c.f.block {
		c.f.once.Do(func() { close(c.f.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if c.f.execErr != nil {
		return nil, c.f.execErr
	}
	return driver.RowsAffected(1), nil
}

func (c *writeSQLConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	c.f.record(q)
	return &writeSQLRows{rows: c.f.rows}, nil
}

func (r *writeSQLRows) Columns() []string { return []string{"bk_tenant_id", "alert_id", "version"} }

func (r *writeSQLRows) Close() error { return nil }

func (r *writeSQLRows) Next(values []driver.Value) error {
	if r.index == len(r.rows) {
		return io.EOF
	}
	copy(values, r.rows[r.index])
	r.index++
	return nil
}

func testWriteRepository(t *testing.T, f *writeSQLFixture) *Repository {
	t.Helper()
	db := sql.OpenDB(writeSQLConnector{f})
	t.Cleanup(func() { _ = db.Close() })
	r, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSnapshotSQLWritesHaveNoReadOrExplicitTransaction(t *testing.T) {
	f := &writeSQLFixture{}
	r := testWriteRepository(t, f)
	a := storetest.Alert("tenant", "a", "opening", "fp", "warning")
	current := store.StoredAlert{Alert: a, Version: versionToken(1)}
	next := a.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	if _, err := r.CompareAndSetAlertFromSnapshot(t.Context(), current, next); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteAlertChange(t.Context(), current, store.MergeChangeField); err != nil {
		t.Fatal(err)
	}
	if len(f.statements) != 2 {
		t.Fatalf("unexpected SQL: %v", f.statements)
	}
	for _, q := range f.statements {
		if !strings.HasPrefix(q, "UPDATE") || !strings.Contains(q, "AND version=?") {
			t.Fatalf("lost single-row CAS: %s", q)
		}
	}
	if !strings.Contains(f.statements[1], "JSON_REMOVE") {
		t.Fatal("completion replaced full document")
	}
}

func TestBatchSQLPartialConflictAndFailure(t *testing.T) {
	for _, stage := range []string{"success", "update failure", "commit response lost", "canceled member"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage)
			f := &writeSQLFixture{rows: [][]driver.Value{{"tenant", "a", int64(1)}, {"tenant", "b", int64(2)}}}
			if stage == "update failure" {
				f.execErr = failure
			}
			if stage == "commit response lost" {
				f.commitErr = failure
			}
			r := testWriteRepository(t, f)
			calls := []*writeCall{}
			for _, id := range []string{"a", "b"} {
				a := storetest.Alert("tenant", id, "opening", id, "warning")
				next := a.Clone()
				next.UpdateAt = next.UpdateAt.Add(time.Second)
				payload, _ := encodeAlert(next)
				calls = append(calls, &writeCall{ctx: t.Context(), alert: &alertWrite{current: store.StoredAlert{Alert: a, Version: versionToken(1)}, next: next, payload: payload}, done: make(chan writeResult, 1)})
			}
			if stage == "canceled member" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				calls[0].ctx = ctx
			}
			b := &writeBatch{repository: r}
			b.writeAlerts(t.Context(), calls)
			first, second := <-calls[0].done, <-calls[1].done
			if !errors.Is(second.err, store.ErrVersionConflict) {
				t.Fatal("conflict item was accepted", second.err)
			}
			switch stage {
			case "success":
				if first.err != nil || first.alert.Version != versionToken(2) {
					t.Fatal(first.err)
				}
			case "canceled member":
				if !errors.Is(first.err, context.Canceled) {
					t.Fatal(first.err)
				}
			default:
				if !errors.Is(first.err, failure) {
					t.Fatal("failure became success", first.err)
				}
			}
		})
	}
}

func TestWriteBatchCloseCancelsSQLAndWaitsForExit(t *testing.T) {
	f := &writeSQLFixture{block: true, started: make(chan struct{})}
	r := testWriteRepository(t, f)
	batched, closeBatch, err := r.EnableWriteBatch(1, 4096, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	a := storetest.Alert("tenant", "a", "opening", "fp", "warning")
	go func() {
		_, err := batched.CompleteAlertChange(t.Context(), store.StoredAlert{Alert: a, Version: versionToken(1)}, store.MergeChangeField)
		done <- err
	}()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("write never started")
	}
	closeBatch()
	closeBatch()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close leaked write")
	}
}

func TestExplicitLogBatchBypassesAutomaticCollector(t *testing.T) {
	f := &writeSQLFixture{}
	r := testWriteRepository(t, f)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	// 自动收集器已不可用；完整的多行调用仍须独立提交自身 INSERT。
	r.batch = &writeBatch{ctx: canceled, queue: make(chan *writeCall)}
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	logs := []domain.AlertLog{
		{LogID: "one", BKTenantID: "tenant", AlertID: "a", OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindMerge, CreatedTime: at},
		{LogID: "two", BKTenantID: "tenant", AlertID: "b", OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindMerge, CreatedTime: at},
	}
	results, err := r.AppendAlertLogs(t.Context(), logs)
	if err != nil || len(results) != 2 {
		t.Fatal("explicit batch joined collector", err)
	}
	for _, item := range results {
		if item.Err != nil || !item.Result.Created {
			t.Fatal(item.Err)
		}
	}
	if len(f.statements) != 1 || !strings.HasPrefix(f.statements[0], "INSERT INTO linkd_alert_logs") {
		t.Fatal("not one multi-row INSERT", f.statements)
	}
	if _, err := r.AppendAlertLogs(t.Context(), logs[:1]); !errors.Is(err, context.Canceled) {
		t.Fatal("single call bypassed its configured collector", err)
	}
}
