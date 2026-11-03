// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Control Leader that did not write the activation head it reads - a
// restart, a new leader - reads every active Query Group's Plan records back
// from its open Segment. One timeline that did not decode failed that whole
// read, so a new leader cut nothing over for anyone and never reached the
// repair of the one bad key. These pin the way out: the bad key is left out
// and named, the rest loads and cuts over, the undecodable key is rebuilt
// from the publication and rewritten, a key another schema wrote is left
// alone, and a read that fails still fails the read.

// newerFormatTimeline says it is a timeline of a schema this build does not
// write: bytes a later build would have written.
const newerFormatTimeline = `{"schema_version":"alarmd-control-schedule-timeline-v9","record_revision":3}`

// freshLeader is the same store opened by another process: a repository with
// nothing of its own writes in memory, so the activation head it reads gets
// its records back from the open Segments.
func (fixture *cutoverFixture) freshLeader(t *testing.T, recordKey func(execution.QueryGroupIdentity) string) *cutoverFixture {
	t.Helper()
	repository, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if recordKey != nil {
		repository.WithAssignmentRecordKey(recordKey)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	now := fixture.now
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress,
		func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &cutoverFixture{ctx: fixture.ctx, client: fixture.client, prefix: fixture.prefix,
		repository: repository, reconciler: reconciler, runtime: runtime, now: now}
}

func skippedCount(repository *controlplane.RedisCatalogRepository, reason controlplane.SkippedTimelineReason) uint64 {
	return repository.SkippedTimelinesReading().Counts[reason]
}

// The Leader's own records for a Query Group, without the publication epoch
// they were stamped with: a rebuilt record takes the current publication's.
func recordsWithoutEpoch(records []controlplane.PlanActivationRecord) []controlplane.PlanActivationRecord {
	stripped := sortedRecords(records)
	for index := range stripped {
		stripped[index].Fact.Selected.StateApplyEpoch = 0
		stripped[index].Publication = controlplane.SnapshotPublicationRef{}
	}
	return stripped
}

// A new leader meets one timeline that does not decode beside one that does.
// The activation loads, with the good Query Group's records and the bad one
// named; under the publication it already runs, the bad one's records are
// rebuilt from that publication and its timeline rewritten. The rebuilt
// records are the lost ones - same generation, no warming again - so the
// State on file and the event identity that carries the generation stay the
// Query Group's own.
func TestAFreshLeaderRebuildsAnUndecodableTimelineAndLoadsTheRest(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:skip-undecodable")
	first := cutoverCatalog(t, 80, nil)
	snapshot := fixture.publish(t, first, 60)
	good, broken := first.QueryGroups[0], first.QueryGroups[1]
	before := fixture.activation(t)
	brokenRecords := recordsOf(before, broken)
	goodBytes := fixture.timelineBytes(t, good.Identity)
	fixture.corrupt(t, broken.Identity, unreadableTimeline)

	leader := fixture.freshLeader(t, recordKey)
	loaded, err := leader.repository.LoadActivation(leader.ctx)
	if err != nil {
		t.Fatalf("a new leader's activation read with one bad timeline = %v, want it loaded without that one", err)
	}
	if want := []controlplane.SkippedTimeline{{QueryGroup: broken.Identity, Reason: controlplane.SkippedTimelineUndecodable}}; !reflect.DeepEqual(loaded.SkippedTimelines, want) {
		t.Fatalf("skipped = %+v, want %+v", loaded.SkippedTimelines, want)
	}
	if !reflect.DeepEqual(sortedRecords(recordsOf(loaded, good)), sortedRecords(recordsOf(before, good))) || len(recordsOf(loaded, broken)) != 0 {
		t.Fatalf("records after the read: good %+v, bad %+v; want the good one's as before and none for the bad one",
			recordsOf(loaded, good), recordsOf(loaded, broken))
	}
	if reading := leader.repository.SkippedTimelinesReading(); reading.Counts[controlplane.SkippedTimelineUndecodable] != 1 ||
		reading.Total != 1 || len(reading.Named) != 1 || reading.Named[0].QueryGroup != broken.Identity {
		t.Fatalf("the reading = %+v, want the bad Query Group named undecodable, counted once", reading)
	}

	after := leader.ensureAt(t, snapshot.Publication, 180)
	if after.Current != snapshot.Publication || after.RecordRevision != before.RecordRevision+1 {
		t.Fatalf("activation after the round = revision %d on %+v, want %d on the same publication",
			after.RecordRevision, after.Current, before.RecordRevision+1)
	}
	if len(after.SkippedTimelines) != 0 {
		t.Fatalf("the activation read after the rewrite still skips %+v", after.SkippedTimelines)
	}
	rebuilt := recordsOf(after, broken)
	if !reflect.DeepEqual(sortedRecords(rebuilt), sortedRecords(brokenRecords)) {
		t.Fatalf("the rebuilt records differ from the lost ones:\n lost=%+v\n rebuilt=%+v", brokenRecords, rebuilt)
	}
	for _, record := range rebuilt {
		if record.Fact.Selected.ForceWarming {
			t.Fatalf("a rebuilt record warms again: %+v", record)
		}
	}
	opened := leader.openSegment(t, broken.Identity, 180)
	if opened.Start != 180 || opened.End != nil || opened.ObjectDigest != digestOf(t, broken) {
		t.Fatalf("the rewritten timeline's open Segment = %+v, want one from 180 on its own content", opened)
	}
	repair, marked, err := leader.runtime.ReadSegmentRepair(leader.ctx, broken.Identity, 180)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairUnreadable {
		t.Fatalf("the Segment's mark = %+v marked %v (%v), want an unreadable rewrite", repair, marked, err)
	}
	if got := fixture.timelineBytes(t, good.Identity); !reflect.DeepEqual(got, goodBytes) {
		t.Fatal("the rebuild touched the good Query Group's timeline")
	}
	if got := repairCount(leader.repository, controlplane.TimelineRepairRewritten); got != 1 {
		t.Fatalf("rewritten = %d, want 1", got)
	}
	// The row names what is still unread: nothing, once the rewrite landed;
	// the count keeps the skip that happened.
	if reading := leader.repository.SkippedTimelinesReading(); reading.Total != 0 || len(reading.Named) != 0 ||
		reading.Counts[controlplane.SkippedTimelineUndecodable] != 1 {
		t.Fatalf("the reading after the rewrite = %+v, want nothing named and the one skip counted", reading)
	}
}

// The same new leader meeting a new publication: the Query Group whose content
// changed is cut over to it, and the one with the bad timeline - content
// unchanged - is rewritten in the same cutover on records that do not warm
// again.
func TestAFreshLeaderCutsTheRestOverPastAnUndecodableTimeline(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:skip-cutover")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	before := fixture.activation(t)
	untouchedRecords := recordsOf(before, untouched)
	fixture.corrupt(t, untouched.Identity, unreadableTimeline)

	leader := fixture.freshLeader(t, recordKey)
	*leader.now = time.Unix(120, 0)
	next, _, err := leader.repository.PublishCatalog(leader.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	after := leader.ensureAt(t, next.Publication, 180)
	if after.Current != next.Publication {
		t.Fatalf("the new leader's activation is on %+v, want the new publication %+v", after.Current, next.Publication)
	}
	var editedNow controlplane.QueryGroup
	for _, group := range second.QueryGroups {
		if group.Identity == edited.Identity {
			editedNow = group
		}
	}
	if opened := leader.openSegment(t, edited.Identity, 180); opened.ObjectDigest != digestOf(t, editedNow) {
		t.Fatalf("the edited Query Group's open Segment names %s, want its new content %s", opened.ObjectDigest, digestOf(t, editedNow))
	}
	repair, marked, err := leader.runtime.ReadSegmentRepair(leader.ctx, untouched.Identity, 180)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairUnreadable {
		t.Fatalf("the bad Query Group's Segment mark = %+v marked %v (%v), want an unreadable rewrite", repair, marked, err)
	}
	rebuilt := recordsOf(after, untouched)
	if !reflect.DeepEqual(recordsWithoutEpoch(rebuilt), recordsWithoutEpoch(untouchedRecords)) {
		t.Fatalf("the bad Query Group's records after the cutover differ from the lost ones beyond the epoch:\n lost=%+v\n now=%+v",
			untouchedRecords, rebuilt)
	}
	for _, record := range rebuilt {
		if record.Fact.Selected.ForceWarming {
			t.Fatalf("an unchanged Query Group read back from nothing warms again: %+v", record)
		}
	}
}

// A timeline another schema wrote is named and left exactly as it is, under
// the publication the activation runs and across a cutover to a new one: it
// is what the build that wrote it runs by.
func TestAFreshLeaderLeavesANewerFormatTimelineAlone(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:skip-newer")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	before := fixture.activation(t)
	fixture.corrupt(t, untouched.Identity, newerFormatTimeline)

	leader := fixture.freshLeader(t, recordKey)
	loaded, err := leader.repository.LoadActivation(leader.ctx)
	if err != nil {
		t.Fatalf("a new leader's activation read with a newer-format timeline = %v, want it loaded without that one", err)
	}
	if want := []controlplane.SkippedTimeline{{QueryGroup: untouched.Identity, Reason: controlplane.SkippedTimelineNewerFormat}}; !reflect.DeepEqual(loaded.SkippedTimelines, want) {
		t.Fatalf("skipped = %+v, want %+v", loaded.SkippedTimelines, want)
	}
	if state := leader.ensureAt(t, snapshot.Publication, 120); state.RecordRevision != before.RecordRevision {
		t.Fatalf("a round under the same publication wrote the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := string(fixture.timelineBytes(t, untouched.Identity)); got != newerFormatTimeline {
		t.Fatalf("the newer-format timeline was touched: %q", got)
	}
	*leader.now = time.Unix(150, 0)
	next, _, err := leader.repository.PublishCatalog(leader.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if after := leader.ensureAt(t, next.Publication, 180); after.Current != next.Publication {
		t.Fatalf("the cutover past a newer-format timeline is on %+v, want %+v", after.Current, next.Publication)
	}
	var editedNow controlplane.QueryGroup
	for _, group := range second.QueryGroups {
		if group.Identity == edited.Identity {
			editedNow = group
		}
	}
	if opened := leader.openSegment(t, edited.Identity, 180); opened.ObjectDigest != digestOf(t, editedNow) {
		t.Fatalf("the edited Query Group was not cut over: open Segment names %s", opened.ObjectDigest)
	}
	if got := string(fixture.timelineBytes(t, untouched.Identity)); got != newerFormatTimeline {
		t.Fatalf("the cutover touched the newer-format timeline: %q", got)
	}
	if reading := leader.repository.SkippedTimelinesReading(); reading.Total != 1 || len(reading.Named) != 1 ||
		reading.Named[0] != (controlplane.SkippedTimeline{QueryGroup: untouched.Identity, Reason: controlplane.SkippedTimelineNewerFormat}) {
		t.Fatalf("the reading after the cutover = %+v, want the newer-format timeline still named", reading)
	}
	if got := skippedCount(leader.repository, controlplane.SkippedTimelineNewerFormat); got == 0 {
		t.Fatal("the newer-format timeline was not counted")
	}
	if got := repairCount(leader.repository, controlplane.TimelineRepairRewritten); got != 0 {
		t.Fatalf("rewritten = %d, want 0", got)
	}
}

// A key Redis cannot read as a timeline - here one of the wrong type, which
// it answers with an error - says nothing about the timeline. The read fails
// as before: nothing is skipped, counted, rebuilt or written.
func TestAFreshLeadersReadErrorFailsTheReadAndSkipsNothing(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:skip-read-error")
	first := cutoverCatalog(t, 80, nil)
	snapshot := fixture.publish(t, first, 60)
	broken := first.QueryGroups[1]
	key := fixture.prefix + ":schedule_timeline:" + string(broken.Identity)
	if err := fixture.client.Del(fixture.ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.HSet(fixture.ctx, key, "not", "a string").Err(); err != nil {
		t.Fatal(err)
	}

	leader := fixture.freshLeader(t, recordKey)
	if _, err := leader.repository.LoadActivation(leader.ctx); err == nil {
		t.Fatal("an activation read Redis answered with an error for one timeline succeeded, want it failed")
	} else {
		var dependency *controlplane.ActivationDependencyIOError
		if !errors.As(err, &dependency) {
			t.Fatalf("the read failed with %v, want the dependency's failure", err)
		}
	}
	*leader.now = time.Unix(120, 0)
	if _, err := leader.reconciler.Ensure(leader.ctx, snapshot.Publication); err == nil {
		t.Fatal("Ensure succeeded over a timeline Redis could not read")
	}
	reading := leader.repository.SkippedTimelinesReading()
	for _, reason := range controlplane.SkippedTimelineReasons {
		if reading.Counts[reason] != 0 {
			t.Fatalf("a read error was counted as a skip: %+v", reading)
		}
	}
	if reading.Total != 0 || len(reading.Named) != 0 {
		t.Fatalf("a read error named a skip: %+v", reading)
	}
	if kind, err := fixture.client.Type(fixture.ctx, key).Result(); err != nil || kind != "hash" {
		t.Fatalf("the unreadable key is now a %q (%v), want it left as it was", kind, err)
	}
}
