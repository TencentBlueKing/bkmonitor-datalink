// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"linkd/internal/domain"
	"linkd/internal/store"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/store/storetest"
)

func batchTestRepository(t *testing.T) (*mysqlstore.Repository, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv(integrationDSNEnv)
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN for isolated SQL integration")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "linkd_batch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	cfg.InterpolateParams = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	t.Cleanup(func() { _ = db.Close() })
	r, err := mysqlstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r, db, name
}

func statementCount(t *testing.T, db *sql.DB, name, prefix string) int {
	t.Helper()
	var count int
	err := db.QueryRowContext(t.Context(), `SELECT COALESCE(SUM(COUNT_STAR),0) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? AND DIGEST_TEXT LIKE ?`, name, prefix+"%").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSnapshotWritesSQLIntegration(t *testing.T) {
	r, db, name := batchTestRepository(t)
	created, err := r.CreateAlert(t.Context(), storetest.Alert("tenant", "one", "opening", "fp", "warning"))
	if err != nil {
		t.Fatal(err)
	}
	next := created.Alert.Clone()
	next.LatestEventID = "updated"
	next.LastOccurredAt = next.LastOccurredAt.Add(time.Second)
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	reads, txs := statementCount(t, db, name, "SELECT `payload`"), statementCount(t, db, name, "START TRANSACTION")
	saved, err := r.CompareAndSetAlertFromSnapshot(t.Context(), created.StoredAlert, next)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Alert.Revision != 2 || created.Alert.LatestEventID == next.LatestEventID {
		t.Fatal("revision or immutable snapshot")
	}
	if statementCount(t, db, name, "SELECT `payload`") != reads || statementCount(t, db, name, "START TRANSACTION") != txs {
		t.Fatal("snapshot write performed read or independent transaction")
	}
	if _, err := r.CompareAndSetAlertFromSnapshot(t.Context(), created.StoredAlert, next); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("stale owner", err)
	}
	event := storetest.Event("tenant", "event", "event-fp", "warning")
	event.SourceRawData = domain.JSONObject{"original": []byte(`{"keep":null,"x":1}`)}
	stored, err := r.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := r.CompareAndSetEventEnrichmentFromSnapshot(t.Context(), stored.StoredEvent, storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	finished, err := r.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), enriched, store.EventResult{State: domain.EventProcessStateOrphaned, Outcome: "orphaned", ProcessedAt: event.CreateAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := r.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := finished.Event.Normalize()
	actual, _ := reloaded.Event.Normalize()
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("partial event write lost source or enriched fields")
	}
	if _, err := r.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), enriched, store.EventResult{State: domain.EventProcessStateOrphaned, Outcome: "orphaned", ProcessedAt: event.CreateAt.Add(time.Minute)}); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("late result", err)
	}
}

func TestExplicit128MemberBatchUsesOneSQLUpdateAndKeepsConflictsIndependent(t *testing.T) {
	r, db, name := batchTestRepository(t)
	updates := make([]store.AlertSnapshotUpdate, 128)
	for i := range updates {
		created, err := r.CreateAlert(t.Context(), storetest.Alert("tenant", fmt.Sprintf("explicit-%03d", i), "opening", fmt.Sprintf("explicit-fp-%03d", i), "warning"))
		if err != nil {
			t.Fatal(err)
		}
		next := created.Alert.Clone()
		next.LatestEventID = "batch"
		next.LastOccurredAt = next.LastOccurredAt.Add(time.Second)
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		updates[i] = store.AlertSnapshotUpdate{Current: created.StoredAlert, Replacement: next}
	}
	if _, err := r.CompareAndSetAlertFromSnapshot(t.Context(), updates[3].Current, updates[3].Replacement); err != nil {
		t.Fatal(err)
	}
	before := statementCount(t, db, name, "UPDATE")
	results, err := r.CompareAndSetAlertsFromSnapshots(t.Context(), updates)
	if err != nil || len(results) != 128 {
		t.Fatal("explicit bulk", err)
	}
	if n := statementCount(t, db, name, "UPDATE") - before; n != 1 {
		t.Fatal("explicit 128 split into per-member UPDATE", n)
	}
	completed := []store.StoredAlert{}
	for i, item := range results {
		if i == 3 {
			if !errors.Is(item.Err, store.ErrVersionConflict) {
				t.Fatal("stale item", item.Err)
			}
			continue
		}
		if item.Err != nil || item.Alert.LatestEventID != "batch" {
			t.Fatal("independent member failed", i, item.Err)
		}
		completed = append(completed, item.StoredAlert)
	}
	before = statementCount(t, db, name, "UPDATE")
	confirmed, err := r.CompleteAlertChanges(t.Context(), completed, store.MergeChangeField)
	if err != nil || len(confirmed) != 127 {
		t.Fatal("explicit completion", err)
	}
	for _, item := range confirmed {
		if item.Err != nil {
			t.Fatal(item.Err)
		}
	}
	if n := statementCount(t, db, name, "UPDATE") - before; n != 1 {
		t.Fatal("confirmation split into per-member UPDATE", n)
	}
}

