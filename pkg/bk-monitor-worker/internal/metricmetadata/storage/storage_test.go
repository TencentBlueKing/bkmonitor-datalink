// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

// Every test creates and drops its own database; the supplied DSN is never used
// as the target schema. A test administrator with CREATE DATABASE is required.
func testMySQL(t *testing.T) (*MySQLStore, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("METRICMETADATA_TEST_DSN")
	if dsn == "" {
		t.Skip("set METRICMETADATA_TEST_DSN for real MySQL integration tests")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Timeout = 3 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("metricmetadata_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8 COLLATE utf8_bin"); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Logf("close MySQL admin connection after setup failure: %v", closeErr)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	ddl, err := os.ReadFile("testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(ddl), ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	execSQL(t, db, `INSERT INTO metadata_timeseriesgroup (time_series_group_id,bk_data_id,bk_biz_id,bk_tenant_id,table_id,is_enable,is_delete,metric_group_dimensions)
VALUES (9,1002,2,'tenant-a','test.metrics',1,0,'[]'),(10,1003,3,'tenant-b','other.metrics',1,0,'[]')`)
	execSQL(t, db, `INSERT INTO metadata_timeseriesmetric VALUES (9,1,'cpu'),(10,2,'memory'),(10,3,'cpu')`)
	options := core.DefaultOptions()
	return NewMySQLStore(db, 5*time.Second, 0, 0, options.TouchInterval, options.LoadPageSize), db
}

func execSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func scalar(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func report(t *testing.T, scope, name, unit string) core.Record {
	t.Helper()
	b, err := json.Marshal([]any{map[string]any{"dataid": 1002, "__scope_type": "APM", "__scope": scope, "__metric": name, "__name": name, "__original_type": "gauge", "__type": "gauge", "__unit": unit}})
	if err != nil {
		t.Fatal(err)
	}
	r, d, err := core.DecodeMessage(b)
	if err != nil || len(r) != 1 || len(d) > 0 {
		t.Fatalf("invalid fixture %v %v", d, err)
	}
	r[0].GroupID = 9
	return r[0]
}

func TestLoadAllRestoresScopesAndRejectsCorruption(t *testing.T) {
	store, db := testMySQL(t)
	ctx := context.Background()
	first := report(t, "checkout-http", "cpu", "s")
	second := report(t, "billing-http", "memory", "1")
	for _, record := range []core.Record{first, second} {
		if _, err := store.Apply(ctx, 9, []core.Record{record}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	states, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("loaded %d scopes, want 2", len(states))
	}
	for _, state := range states {
		if state.Key.GroupID != 9 || len(state.Metrics) != 1 {
			t.Fatalf("invalid loaded scope: %+v", state)
		}
	}
	execSQL(t, db, `UPDATE metadata_timeseriesreportscope SET report_value_ids=? WHERE scope_hash=?`, []byte{1}, first.ScopeHash)
	if _, err := store.LoadAll(ctx); err == nil {
		t.Fatal("corrupt member list accepted by full refresh")
	}
}

func assertReferences(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT group_id,report_value_ids,value_count FROM metadata_timeseriesreportscope`)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[int64]int)
	groups := make(map[int64]int)
	for rows.Next() {
		var group, count int
		var b []byte
		if err := rows.Scan(&group, &b, &count); err != nil {
			t.Fatal(err)
		}
		ids, err := core.DecodeIDs(b, count)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			counts[int64(id)]++
			groups[int64(id)] = group
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT id,group_id,scope_count FROM metadata_timeseriesreportvalue`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	for rows.Next() {
		var id int64
		var group, count int
		if err := rows.Scan(&id, &group, &count); err != nil {
			t.Fatal(err)
		}
		if counts[id] != count {
			t.Fatalf("value %d references: db=%d actual=%d", id, count, counts[id])
		}
		if count > 0 && groups[id] != group {
			t.Fatal("cross-group reference")
		}
		delete(counts, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatal("dangling scope members", counts)
	}
}

func TestMySQLScopeChangesReplayAndRollback(t *testing.T) {
	s, db := testMySQL(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	a := report(t, "checkout-http", "cpu", "1")
	b := report(t, "cart-http", "cpu", "1")
	result, err := s.Apply(ctx, 9, []core.Record{a, b}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedValues != 1 || result.CountChanges != 2 {
		t.Fatalf("unexpected result %+v", result)
	}
	if n := scalar(t, db, `SELECT scope_count FROM metadata_timeseriesreportvalue`); n != 2 {
		t.Fatal(n)
	}
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = s.Apply(ctx, 9, []core.Record{a, b}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedScopes != 0 || result.CreatedValues != 0 || result.DuplicateRecords != 2 || scalar(t, db, `SELECT MAX(version) FROM metadata_timeseriesreportscope`) != 0 {
		t.Fatal("replay changed state")
	}
	changed := report(t, "checkout-http", "cpu", "ms")
	result, err = s.Apply(ctx, 9, []core.Record{changed}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedScopes != 1 || result.CountChanges != 0 {
		t.Fatalf("replacement changed count %+v", result)
	}
	if version := scalar(t, db, `SELECT version FROM metadata_timeseriesreportscope WHERE group_id=? AND scope_hash=?`, 9, a.ScopeHash); version != 1 {
		t.Fatalf("metadata replacement version = %d, want 1", version)
	}
	if count := scalar(t, db, `SELECT value_count FROM metadata_timeseriesreportscope WHERE group_id=? AND scope_hash=?`, 9, a.ScopeHash); count != 1 {
		t.Fatalf("metadata replacement value_count = %d, want 1", count)
	}
	added := report(t, "checkout-http", "memory", "bytes")
	if _, err = s.Apply(ctx, 9, []core.Record{changed, added}, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, db, `SELECT value_count FROM metadata_timeseriesreportscope WHERE scope_hash=?`, a.ScopeHash); n != 2 {
		t.Fatal("member count", n)
	}
	assertReferences(t, db)
	// An error after scope writes must roll back both the scope and new values.
	execSQL(t, db, `CREATE TRIGGER fail_references BEFORE UPDATE ON metadata_timeseriesreportvalue FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected reference failure'`)
	failed := report(t, "checkout-http", "disk", "bytes")
	if _, err = s.Apply(ctx, 9, []core.Record{failed}, now.Add(4*time.Hour)); err == nil {
		t.Fatal("injected failure was hidden")
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue WHERE field_name='disk'`) != 0 {
		t.Fatal("failed value insert committed")
	}
	if n := scalar(t, db, `SELECT value_count FROM metadata_timeseriesreportscope WHERE scope_hash=?`, a.ScopeHash); n != 2 {
		t.Fatal("failed scope update committed")
	}
	execSQL(t, db, `DROP TRIGGER fail_references`)
	if _, err = s.Apply(ctx, 9, []core.Record{failed}, now.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertReferences(t, db)
	// A -> B -> A in one batch must leave only the final value referenced.
	if _, err = s.Apply(ctx, 9, []core.Record{a, changed, a}, now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertReferences(t, db)
	if _, err = s.Apply(ctx, 9, []core.Record{a}, now.Add(30*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var seen time.Time
	if err = db.QueryRow(`SELECT last_seen FROM metadata_timeseriesreportscope WHERE scope_hash=?`, a.ScopeHash).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if !seen.Equal(now.Add(30 * time.Hour)) {
		t.Fatalf("last_seen=%s", seen)
	}
}

func TestMySQLApplyReloadsValueIDsAddedByAnotherStore(t *testing.T) {
	firstStore, db := testMySQL(t)
	options := core.DefaultOptions()
	secondStore := NewMySQLStore(db, 5*time.Second, 0, 0, options.TouchInterval, options.LoadPageSize)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	first := report(t, "checkout-http", "cpu", "s")
	changed := report(t, "checkout-http", "cpu", "ms")
	if _, err := firstStore.Apply(ctx, 9, []core.Record{first}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.Apply(ctx, 9, []core.Record{changed}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := firstStore.Apply(ctx, 9, []core.Record{first}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("first store did not resolve the new persisted value ID: %v", err)
	}
	assertReferences(t, db)
}

func TestMySQLLoadsMetricRoutes(t *testing.T) {
	s, db := testMySQL(t)
	ctx := context.Background()
	execSQL(t, db, `DELETE FROM metadata_timeseriesmetric`)
	routes, err := s.LoadMetricRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[1002].TenantID != "tenant-a" || routes[1003].GroupID != 10 {
		t.Fatal(routes)
	}
	route, found, err := s.GroupForDataID(ctx, 1003)
	if err != nil || !found || route.GroupID != 10 || route.TenantID != "tenant-b" {
		t.Fatalf("route=%+v found=%t err=%v", route, found, err)
	}
	route, found, err = s.GroupForDataID(ctx, 1002)
	if err != nil || !found || route.GroupID != 9 {
		t.Fatalf("group lookup by dataid failed: route=%+v found=%t err=%v", route, found, err)
	}
	if _, found, err = s.GroupForDataID(ctx, 9999); err != nil || found {
		t.Fatalf("unknown dataid was routed: found=%t err=%v", found, err)
	}
	execSQL(t, db, `UPDATE metadata_timeseriesgroup SET is_enable=0 WHERE bk_data_id=1003`)
	routes, err = s.LoadMetricRoutes(ctx)
	if err != nil || len(routes) != 1 {
		t.Fatalf("disabled group remained in metric routes: %v %v", routes, err)
	}
	if _, found, err = s.GroupForDataID(ctx, 1003); err != nil || found {
		t.Fatalf("disabled group was routed: found=%t err=%v", found, err)
	}
	if _, err := s.Apply(ctx, 10, []core.Record{report(t, "billing-http", "disk", "bytes")}, time.Now()); !errors.Is(err, ErrGroupUnavailable) {
		t.Fatalf("disabled group error=%v, want ErrGroupUnavailable", err)
	}
	r := report(t, "checkout-http", "cpu", "1")
	r.GroupID = 10
	if _, err = s.Apply(ctx, 9, []core.Record{r}, time.Now()); err == nil {
		t.Fatal("transaction accepted mismatched group")
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue`) != 0 {
		t.Fatal("mismatched data committed")
	}
}

func TestMySQLConcurrentWritersAndExpiration(t *testing.T) {
	s, db := testMySQL(t)
	now := time.Now().Truncate(time.Microsecond)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		r := report(t, fmt.Sprintf("service%d-http", i), "cpu", "1")
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Apply(ctx, 9, []core.Record{r}, now); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue`) != 1 || scalar(t, db, `SELECT scope_count FROM metadata_timeseriesreportvalue`) != 12 {
		t.Fatal("concurrent duplicate or reference loss")
	}
	assertReferences(t, db)
	n, err := s.Expire(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Fatal(n)
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportscope`) != 0 || scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue`) != 0 {
		t.Fatal("expiration left members")
	}
}

func TestMySQLCorruptionAndIDOverflow(t *testing.T) {
	s, db := testMySQL(t)
	r := report(t, "checkout-http", "cpu", "1")
	ctx := context.Background()
	if _, err := s.Apply(ctx, 9, []core.Record{r}, time.Now()); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `UPDATE metadata_timeseriesreportscope SET report_value_ids=?`, []byte{1})
	if _, err := s.Apply(ctx, 9, []core.Record{r}, time.Now()); err == nil {
		t.Fatal("corrupt members overwritten")
	}
	execSQL(t, db, `DELETE FROM metadata_timeseriesreportscope`)
	execSQL(t, db, `DELETE FROM metadata_timeseriesreportvalue`)
	execSQL(t, db, `ALTER TABLE metadata_timeseriesreportvalue AUTO_INCREMENT=4294967296`)
	if _, err := s.Apply(ctx, 9, []core.Record{r}, time.Now()); err == nil {
		t.Fatal("unencodable ID accepted")
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportscope`) != 0 || scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue`) != 0 {
		t.Fatal("ID overflow did not roll back")
	}
}

func TestMySQLCleansUnreferencedValuesWithoutExpiredScopes(t *testing.T) {
	s, db := testMySQL(t)
	now := time.Now()
	ctx := context.Background()
	for _, unit := range []string{"s", "ms"} {
		r := report(t, "checkout-http", "cpu", unit)
		if _, err := s.Apply(ctx, 9, []core.Record{r}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Expire(ctx, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportvalue WHERE scope_count=0`); n != 0 {
		t.Fatal("unused replacement values never cleaned")
	}
	assertReferences(t, db)
}

func TestMySQLExpirationRacingWithNewReports(t *testing.T) {
	s, db := testMySQL(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	var records []core.Record
	for i := 0; i < 8; i++ {
		records = append(records, report(t, fmt.Sprintf("race%d-http", i), "cpu", "1"))
	}
	if _, err := s.Apply(ctx, 9, records, now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() {
		_, err := s.Apply(ctx, 9, records, now)
		errs <- err
	}()
	go func() {
		_, err := s.Expire(ctx, now.Add(-30*24*time.Hour))
		errs <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, db, `SELECT COUNT(*) FROM metadata_timeseriesreportscope`) != 8 ||
		scalar(t, db, `SELECT scope_count FROM metadata_timeseriesreportvalue`) != 8 {
		t.Fatal("expiration lost a concurrent report")
	}
	assertReferences(t, db)
}
