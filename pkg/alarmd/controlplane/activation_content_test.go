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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The v1 body a development build wrote before the first release is refused
// by name, not upgraded: every reader says which schema it found, and the
// Control Leader's round writes nothing over it.
func TestAV1ActivationBodyIsRefusedByName(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:v1-body-refused"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
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
	const v1 = "alarmd-control-activation-v1"
	legacy := oldState
	legacy.SchemaVersion = v1
	legacy.ActiveQGSetRef = controlplane.ActiveQueryGroupSetRef{}
	legacyPayload, _ := json.Marshal(legacy)
	if err := client.Set(ctx, prefix+":activation", legacyPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	newSnapshot, _, err := repository.PublishCatalog(ctx, catalogWithSchedule(t, validCatalog(t, 81), 60, 0))
	if err != nil {
		t.Fatal(err)
	}
	scheduleKey := prefix + ":schedule_timeline:" + string(oldCatalog.QueryGroups[0].Identity)
	scheduleBefore, _ := client.Get(ctx, scheduleKey).Bytes()

	reader, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var corrupt *controlplane.PersistedActivationCorruptError
	if _, err := reader.LoadActivationHead(ctx); !errors.As(err, &corrupt) || !strings.Contains(err.Error(), v1) {
		t.Fatalf("a reader of a v1 body = %v, want it refused as corrupt naming %s", err, v1)
	}
	reconciler, _ := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	_, err = reconciler.Ensure(ctx, newSnapshot.Publication)
	if err == nil || !strings.Contains(err.Error(), v1) {
		t.Fatalf("the Control Leader's round over a v1 body = %v, want it refused naming %s", err, v1)
	}
	if failure, ok := controlplane.ActivationFailureFromError(err); !ok || failure.Class != controlplane.ActivationFailureClassCorrupt {
		t.Fatalf("the refusal is classed %+v (%t), want corrupt: a body no writer of this build can fix is not a retry", failure, ok)
	}
	activation, _ := client.Get(ctx, prefix+":activation").Bytes()
	schedule, _ := client.Get(ctx, scheduleKey).Bytes()
	if !bytes.Equal(activation, legacyPayload) || !bytes.Equal(schedule, scheduleBefore) {
		t.Fatal("the refused round wrote the activation or the timeline")
	}
}

// A head body carries no records, so the active set reference is what names
// its population: a head without one is refused as corrupt, not read as an
// activation of nothing.
func TestAHeadWithoutItsActiveSetReferenceIsRefused(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:head-without-set"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := repository.PublishCatalog(ctx, catalogWithSchedule(t, validCatalog(t, 80), 60, 0))
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	initial, _ := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(83, 0) })
	state, err := initial.Ensure(ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	head := state
	head.SchemaVersion, head.Plans, head.ActiveQGSetRef = "alarmd-control-activation-v3", nil, controlplane.ActiveQueryGroupSetRef{}
	payload, _ := json.Marshal(head)
	if err := client.Set(ctx, prefix+":activation", payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	reader, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var corrupt *controlplane.PersistedActivationCorruptError
	if _, err := reader.LoadActivationHead(ctx); !errors.As(err, &corrupt) || !strings.Contains(err.Error(), "active Query Group set reference") {
		t.Fatalf("a head without its set reference read as %v, want it refused as corrupt naming the reference", err)
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

// One Query Group of one revision is read through the manifest of that
// revision: its object and the output context of each of its Plans, field
// for field the Query Group that was published, PlanRevision aside. The
// revision's manifest gone reads as an unavailable snapshot; a Query Group
// the manifest does not name, or whose object is gone, as an unavailable
// object.
func TestLoadQueryGroupReadsTheManifestAndObjects(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	ctx := harness.ctx
	catalog := twoQueryGroupCatalog(t)
	published := harness.publish(t, catalog)
	revision := published.Publication.SnapshotRevision
	repository := harness.newRepository(t)
	for _, group := range catalog.QueryGroups {
		read, err := repository.LoadQueryGroup(ctx, revision, group.Identity)
		if err != nil {
			t.Fatalf("LoadQueryGroup(%s) error = %v", group.Identity, err)
		}
		if want := withoutPlanRevisions([]controlplane.QueryGroup{group})[0]; !reflect.DeepEqual(read, want) {
			t.Fatalf("LoadQueryGroup(%s) = %+v, want %+v", group.Identity, read, want)
		}
	}
	if _, err := repository.LoadQueryGroup(ctx, revision, "not-a-query-group"); !errors.Is(err, controlplane.ErrCatalogObjectUnavailable) {
		t.Fatalf("LoadQueryGroup(unnamed) error = %v, want unavailable object", err)
	}
	manifest, err := repository.LoadCatalogManifest(ctx, revision)
	if err != nil {
		t.Fatal(err)
	}
	objectKey := harness.prefix + ":qgobj:" + string(manifest.QueryGroups[0].ObjectDigest)
	if deleted := harness.client.Del(ctx, objectKey).Val(); deleted != 1 {
		t.Fatalf("object key %q was not present to delete", objectKey)
	}
	if _, err := repository.LoadQueryGroup(ctx, revision, manifest.QueryGroups[0].QueryGroup); !errors.Is(err, controlplane.ErrCatalogObjectUnavailable) {
		t.Fatalf("LoadQueryGroup(object gone) error = %v, want unavailable object", err)
	}
	if deleted := harness.client.Del(ctx, harness.prefix+":manifest:"+string(revision)).Val(); deleted != 1 {
		t.Fatal("manifest key was not present to delete")
	}
	if _, err := repository.LoadQueryGroup(ctx, revision, manifest.QueryGroups[1].QueryGroup); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("LoadQueryGroup(manifest gone) error = %v, want unavailable snapshot", err)
	}
}
