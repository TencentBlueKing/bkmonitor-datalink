// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// These tests pin what step 4 of the object catalog design owes: a cutover
// is triggered by a Query Group's execution content changing, not by a new
// publication arriving. They are written against the shape step 3 left
// behind, the digest and output context references on the Segment, and
// every one of them fails on that code, where each publication closes and
// reopens every Segment. A Segment rewritten with the content it already
// carries is exactly the defect they exist to catch.

type cutoverFixture struct {
	ctx        context.Context
	client     *redis.Client
	prefix     string
	repository *controlplane.RedisCatalogRepository
	reconciler *controlplane.ScheduleActivationReconciler
	runtime    *controlplane.RedisCatalogRuntime
	now        *time.Time
}

func newCutoverFixture(t *testing.T, prefix string) *cutoverFixture {
	t.Helper()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	now := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &cutoverFixture{ctx: context.Background(), client: client, prefix: prefix,
		repository: repository, reconciler: reconciler, runtime: runtime, now: &now}
}

// publish publishes the catalog at the given second and activates it.
func (fixture *cutoverFixture) publish(t *testing.T, catalog controlplane.Catalog, at int64) controlplane.PublishedSnapshot {
	t.Helper()
	*fixture.now = time.Unix(at, 0)
	snapshot, _, err := fixture.repository.PublishCatalog(fixture.ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(fixture.ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (fixture *cutoverFixture) timelineBytes(t *testing.T, group execution.QueryGroupIdentity) []byte {
	t.Helper()
	raw, err := fixture.client.Get(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group)).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (fixture *cutoverFixture) openSegment(t *testing.T, group execution.QueryGroupIdentity, at execution.EvaluationTime) execution.ScheduleSegmentFact {
	t.Helper()
	schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, group, at)
	if err != nil {
		t.Fatal(err)
	}
	return schedule.Segment
}

func (fixture *cutoverFixture) activation(t *testing.T) controlplane.ActivationState {
	t.Helper()
	state, err := fixture.repository.LoadActivation(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func recordsOf(state controlplane.ActivationState, group controlplane.QueryGroup) []controlplane.PlanActivationRecord {
	wanted := make(map[execution.PlanIdentity]struct{}, len(group.Plans))
	for _, plan := range group.Plans {
		wanted[plan.Identity] = struct{}{}
	}
	records := make([]controlplane.PlanActivationRecord, 0, len(group.Plans))
	for _, record := range state.Plans {
		if _, ok := wanted[record.Fact.Plan]; ok {
			records = append(records, record)
		}
	}
	return records
}

// cutoverCatalog builds the two-Query-Group catalog with the first source
// document edited in place, so a test can change exactly one field of one
// strategy and know which digest that field belongs to.
func cutoverCatalog(t *testing.T, thresholdA int, editA func(document map[string]any)) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	documentA := []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":`+strconv.Itoa(thresholdA), 1))
	if editA != nil {
		var decodedA map[string]any
		if err := json.Unmarshal(documentA, &decodedA); err != nil {
			t.Fatal(err)
		}
		editA(decodedA)
		edited, err := json.Marshal(decodedA)
		if err != nil {
			t.Fatal(err)
		}
		documentA = edited
	}
	var decodedB map[string]any
	if err := json.Unmarshal(withWireIdentity(t, documents[1], "tenant-a", "bkcc__3"), &decodedB); err != nil {
		t.Fatal(err)
	}
	decodedB["bk_biz_id"] = float64(3)
	documentB, err := json.Marshal(decodedB)
	if err != nil {
		t.Fatal(err)
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documentA, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1002", Document: documentB, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
	}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 2 {
		t.Fatalf("expected two Query Groups, got %d with dispositions %+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// splitEdited tells which Query Group of the first catalog the edit reached,
// by its execution digest or, when that stayed, by a Plan's output context
// digest, and which one it left alone. Query Groups are ordered by identity,
// not by source, so a test cannot assume which index it edited.
func splitEdited(t *testing.T, first, second controlplane.Catalog) (edited, untouched controlplane.QueryGroup) {
	t.Helper()
	moved := 0
	for index := range first.QueryGroups {
		before, after := first.QueryGroups[index], second.QueryGroups[index]
		if before.Identity != after.Identity {
			t.Fatalf("the edit changed the Query Group set: %s vs %s", before.Identity, after.Identity)
		}
		beforeDigest, err := controlplane.DeriveQueryGroupObjectDigest(before)
		if err != nil {
			t.Fatal(err)
		}
		afterDigest, err := controlplane.DeriveQueryGroupObjectDigest(after)
		if err != nil {
			t.Fatal(err)
		}
		beforeContext, err := controlplane.DeriveOutputContextDigest(before.Plans[0])
		if err != nil {
			t.Fatal(err)
		}
		afterContext, err := controlplane.DeriveOutputContextDigest(after.Plans[0])
		if err != nil {
			t.Fatal(err)
		}
		if beforeDigest != afterDigest || beforeContext != afterContext {
			edited = before
			moved++
		} else {
			untouched = before
		}
	}
	if moved != 1 {
		t.Fatalf("the edit must reach exactly one Query Group, reached %d", moved)
	}
	return edited, untouched
}

func renameStrategy(document map[string]any) {
	document["name"] = "cpu usage renamed"
	document["update_time"] = float64(1725000999)
}

// TestExecutionEditCutsOnlyTheEditedQueryGroup: a threshold edit on one
// strategy cuts that Query Group's Segment and leaves the other Query Group's
// timeline byte for byte as it was. The activation advances to the new
// publication, but the untouched Query Group's Plan records are carried
// over verbatim: same publication, same state apply epoch, no warming.
func TestExecutionEditCutsOnlyTheEditedQueryGroup(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-execution-edit")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	groupA, groupB := splitEdited(t, first, second)
	firstSnapshot := fixture.publish(t, first, 60)
	segmentA := fixture.openSegment(t, groupA.Identity, 60)
	bytesB := fixture.timelineBytes(t, groupB.Identity)
	before := fixture.activation(t)
	recordsB := recordsOf(before, groupB)

	secondSnapshot := fixture.publish(t, second, 120)
	if secondSnapshot.Publication == firstSnapshot.Publication {
		t.Fatal("the threshold edit must publish a new revision")
	}

	cut := fixture.openSegment(t, groupA.Identity, 120)
	if cut.Start != 120 || cut.ObjectDigest == segmentA.ObjectDigest || cut.Publication.SnapshotRevision != secondSnapshot.Publication.SnapshotRevision {
		t.Fatalf("edited Query Group must run under a new Segment naming the new content: before=%+v after=%+v", segmentA, cut)
	}
	if got := fixture.timelineBytes(t, groupB.Identity); !reflect.DeepEqual(got, bytesB) {
		t.Fatalf("untouched Query Group's timeline was rewritten:\n before=%s\n after=%s", bytesB, got)
	}
	after := fixture.activation(t)
	if after.Current != secondSnapshot.Publication {
		t.Fatalf("activation current=%+v want %+v", after.Current, secondSnapshot.Publication)
	}
	if got := recordsOf(after, groupB); !reflect.DeepEqual(got, recordsB) {
		t.Fatalf("untouched Query Group's Plan records changed:\n before=%+v\n after=%+v", recordsB, got)
	}
	for _, record := range recordsOf(after, groupA) {
		if record.Publication != secondSnapshot.Publication ||
			record.Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(secondSnapshot.Publication.PublicationEpoch) {
			t.Fatalf("edited Query Group's Plan record must name the new publication: %+v", record)
		}
	}
}

// TestOutputContextEditRevisesReferencesWithoutCuttingTheSegment: renaming a
// strategy changes its output context and nothing a Worker executes. The
// Query Group keeps its Segment (same start, same object digest, same
// publication) and every Plan record is carried over verbatim; only the
// output context reference on that Segment moves, and the new context
// object is stored.
//
// Not pinned here, because it needs the step 4 read API: a Slot at an
// evaluation time before the edit's boundary resolves the old context, one
// at or after it the new one, and a retry of either resolves the same one
// it first did (design §11 item 8).
func TestOutputContextEditRevisesReferencesWithoutCuttingTheSegment(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-context-edit")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 80, renameStrategy)
	groupA, groupB := splitEdited(t, first, second)
	firstSnapshot := fixture.publish(t, first, 60)
	segmentA := fixture.openSegment(t, groupA.Identity, 60)
	bytesB := fixture.timelineBytes(t, groupB.Identity)
	before := fixture.activation(t)

	secondSnapshot := fixture.publish(t, second, 120)
	if secondSnapshot.Publication == firstSnapshot.Publication {
		t.Fatal("the rename must publish a new revision")
	}
	var renamed controlplane.QueryGroup
	for _, group := range second.QueryGroups {
		if group.Identity == groupA.Identity {
			renamed = group
		}
	}
	wantContext, err := controlplane.DeriveOutputContextDigest(renamed.Plans[0])
	if err != nil {
		t.Fatal(err)
	}
	if wantContext == segmentA.OutputContextRefFor(groupA.Plans[0].Identity) {
		t.Fatal("the rename must move the output context digest, or the test edits nothing")
	}

	kept := fixture.openSegment(t, groupA.Identity, 120)
	if kept.Start != segmentA.Start || kept.ObjectDigest != segmentA.ObjectDigest || kept.Publication != segmentA.Publication {
		t.Fatalf("a rename must not cut the Segment: before=%+v after=%+v", segmentA, kept)
	}
	if got := fixture.timelineBytes(t, groupB.Identity); !reflect.DeepEqual(got, bytesB) {
		t.Fatalf("untouched Query Group's timeline was rewritten:\n before=%s\n after=%s", bytesB, got)
	}
	after := fixture.activation(t)
	if after.Current != secondSnapshot.Publication {
		t.Fatalf("activation current=%+v want %+v", after.Current, secondSnapshot.Publication)
	}
	if !reflect.DeepEqual(after.Plans, before.Plans) {
		t.Fatalf("a rename must carry every Plan record over verbatim:\n before=%+v\n after=%+v", before.Plans, after.Plans)
	}
	if exists, err := fixture.client.Exists(fixture.ctx, fixture.prefix+":outctx:"+string(wantContext)).Result(); err != nil || exists != 1 {
		t.Fatalf("the renamed strategy's output context object is not stored: exists=%d err=%v", exists, err)
	}
}

// TestLegacyOpenSegmentIsCutOnceThenKept: an open Segment written before
// step 3 names no content. The first publication after the upgrade cuts it
// once, whatever the content did, so that every open Segment names its
// content; the publication after that keeps it.
func TestLegacyOpenSegmentIsCutOnceThenKept(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-legacy-segment")
	first := cutoverCatalog(t, 80, nil)
	groupA, _ := splitEdited(t, first, cutoverCatalog(t, 80, renameStrategy))
	fixture.publish(t, first, 60)
	key := fixture.prefix + ":schedule_timeline:" + string(groupA.Identity)
	var timeline map[string]any
	if err := json.Unmarshal(fixture.timelineBytes(t, groupA.Identity), &timeline); err != nil {
		t.Fatal(err)
	}
	segments := timeline["segments"].([]any)
	segment := segments[len(segments)-1].(map[string]any)["schedule"].(map[string]any)["Segment"].(map[string]any)
	if _, named := segment["ObjectDigest"]; !named {
		t.Fatalf("fixture expects the open Segment to name its content: %+v", segment)
	}
	delete(segment, "ObjectDigest")
	delete(segment, "OutputContextRefs")
	legacy, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Set(fixture.ctx, key, legacy, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	fixture.publish(t, cutoverCatalog(t, 80, renameStrategy), 120)
	cut := fixture.openSegment(t, groupA.Identity, 120)
	if cut.Start != 120 || cut.ObjectDigest == "" {
		t.Fatalf("a Segment without content must be cut once so it names its content: %+v", cut)
	}

	fixture.publish(t, cutoverCatalog(t, 80, func(document map[string]any) {
		renameStrategy(document)
		document["description"] = "edited again"
	}), 180)
	kept := fixture.openSegment(t, groupA.Identity, 180)
	if kept.Start != 120 || kept.ObjectDigest != cut.ObjectDigest {
		t.Fatalf("a Segment that names its unchanged content must be kept: cut=%+v later=%+v", cut, kept)
	}
}

// TestCutoverDecidesByPersistedSegmentsWhenTheOldSnapshotIsGone: the
// decision to cut is read from the persisted open Segment, so losing the
// previous whole Snapshot changes nothing about which Query Groups are cut.
func TestCutoverDecidesByPersistedSegmentsWhenTheOldSnapshotIsGone(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-old-snapshot-gone")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	groupA, groupB := splitEdited(t, first, second)
	firstSnapshot := fixture.publish(t, first, 60)
	segmentA := fixture.openSegment(t, groupA.Identity, 60)
	bytesB := fixture.timelineBytes(t, groupB.Identity)
	if err := fixture.client.Del(fixture.ctx, fixture.prefix+":snapshot:"+string(firstSnapshot.Publication.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}

	fixture.publish(t, second, 120)
	cut := fixture.openSegment(t, groupA.Identity, 120)
	if cut.Start != 120 || cut.ObjectDigest == segmentA.ObjectDigest {
		t.Fatalf("edited Query Group must be cut without the old Snapshot: before=%+v after=%+v", segmentA, cut)
	}
	if got := fixture.timelineBytes(t, groupB.Identity); !reflect.DeepEqual(got, bytesB) {
		t.Fatalf("untouched Query Group's timeline was rewritten without the old Snapshot:\n before=%s\n after=%s", bytesB, got)
	}
}

type objectWriteRefusingHook struct{}

func (objectWriteRefusingHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	return ctx, refuseObjectWrite(cmd)
}

func (objectWriteRefusingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (objectWriteRefusingHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		if err := refuseObjectWrite(cmd); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func (objectWriteRefusingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func refuseObjectWrite(cmd redis.Cmder) error {
	if strings.ToLower(cmd.Name()) != "setnx" || len(cmd.Args()) < 2 {
		return nil
	}
	if key, ok := cmd.Args()[1].(string); ok && strings.Contains(key, ":qgobj:") {
		return errors.New("object store refused")
	}
	return nil
}

// TestPublicationFailsWhenTheObjectCatalogCannotBeWritten: once a Segment
// outlives the publication it was opened under, the object is what a Worker
// reads and the whole Snapshot is no longer there to fall back on. A
// publication whose objects cannot be stored therefore does not publish.
func TestPublicationFailsWhenTheObjectCatalogCannotBeWritten(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-object-write-refused")
	fixture.client.AddHook(objectWriteRefusingHook{})
	if _, _, err := fixture.repository.PublishCatalog(fixture.ctx, cutoverCatalog(t, 80, nil)); err == nil {
		t.Fatal("a publication whose execution objects cannot be stored must fail")
	}
	if _, err := fixture.repository.LoadLatestPublication(fixture.ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("a refused publication must leave nothing published: err=%v", err)
	}
}
