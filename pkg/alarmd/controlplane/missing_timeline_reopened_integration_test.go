// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group whose timeline key went missing - evicted, expired, deleted -
// was opened again only by a cutover to a new publication. Under the
// publication the activation already runs nothing read the key again, so the
// Query Group ran nothing until the next strategy change, however long that
// was. And a Control Leader that had not written the activation head it read
// gave that Query Group no records and did not say so, so its first cutover
// found an open Segment's Plans without records and failed whole. These pin
// the way out: missing is named, and opened again under the publication the
// activation runs.

// reopenedOutcome is the repair outcome a missing timeline opened again is
// counted under.
const reopenedOutcome = controlplane.TimelineRepairOutcome("reopened")

// absentSkip is the reason the activation read names a missing key by.
const absentSkip = controlplane.SkippedTimelineReason("absent")

func (fixture *cutoverFixture) deleteTimeline(t *testing.T, group execution.QueryGroupIdentity) {
	t.Helper()
	if err := fixture.client.Del(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group)).Err(); err != nil {
		t.Fatal(err)
	}
}

func (fixture *cutoverFixture) timelinePresent(t *testing.T, group execution.QueryGroupIdentity) bool {
	t.Helper()
	present, err := fixture.client.Exists(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return present == 1
}

// A Worker names the Query Group whose timeline key is gone; under the
// publication the activation runs, the Leader reads the key, finds it absent,
// and opens the timeline again as the cutover's absent branch does: one
// Segment from its boundary on the content and records the Query Group runs,
// marked as an absent-key reopen, at a revision one above the one the
// Assignment record names, written only while the key is still absent.
// Unnamed, nothing reads it.
func TestANamedMissingTimelineIsReopenedUnderTheCurrentPublication(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:missing-current")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	other, gone := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	if err := fixture.client.HSet(fixture.ctx, recordKey(gone.Identity), "desired_worker_id", "w1",
		"timeline_record_revision", "4").Err(); err != nil {
		t.Fatal(err)
	}
	before := fixture.activation(t)
	records := recordsOf(before, gone)
	otherBytes := fixture.timelineBytes(t, other.Identity)
	fixture.deleteTimeline(t, gone.Identity)

	if state := fixture.ensureAt(t, snapshot.Publication, 120); state.RecordRevision != before.RecordRevision {
		t.Fatalf("an unnamed round wrote the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if fixture.timelinePresent(t, gone.Identity) {
		t.Fatal("an unnamed round opened the timeline")
	}

	fixture.name(gone.Identity)
	after := fixture.ensureAt(t, snapshot.Publication, 180)
	if after.Current != snapshot.Publication || after.RecordRevision != before.RecordRevision+1 {
		t.Fatalf("activation after the round = revision %d on %+v, want %d on the same publication: "+
			"a missing timeline named under the current publication was not opened again",
			after.RecordRevision, after.Current, before.RecordRevision+1)
	}
	if !fixture.timelinePresent(t, gone.Identity) {
		t.Fatal("the named Query Group's timeline key is still absent")
	}
	if !reflect.DeepEqual(sortedRecords(recordsOf(after, gone)), sortedRecords(records)) {
		t.Fatalf("the reopen changed the Query Group's records:\n before=%+v\n after=%+v", records, recordsOf(after, gone))
	}
	opened := fixture.openSegment(t, gone.Identity, 180)
	if opened.Start != 180 || opened.End != nil || opened.ObjectDigest != digestOf(t, gone) {
		t.Fatalf("the reopened timeline's open Segment = %+v, want one from 180 on its own content", opened)
	}
	repair, marked, err := fixture.runtime.ReadSegmentRepair(fixture.ctx, gone.Identity, 180)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairAbsent || repair.AtUnixMilli != 180_000 {
		t.Fatalf("the Segment's mark = %+v marked %v (%v), want an absent-key reopen at 180", repair, marked, err)
	}
	if got := fixture.persistedRevision(t, gone.Identity); got != 5 {
		t.Fatalf("the reopened timeline is at revision %d, want 5: one above what the Assignment record named", got)
	}
	if stamped, err := fixture.client.HGet(fixture.ctx, recordKey(gone.Identity), "timeline_record_revision").Result(); err != nil || stamped != "5" {
		t.Fatalf("the Assignment record names timeline revision %q (%v), want 5", stamped, err)
	}
	if got := fixture.timelineBytes(t, other.Identity); !reflect.DeepEqual(got, otherBytes) {
		t.Fatal("the reopen rewrote a timeline nobody named")
	}
	if got := repairCount(fixture.repository, reopenedOutcome); got != 1 {
		t.Fatalf("reopened = %d, want 1", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairRewritten); got != 0 {
		t.Fatalf("rewritten = %d, want 0: a reopen is not a rewrite of bytes that did not decode", got)
	}
}

// A timeline written back between the read that found the key absent and the
// write wins: the reopen is written only while the key is absent, loses,
// writes nothing - neither the timeline nor the activation - and says so.
func TestAMissingTimelineThatReappearsIsNotOverwritten(t *testing.T) {
	fixture, _ := repairFixture(t, "alarmd:control:missing-reappears")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, gone := splitEdited(t, first, second)
	snapshot := fixture.publish(t, first, 60)
	key := fixture.prefix + ":schedule_timeline:" + string(gone.Identity)
	good := fixture.timelineBytes(t, gone.Identity)
	before := fixture.activation(t)
	fixture.deleteTimeline(t, gone.Identity)
	other := redis.NewClient(fixture.client.Options())
	t.Cleanup(func() { _ = other.Close() })
	var armed atomic.Bool
	armed.Store(true)
	fixture.client.AddHook(reappearHook{armed: &armed, restore: func(ctx context.Context) {
		_ = other.Set(ctx, key, good, time.Hour).Err()
	}})
	fixture.name(gone.Identity)
	state := fixture.ensureAt(t, snapshot.Publication, 120)
	if armed.Load() {
		t.Fatal("the concurrent write was never made: the case did not reach the reopen")
	}
	if state.RecordRevision != before.RecordRevision {
		t.Fatalf("a lost reopen moved the activation: %d -> %d", before.RecordRevision, state.RecordRevision)
	}
	if got := fixture.timelineBytes(t, gone.Identity); !reflect.DeepEqual(got, good) {
		t.Fatalf("the timeline written back first was overwritten: %q", got)
	}
	if got := repairCount(fixture.repository, controlplane.TimelineRepairConflict); got != 1 {
		t.Fatalf("conflict = %d, want 1", got)
	}
	if got := repairCount(fixture.repository, reopenedOutcome); got != 0 {
		t.Fatalf("reopened = %d, want 0", got)
	}
}

// A named Query Group whose key Redis cannot read is not opened again: the
// round fails as any read does, and the key is left as it was. An error is
// not a missing key - neither an error the store answers with, nor a read
// that never got an answer. The second is the one that tells: its key is
// there, so a read error taken for absent would reach the write, lose it to
// the key that is there, and let the round succeed as a conflict.
func TestANamedTimelineRedisCannotReadIsNotReopened(t *testing.T) {
	for _, shape := range []string{"error reply", "transport"} {
		t.Run(shape, func(t *testing.T) {
			fixture, _ := repairFixture(t, "alarmd:control:missing-read-error-"+strings.ReplaceAll(shape, " ", "-"))
			first := cutoverCatalog(t, 80, nil)
			second := cutoverCatalog(t, 90, nil)
			_, broken := splitEdited(t, first, second)
			snapshot := fixture.publish(t, first, 60)
			key := fixture.prefix + ":schedule_timeline:" + string(broken.Identity)
			before := fixture.activation(t)
			good := fixture.timelineBytes(t, broken.Identity)
			if shape == "error reply" {
				fixture.deleteTimeline(t, broken.Identity)
				if err := fixture.client.HSet(fixture.ctx, key, "not", "a string").Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				var armed atomic.Bool
				armed.Store(true)
				fixture.client.AddHook(failingGetHook{key: key, armed: &armed})
			}
			fixture.name(broken.Identity)
			*fixture.now = time.Unix(120, 0)
			if _, err := fixture.reconciler.Ensure(fixture.ctx, snapshot.Publication); err == nil {
				t.Fatal("Ensure succeeded over a named timeline Redis could not read, want the round failed")
			}
			if shape == "error reply" {
				if kind, err := fixture.client.Type(fixture.ctx, key).Result(); err != nil || kind != "hash" {
					t.Fatalf("the unreadable key is now a %q (%v), want it left as it was", kind, err)
				}
			} else {
				reader := redis.NewClient(fixture.client.Options())
				t.Cleanup(func() { _ = reader.Close() })
				if got, err := reader.Get(fixture.ctx, key).Bytes(); err != nil || !reflect.DeepEqual(got, good) {
					t.Fatalf("the timeline Redis could not be asked for is now %q (%v), want it as it was", got, err)
				}
				if state, err := controlplane.NewRedisCatalogRepository(reader, fixture.prefix, time.Hour); err != nil {
					t.Fatal(err)
				} else if after, err := state.LoadActivationHead(fixture.ctx); err != nil || after.RecordRevision != before.RecordRevision {
					t.Fatalf("the activation moved to %d (%v), want %d", after.RecordRevision, err, before.RecordRevision)
				}
			}
			for _, outcome := range []controlplane.TimelineRepairOutcome{reopenedOutcome, controlplane.TimelineRepairConflict} {
				if got := repairCount(fixture.repository, outcome); got != 0 {
					t.Fatalf("%s = %d, want 0: a read error is not a missing key", outcome, got)
				}
			}
		})
	}
}

// A new leader - a process that did not write the activation head it reads -
// meets a Query Group whose timeline key is gone. It names it, loads the
// rest, rebuilds that Query Group's records from the publication it runs and
// opens its timeline again under that publication, without waiting for a
// Worker to report it. Before, the read gave it no records and said nothing.
func TestAFreshLeaderNamesAMissingTimelineAndReopensIt(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:missing-fresh")
	first := cutoverCatalog(t, 80, nil)
	snapshot := fixture.publish(t, first, 60)
	good, gone := first.QueryGroups[0], first.QueryGroups[1]
	before := fixture.activation(t)
	goneRecords := recordsOf(before, gone)
	goodBytes := fixture.timelineBytes(t, good.Identity)
	fixture.deleteTimeline(t, gone.Identity)

	leader := fixture.freshLeader(t, recordKey)
	loaded, err := leader.repository.LoadActivation(leader.ctx)
	if err != nil {
		t.Fatalf("a new leader's activation read with one key gone = %v", err)
	}
	if want := []controlplane.SkippedTimeline{{QueryGroup: gone.Identity, Reason: absentSkip}}; !reflect.DeepEqual(loaded.SkippedTimelines, want) {
		t.Fatalf("skipped = %+v, want %+v: a missing key is named, never taken for benign", loaded.SkippedTimelines, want)
	}
	if got := skippedCount(leader.repository, absentSkip); got != 1 {
		t.Fatalf("absent skips = %d, want 1", got)
	}

	after := leader.ensureAt(t, snapshot.Publication, 180)
	if after.Current != snapshot.Publication || after.RecordRevision != before.RecordRevision+1 {
		t.Fatalf("activation after the round = revision %d on %+v, want %d on the same publication",
			after.RecordRevision, after.Current, before.RecordRevision+1)
	}
	if len(after.SkippedTimelines) != 0 {
		t.Fatalf("the activation read after the reopen still skips %+v", after.SkippedTimelines)
	}
	if rebuilt := recordsOf(after, gone); !reflect.DeepEqual(sortedRecords(rebuilt), sortedRecords(goneRecords)) {
		t.Fatalf("the rebuilt records differ from the lost ones:\n lost=%+v\n rebuilt=%+v", goneRecords, rebuilt)
	}
	repair, marked, err := leader.runtime.ReadSegmentRepair(leader.ctx, gone.Identity, 180)
	if err != nil || !marked || repair.Kind != execution.SegmentRepairAbsent {
		t.Fatalf("the Segment's mark = %+v marked %v (%v), want an absent-key reopen", repair, marked, err)
	}
	if got := fixture.timelineBytes(t, good.Identity); !reflect.DeepEqual(got, goodBytes) {
		t.Fatal("the reopen touched the good Query Group's timeline")
	}
	if got := repairCount(leader.repository, reopenedOutcome); got != 1 {
		t.Fatalf("reopened = %d, want 1", got)
	}
}

// The same new leader meeting a new publication: its first cutover reads
// every open Segment and used to fail whole, finding the missing Query
// Group's Plans with no record; now the edited Query Group is cut over and
// the missing one is opened again in the same cutover, on records that do not
// warm again.
func TestAFreshLeaderCutsOverPastAMissingTimeline(t *testing.T) {
	fixture, recordKey := repairFixture(t, "alarmd:control:missing-cutover")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	before := fixture.activation(t)
	untouchedRecords := recordsOf(before, untouched)
	fixture.deleteTimeline(t, untouched.Identity)

	leader := fixture.freshLeader(t, recordKey)
	*leader.now = time.Unix(120, 0)
	next, _, err := leader.repository.PublishCatalog(leader.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	*leader.now = time.Unix(180, 0)
	after, err := leader.reconciler.Ensure(leader.ctx, next.Publication)
	if err != nil {
		t.Fatalf("a new leader's cutover past a missing timeline = %v, want it cut over", err)
	}
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
	if err != nil || !marked || repair.Kind != execution.SegmentRepairAbsent {
		t.Fatalf("the missing Query Group's Segment mark = %+v marked %v (%v), want an absent-key reopen", repair, marked, err)
	}
	rebuilt := recordsOf(after, untouched)
	if !reflect.DeepEqual(recordsWithoutEpoch(rebuilt), recordsWithoutEpoch(untouchedRecords)) {
		t.Fatalf("the missing Query Group's records after the cutover differ from the lost ones beyond the epoch:\n lost=%+v\n now=%+v",
			untouchedRecords, rebuilt)
	}
	for _, record := range rebuilt {
		if record.Fact.Selected.ForceWarming {
			t.Fatalf("an unchanged Query Group read back from nothing warms again: %+v", record)
		}
	}
}
