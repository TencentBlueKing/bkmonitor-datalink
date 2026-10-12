// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group whose timeline stopped decoding ran nothing for as long as
// the key lived, and the renewal kept it alive. These pin the way out: a
// timeline a Worker names is read again by the Leader under the publication
// it already runs, and rewritten when the Leader cannot decode it either;
// a cutover that meets such bytes rewrites them rather than holding the
// Query Group back. In every case the rewrite keeps the records, opens one
// Segment at the Leader's boundary marked as a rewrite, moves the revision
// the Assignment record names forward, and is fenced on the bytes it read.

const unreadableTimeline = "{not a timeline"

func repairFixture(t *testing.T, prefix string) (*cutoverFixture, func(execution.QueryGroupIdentity) string) {
	t.Helper()
	fixture := newCutoverFixture(t, prefix)
	recordKey := func(queryGroup execution.QueryGroupIdentity) string {
		return prefix + ":assignment:" + string(queryGroup)
	}
	fixture.repository.WithAssignmentRecordKey(recordKey)
	return fixture, recordKey
}

func (fixture *cutoverFixture) name(queryGroups ...execution.QueryGroupIdentity) {
	requests := &controlplane.TimelineRepairRequests{}
	requests.Replace(queryGroups)
	fixture.reconciler.WithTimelineRepairs(requests)
}

func (fixture *cutoverFixture) corrupt(t *testing.T, group execution.QueryGroupIdentity, payload string) {
	t.Helper()
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group), payload, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
}

func (fixture *cutoverFixture) ensureAt(t *testing.T, publication controlplane.SnapshotPublicationRef, at int64) controlplane.ActivationState {
	t.Helper()
	*fixture.now = time.Unix(at, 0)
	state, err := fixture.reconciler.Ensure(fixture.ctx, publication)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	return state
}

// persistedRevision is the record revision the stored timeline says it is
// at, read from its bytes rather than through the reader under test.
func (fixture *cutoverFixture) persistedRevision(t *testing.T, group execution.QueryGroupIdentity) uint64 {
	t.Helper()
	var stored struct {
		RecordRevision uint64 `json:"record_revision"`
	}
	if err := json.Unmarshal(fixture.timelineBytes(t, group), &stored); err != nil {
		t.Fatalf("the stored timeline of %s is not JSON: %v", group, err)
	}
	return stored.RecordRevision
}

func repairCount(repository *controlplane.RedisCatalogRepository, outcome controlplane.TimelineRepairOutcome) uint64 {
	return repository.TimelineRepairCounts()[outcome]
}