func TestWriteBatchMixedCASAndLogsSQLIntegration(t *testing.T) {
	r, db, name := batchTestRepository(t)
	batched, closeBatch, err := r.EnableWriteBatch(16, 4<<20, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBatch()
	const count = 16
	current := make([]store.StoredAlert, count)
	for i := range count {
		created, err := r.CreateAlert(t.Context(), storetest.Alert("tenant", fmt.Sprint("a", i), "opening", fmt.Sprint("fp", i), "warning"))
		if err != nil {
			t.Fatal(err)
		}
		current[i] = created.StoredAlert
	}
	stale := current[0]
	next := stale.Alert.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.LatestEventID = "winning-writer"
	next.LastOccurredAt = next.LastOccurredAt.Add(time.Second)
	if _, err := r.CompareAndSetAlertFromSnapshot(t.Context(), stale, next); err != nil {
		t.Fatal(err)
	}
	updates := statementCount(t, db, name, "UPDATE")
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]store.StoredAlert, count)
	errs := make([]error, count)
	for i := range count {
		wg.Go(func() {
			<-start
			next := current[i].Alert.Clone()
			next.LatestEventID = "batch"
			next.LastOccurredAt = next.LastOccurredAt.Add(time.Second)
			next.UpdateAt = next.UpdateAt.Add(time.Second)
			results[i], errs[i] = batched.CompareAndSetAlertFromSnapshot(t.Context(), current[i], next)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if i == 0 {
			if !errors.Is(err, store.ErrVersionConflict) {
				t.Fatal("stale item", err)
			}
		} else if err != nil {
			t.Fatalf("healthy item %d: %v", i, err)
		}
	}
	physical := statementCount(t, db, name, "UPDATE") - updates
	if physical < 1 || physical >= count-1 {
		t.Fatalf("CAS not physically batched: %d updates", physical)
	}
	t.Logf("16 CAS calls (1 conflict) used %d physical UPDATE statements", physical)
	actual, err := r.GetAlert(t.Context(), "tenant", stale.Alert.AlertID)
	if err != nil || actual.Alert.LatestEventID != "winning-writer" {
		t.Fatal("late owner overwrote winner", err)
	}
	logs := make([]domain.AlertLog, count)
	for i := range count {
		logs[i] = domain.AlertLog{LogID: fmt.Sprint("log", i), BKTenantID: "tenant", AlertID: current[i].Alert.AlertID, OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindTrigger, CreatedTime: current[i].Alert.CreateAt, Params: domain.JSONObject{}}
	}
	inserts := statementCount(t, db, name, "INSERT INTO `linkd_alert_logs`")
	items, err := batched.AppendAlertLogs(t.Context(), logs)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Err != nil || !item.Result.Created {
			t.Fatal("batch log", item.Err)
		}
	}
	if n := statementCount(t, db, name, "INSERT INTO `linkd_alert_logs`") - inserts; n != 1 {
		t.Fatalf("logs required %d INSERTs", n)
	}
	bad := logs[0]
	bad.Params = domain.JSONObject{"changed": []byte(`true`)}
	fresh := logs[1]
	fresh.LogID = "new-log"
	items, err = batched.AppendAlertLogs(t.Context(), []domain.AlertLog{logs[0], bad, fresh})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Err != nil || items[0].Result.Created || !errors.Is(items[1].Err, store.ErrIdentityConflict) || items[2].Err != nil || !items[2].Result.Created {
		t.Fatalf("mixed log results: %+v", items)
	}
	closeBatch()
	if _, err := batched.CompleteAlertChange(t.Context(), current[1], store.MergeChangeField); !errors.Is(err, context.Canceled) {
		t.Fatal("write after close", err)
	}
}

func TestCompleteAlertChangeSQLIntegration(t *testing.T) {
	r, _, _ := batchTestRepository(t)
	batched, closeBatch, err := r.EnableWriteBatch(8, 4<<20, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBatch()
	for _, field := range []store.AlertChangeField{store.MergeChangeField, store.PolicyChangeField} {
		t.Run(string(field), func(t *testing.T) {
			a := storetest.Alert("tenant", string(field), "opening", string(field), "warning")
			relation, window := strings.Repeat("e", 64), strings.Repeat("f", 64)
			if field == store.MergeChangeField {
				a.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{relation}}
			} else {
				at := a.CreateAt.Add(time.Hour)
				a.Shield = domain.AlertShield{Active: true, NextCheckAt: &at, Bindings: []domain.ShieldBinding{storetest.ShieldBinding(a.CreateAt)}}
			}
			created, err := r.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			next := created.Alert.Clone()
			next.UpdateAt = next.UpdateAt.Add(time.Second)
			if field == store.MergeChangeField {
				next.Merge, _, err = next.Merge.WithoutRelation(window, relation)
				if err != nil {
					t.Fatal(err)
				}
				next.MergeChange = &domain.AlertMergeChange{Kind: "member_unlink", OperationID: "operation", RelationID: relation, WindowID: window, EffectiveAt: next.UpdateAt, Before: a.Merge.Clone(), After: next.Merge.Clone()}
			} else {
				next.Shield = domain.AlertShield{}
				next.PolicyChange = &domain.AlertPolicyChange{OperationID: "operation", EffectiveAt: next.UpdateAt, Before: a.Shield.Clone().Bindings}
			}
			pending, err := batched.CompareAndSetAlertFromSnapshot(t.Context(), created.StoredAlert, next)
			if err != nil {
				t.Fatal(err)
			}
			completed, err := batched.CompleteAlertChange(t.Context(), pending, field)
			if err != nil {
				t.Fatal(err)
			}
			want := pending.Alert.Clone()
			want.MergeChange = nil
			want.PolicyChange = nil
			if !reflect.DeepEqual(completed.Alert, want) || completed.Version == pending.Version {
				t.Fatal("completion changed business fields")
			}
			actual, err := r.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil || !reflect.DeepEqual(actual.Alert, want) {
				t.Fatal("completion roundtrip", err)
			}
			if _, err := batched.CompleteAlertChange(t.Context(), pending, field); !errors.Is(err, store.ErrVersionConflict) {
				t.Fatal("late completion", err)
			}
		})
	}
}
