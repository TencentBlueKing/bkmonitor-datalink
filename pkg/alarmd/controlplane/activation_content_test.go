// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A legacy activation whose publication has no manifest is upgraded from a
// scan of the Schedule timelines: the scan honours cancellation and its
// deadline without touching Activation or Schedule, fails closed when the
// key budget overflows, and reports each attempt. With a manifest the scan
// is never entered (the sibling test asserts that), so this is the one
// place the scan's guarantees are exercised.
func TestScheduleActivationReconcilerUpgradesLegacyByScanWhenNoManifestExists(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:legacy-scan-without-manifest"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var migrationObservations []observability.Observation
	repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.LegacyMigration != nil {
			migrationObservations = append(migrationObservations, observation)
		}
	}))
	if err := repository.ConfigureLegacyMigration(50000, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	oldState, err := initial.Ensure(ctx, oldSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	legacy := oldState
	legacy.SchemaVersion = "alarmd-control-activation-v1"
	legacy.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	legacyPayload, _ := json.Marshal(legacy)
	if err := client.Set(ctx, prefix+":activation", legacyPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	// A publication from before the object catalog has neither a manifest
	// nor, once it aged, a body; the timelines are all that name its
	// population.
	revision := string(oldSnapshot.Publication.SnapshotRevision)
	if err := client.Del(ctx, prefix+":manifest:"+revision, prefix+":snapshot:"+revision).Err(); err != nil {
		t.Fatal(err)
	}
	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(ctx, emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	activationBefore, _ := client.Get(ctx, prefix+":activation").Bytes()
	scheduleKey := prefix + ":schedule_timeline:" + string(oldCatalog.QueryGroups[0].Identity)
	scheduleBefore, _ := client.Get(ctx, scheduleKey).Bytes()
	unchanged := func(step string) {
		t.Helper()
		activation, _ := client.Get(ctx, prefix+":activation").Bytes()
		schedule, _ := client.Get(ctx, scheduleKey).Bytes()
		if !bytes.Equal(activationBefore, activation) || !bytes.Equal(scheduleBefore, schedule) {
			t.Fatalf("%s changed Activation or Schedule", step)
		}
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	canceledReconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := canceledReconciler.Ensure(canceledCtx, emptySnapshot.Publication); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent context cancellation error=%v", err)
	}
	unchanged("parent context cancellation")
	if err := repository.ConfigureLegacyMigration(50000, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	timeoutReconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := timeoutReconciler.Ensure(ctx, emptySnapshot.Publication); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration timeout error=%v", err)
	}
	unchanged("migration timeout")
	if err := client.Set(ctx, prefix+":schedule_timeline:extra", `{}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureLegacyMigration(1, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	reconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if _, err := reconciler.Ensure(ctx, emptySnapshot.Publication); err == nil {
		t.Fatal("max_scan_keys overflow must fail closed")
	}
	unchanged("scan overflow")
	_ = client.Del(ctx, prefix+":schedule_timeline:extra").Err()
	_ = repository.ConfigureLegacyMigration(50000, 30*time.Second)
	state, err := reconciler.Ensure(ctx, emptySnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != emptySnapshot.Publication || state.SchemaVersion != "alarmd-control-activation-v2" || len(state.Plans) != 0 {
		t.Fatalf("migration state=%#v", state)
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil || len(groups) != 0 {
		t.Fatalf("active set=(%#v,%v)", groups, err)
	}
	if len(migrationObservations) != 3 || migrationObservations[0].LegacyMigration.Result != "canceled" ||
		migrationObservations[1].LegacyMigration.Result != "fail_closed" || migrationObservations[2].LegacyMigration.Result != "success" ||
		migrationObservations[2].LegacyMigration.ScanKeys == 0 {
		t.Fatalf("legacy migration observations=%#v", migrationObservations)
	}
}

// A Query Group whose retirement drained and that a publication brings
// back is compiled again even when its content is byte for byte what it
// was: its records name the publication that reopened it and restart
// through WARMING. A Query Group that stayed keeps the records of the
// publication that opened its Segment. Carrying is decided by the previous
// activation's content, which a retired Query Group is not part of, so a
// reactivating Query Group can never carry a record, not even one a Plan
// it shares with an active Query Group would offer.
func TestScheduleActivationReconcilerCompilesAReturningQueryGroupAndCarriesTheRest(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:returning-compiled", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	both := twoQueryGroupCatalog(t)
	if len(both.QueryGroups) != 2 {
		t.Fatalf("catalog has %d Query Groups, want two", len(both.QueryGroups))
	}
	returning, staying := both.QueryGroups[0], both.QueryGroups[1]
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		returning.Identity: {Status: execution.ProgressMissing}, staying.Identity: {Status: execution.ProgressMissing},
	}}
	clock := []time.Time{time.Unix(90, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	opened, err := initial.Ensure(ctx, first.Publication)
	if err != nil {
		t.Fatal(err)
	}
	recordsOf := func(state controlplane.ActivationState, group controlplane.QueryGroup) []controlplane.PlanActivationRecord {
		var records []controlplane.PlanActivationRecord
		for _, plan := range group.Plans {
			for _, record := range state.Plans {
				if record.Fact.Plan == plan.Identity {
					records = append(records, record)
				}
			}
		}
		if len(records) != len(group.Plans) {
			t.Fatalf("Query Group %s has %d records in %+v, want %d", group.Identity, len(records), state.Plans, len(group.Plans))
		}
		return records
	}
	openedReturning := recordsOf(opened, returning)

	onlyStaying := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{staying}}
	onlyStaying.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", onlyStaying.QueryGroups))
	second, _, err := repository.PublishCatalog(ctx, onlyStaying)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reconciler.Ensure(ctx, second.Publication)
	if err != nil || len(retired.Draining) != 1 || retired.Draining[0].QueryGroup != returning.Identity {
		t.Fatalf("retirement = (%+v, %v), want %s draining", retired.Draining, err, returning.Identity)
	}

	// The retirement has drained: the cursor stands on the retired boundary.
	progress.byGroup[returning.Identity] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: returning.Identity}, NextSlot: 90, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	third, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	if third.Publication.SnapshotRevision != first.Publication.SnapshotRevision || third.Publication == first.Publication {
		t.Fatalf("republishing the same content = %+v, want the first revision under a new epoch (first %+v)", third.Publication, first.Publication)
	}
	reopened, err := reconciler.Ensure(ctx, third.Publication)
	if err != nil || len(reopened.Draining) != 0 || reopened.Current != third.Publication {
		t.Fatalf("reopening = (%+v, %v), want %s reactivated under %+v", reopened, err, returning.Identity, third.Publication)
	}
	for index, record := range recordsOf(reopened, returning) {
		before := openedReturning[index]
		if record.Publication != third.Publication || !record.Fact.Selected.ForceWarming ||
			record.Fact.Selected.StateGeneration != before.Fact.Selected.StateGeneration ||
			record.Fact.Selected.ScheduleRevision != before.Fact.Selected.ScheduleRevision {
			t.Fatalf("returning record = %+v, want it compiled under %+v with the same generation as %+v and restarted through WARMING",
				record, third.Publication, before)
		}
	}
	for _, record := range recordsOf(reopened, staying) {
		if record.Publication != first.Publication || record.Fact.Selected.ForceWarming {
			t.Fatalf("staying record = %+v, want it carried from %+v without a restart", record, first.Publication)
		}
	}
}