func TestANamedUnreadableTimelineIsRewrittenUnderTheCurrentPublication(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:repair-current")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	other, broken := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	// Placed, and the record says the timeline was at revision 7 when it was
	// last stamped: the rewrite has to go past it.
	if err := fixture.client.HSet(fixture.ctx, recordKey(broken.Identity), "desired_worker_id", "w1",
		"timeline_record_revision", "7").Err(); err != nil {
		t.Fatal(err)
	}
	before := fixture.activation(t)
	records := recordsOf(before, broken)
	otherBytes := fixture.timelineBytes(t, other.Identity)
	fixture.corrupt(t, broken.Identity, unreadableTimeline)

	// No name, no new publication: nothing reads the timeline again, which
	// is how a Query Group stayed dark.
	if state := fixture.ensureAt(t, snapshot.Publication, 120); state.RecordRevision != before.RecordRevision {
		t.Fatalf("an unnamed round wrote the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := string(fixture.timelineBytes(t, broken.Identity)); got != unreadableTimeline {
		t.Fatalf("an unnamed round touched the timeline: %q", got)
	}

	fixture.name(broken.Identity)
	after := fixture.ensureAt(t, snapshot.Publication, 180)
	if after.Current != snapshot.Publication || after.RecordRevision != before.RecordRevision+1 {
		t.Fatalf("activation after the repair = revision %d on %+v, want %d on the same publication",
			after.RecordRevision, after.Current, before.RecordRevision+1)
	}
	if !reflect.DeepEqual(sortedRecords(recordsOf(after, broken)), sortedRecords(records)) {
		t.Fatalf("the rewrite changed the Query Group's records:\n before=%+v\n after=%+v", records, recordsOf(after, broken))
	}
	if !reflect.DeepEqual(after.Draining, before.Draining) || len(after.Plans) != len(before.Plans) {
		t.Fatalf("the rewrite changed the activation beyond its revision: %+v", after)
	}
	opened := fixture.openSegment(t, broken.Identity, 180)
	if opened.Start != 180 || opened.End != nil || opened.ObjectDigest != digestOf(t, broken) ||
		opened.Publication.SnapshotRevision != records[0].Publication.SnapshotRevision {
		t.Fatalf("the rewritten timeline's open Segment = %+v, want one from 180 on its own content and its records' publication", opened)
	}
	repair, marked, err := fixture.runtime.ReadSegmentRepair(fixture.ctx, broken.Identity, 180)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairUnreadable || repair.AtUnixMilli != 180_000 {
		t.Fatalf("the Segment's mark = %+v marked %v (%v), want an unreadable rewrite at 180", repair, marked, err)
	}
	if got := fixture.persistedRevision(t, broken.Identity); got != 8 {
		t.Fatalf("the rewritten timeline is at revision %d, want 8: one above what the Assignment record named", got)
	}
	if stamped, err := fixture.client.HGet(fixture.ctx, recordKey(broken.Identity), "timeline_record_revision").Result(); err != nil || stamped != "8" {
		t.Fatalf("the Assignment record names timeline revision %q (%v), want 8", stamped, err)
	}
	if got := fixture.timelineBytes(t, other.Identity); !reflect.DeepEqual(got, otherBytes) {
		t.Fatal("the repair rewrote a timeline nobody named")
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 1 {
		t.Fatalf("rewritten = %d, want 1", got)
	}

	// The Worker's name outlives the rewrite until its next Slot runs: the
	// next round reads the timeline again, finds it decodes and writes
	// nothing.
	again := fixture.ensureAt(t, snapshot.Publication, 240)
	if again.RecordRevision != after.RecordRevision {
		t.Fatalf("a timeline that decodes again was rewritten: revision %d -> %d", after.RecordRevision, again.RecordRevision)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairDecodesAgain); got != 1 {
		t.Fatalf("decodes_again = %d, want 1", got)
	}
}

// A name for a timeline the Leader decodes is a timeline left alone: a Worker
// of another build may not read what this one does.
func TestANamedTimelineThatDecodesIsLeftAlone(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:repair-decodes")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, fine := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	before := fixture.activation(t)
	original := fixture.timelineBytes(t, fine.Identity)
	fixture.name(fine.Identity)
	if state := fixture.ensureAt(t, snapshot.Publication, 120); state.RecordRevision != before.RecordRevision {
		t.Fatalf("a decodable named timeline moved the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := fixture.timelineBytes(t, fine.Identity); !reflect.DeepEqual(got, original) {
		t.Fatal("a decodable named timeline was rewritten")
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairDecodesAgain); got != 1 {
		t.Fatalf("decodes_again = %d, want 1", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 0 {
		t.Fatalf("rewritten = %d, want 0", got)
	}
}

// Bytes that say they are a timeline of another schema were written by a
// build that reads them; this one leaves them rather than destroying them.
func TestANamedTimelineOfAnotherSchemaIsLeftAlone(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:repair-other-schema")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, foreign := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	before := fixture.activation(t)
	payload := `{"schema_version":"alarmd-control-schedule-timeline-v9","query_group":"` + string(foreign.Identity) + `"}`
	fixture.corrupt(t, foreign.Identity, payload)
	fixture.name(foreign.Identity)
	if state := fixture.ensureAt(t, snapshot.Publication, 120); state.RecordRevision != before.RecordRevision {
		t.Fatalf("another schema's timeline moved the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := string(fixture.timelineBytes(t, foreign.Identity)); got != payload {
		t.Fatalf("another schema's timeline was rewritten: %q", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairOtherSchema); got != 1 {
		t.Fatalf("other_schema = %d, want 1", got)
	}
}

// A good timeline written between the read and the write wins: the rewrite is
// fenced on the bytes that did not decode, loses, writes nothing - neither
// the timeline nor the activation - and says so.
func TestAGoodTimelineWrittenBeforeTheRepairWins(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:repair-conflict")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, broken := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	key := fixture.prefix + ":schedule_timeline:" + string(broken.Identity)
	good := fixture.timelineBytes(t, broken.Identity)
	before := fixture.activation(t)
	fixture.corrupt(t, broken.Identity, unreadableTimeline)
	other := redis.NewClient(fixture.client.Options())
	t.Cleanup(func() { _ = other.Close() })
	var armed atomic.Bool
	armed.Store(true)
	fixture.client.AddHook(reappearHook{armed: &armed, restore: func(ctx context.Context) {
		_ = other.Set(ctx, key, good, time.Hour).Err()
	}})
	fixture.name(broken.Identity)
	state := fixture.ensureAt(t, snapshot.Publication, 120)
	if armed.Load() {
		t.Fatal("the concurrent write was never made: the case did not reach the rewrite")
	}
	if state.RecordRevision != before.RecordRevision {
		t.Fatalf("a lost rewrite moved the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := fixture.timelineBytes(t, broken.Identity); !reflect.DeepEqual(got, good) {
		t.Fatalf("the good timeline written first was overwritten: %q", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairConflict); got != 1 {
		t.Fatalf("conflict = %d, want 1", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 0 {
		t.Fatalf("rewritten = %d, want 0", got)
	}
}

// The first cutover after a start reads every timeline; one that does not
// decode is rewritten there - the Query Group stays in the publication with
// its records - instead of being held back with nothing to run.
func TestTheFirstCutoverRewritesAnUnreadableTimeline(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:repair-cutover")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	records := recordsOf(fixture.activation(t), untouched)
	fixture.corrupt(t, untouched.Identity, unreadableTimeline)
	secondSnapshot, err := fixture.publishNoEnsure(t, second, 120)
	if err != nil {
		t.Fatalf("an unreadable timeline failed the publication: %v", err)
	}
	after := fixture.activation(t)
	if after.Current != secondSnapshot.Publication {
		t.Fatalf("activation current = %+v, want the new publication", after.Current)
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("the unreadable timeline was held back instead of rewritten: %+v", blocked)
	}
	if !reflect.DeepEqual(sortedRecords(recordsOf(after, untouched)), sortedRecords(records)) {
		t.Fatalf("the rewritten Query Group's records changed:\n before=%+v\n after=%+v", records, recordsOf(after, untouched))
	}
	opened := fixture.openSegment(t, untouched.Identity, 120)
	if opened.Start != 120 || opened.End != nil || opened.ObjectDigest != digestOf(t, untouched) {
		t.Fatalf("the rewritten timeline's open Segment = %+v, want one from 120 on its own content", opened)
	}
	if repair, marked, err := fixture.runtime.ReadSegmentRepair(fixture.ctx, untouched.Identity, 120); err != nil || !marked ||
		repair.Kind != execution.SegmentRepairUnreadable {
		t.Fatalf("the Segment's mark = %+v marked %v (%v), want an unreadable rewrite", repair, marked, err)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 1 {
		t.Fatalf("rewritten = %d, want 1", got)
	}
}

// A later cutover trusts the manifest for a Query Group whose content did not
// move and reads nothing of it. Named by a Worker, it is read anyway, and its
// unreadable timeline is rewritten by that cutover; not named, it is kept
// unread as before.
func TestALaterCutoverReadsANamedTimelineItWouldHaveKept(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "unnamed", true: "named"}[named], func(t *testing.T) {
			fixture, _ := repairFixture(t, "alarmd:control:repair-later-cutover-"+map[bool]string{false: "unnamed", true: "named"}[named])
			first := cutoverCatalog(t, 80, nil)
			second := cutoverCatalog(t, 90, nil)
			_, untouched := splitEdited(t, first, second)
			fixture.publish(t, first, 60)
			fixture.publish(t, second, 120)
			fixture.corrupt(t, untouched.Identity, unreadableTimeline)
			if named {
				fixture.name(untouched.Identity)
			}
			if _, err := fixture.publishNoEnsure(t, cutoverCatalog(t, 95, nil), 180); err != nil {
				t.Fatalf("third publication: %v", err)
			}
			stored := string(fixture.timelineBytes(t, untouched.Identity))
			if !named {
				if stored != unreadableTimeline {
					t.Fatalf("an unnamed unchanged Query Group's timeline was read and written: %q", stored)
				}
				return
			}
			if stored == unreadableTimeline {
				t.Fatal("a named unreadable timeline was kept unread by the cutover")
			}
			if opened := fixture.openSegment(t, untouched.Identity, 180); opened.Start != 180 {
				t.Fatalf("the named timeline's open Segment = %+v, want one from the cutover's boundary", opened)
			}
			if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 1 {
				t.Fatalf("rewritten = %d, want 1", got)
			}
		})
	}
}

// A named timeline that decodes is read by the cutover all the same, handled
// as any read one - kept, its content unchanged - and counted as decoding
// again.
func TestALaterCutoverHandlesANamedTimelineThatDecodes(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:repair-later-cutover-decodes")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	fixture.publish(t, second, 120)
	original := fixture.timelineBytes(t, untouched.Identity)
	fixture.name(untouched.Identity)
	if _, err := fixture.publishNoEnsure(t, cutoverCatalog(t, 95, nil), 180); err != nil {
		t.Fatalf("third publication: %v", err)
	}
	if got := fixture.timelineBytes(t, untouched.Identity); !reflect.DeepEqual(got, original) {
		t.Fatal("a named timeline that decodes and runs unchanged content was written")
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairDecodesAgain); got != 1 {
		t.Fatalf("decodes_again = %d, want 1", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 0 {
		t.Fatalf("rewritten = %d, want 0", got)
	}
}

// A timeline whose key is gone is opened again by the cutover, as before,
// and now marked as such, so the Worker whose cursor stood in it records
// the Slots it lost under SCHEDULE_REOPENED instead of jumping past them;
// and its revision goes one above what the Assignment record names, so a
// body cached under the old timeline's revision is never served for it.
func TestAReopenedTimelineIsMarkedAndMovesTheRevisionForward(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:reopen-marked")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, gone := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	if err := fixture.client.HSet(fixture.ctx, recordKey(gone.Identity), "desired_worker_id", "w1",
		"timeline_record_revision", "4").Err(); err != nil {
		t.Fatal(err)
	}
	records := recordsOf(fixture.activation(t), gone)
	if err := fixture.client.Del(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(gone.Identity)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatalf("a timeline whose key was gone failed the publication: %v", err)
	}
	if !reflect.DeepEqual(sortedRecords(recordsOf(fixture.activation(t), gone)), sortedRecords(records)) {
		t.Fatal("the reopened Query Group's records changed")
	}
	repair, marked, err := fixture.runtime.ReadSegmentRepair(fixture.ctx, gone.Identity, 120)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairAbsent || repair.AtUnixMilli != 120_000 {
		t.Fatalf("the reopened Segment's mark = %+v marked %v (%v), want an absent-key reopen at 120", repair, marked, err)
	}
	if got := fixture.persistedRevision(t, gone.Identity); got != 5 {
		t.Fatalf("the reopened timeline is at revision %d, want 5: one above what the Assignment record named", got)
	}
	if stamped, err := fixture.client.HGet(fixture.ctx, recordKey(gone.Identity), "timeline_record_revision").Result(); err != nil || stamped != "5" {
		t.Fatalf("the Assignment record names timeline revision %q (%v), want 5", stamped, err)
	}
	if _, _, reopened := fixture.repository.ActivationBlockedCounts(); reopened != 1 {
		t.Fatalf("reopened = %d, want 1", reopened)
	}
}
