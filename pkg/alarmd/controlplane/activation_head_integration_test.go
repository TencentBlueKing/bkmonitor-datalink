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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A later build writes the activation body as a head - no Plan records, the
// records being on the open Segments - and may leave a publication cutover
// committed in pieces unfinished (N15). This build is what a rollback lands
// on, so it must read both. These pin that it does.

// headCatalog builds a catalog from any of three strategies in three
// businesses, so three Query Groups: A (business 2, its threshold given),
// B (business 3) and C (business 4).
func headCatalog(t *testing.T, thresholdA int, sources string) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	withBusiness := func(document json.RawMessage, business string, id int) []byte {
		var decoded map[string]any
		if err := json.Unmarshal(withWireIdentity(t, document, "tenant-a", "bkcc__"+business), &decoded); err != nil {
			t.Fatal(err)
		}
		biz, _ := strconv.Atoi(business)
		decoded["bk_biz_id"] = float64(biz)
		decoded["id"] = float64(id)
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	all := map[byte]controlplane.SourceStrategy{
		'A': {SourceID: "1001", Document: []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":`+strconv.Itoa(thresholdA), 1)),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		'B': {SourceID: "1002", Document: withBusiness(documents[1], "3", 1002),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
		'C': {SourceID: "1003", Document: withBusiness(documents[1], "4", 1003),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "4", SpaceScope: "bkcc__4"}},
	}
	strategies := make([]controlplane.SourceStrategy, 0, len(sources))
	for index := 0; index < len(sources); index++ {
		strategies = append(strategies, all[sources[index]])
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{
		"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3"), "4": queryFactsFor(t, "4", "bkcc__4")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != len(sources) {
		t.Fatalf("expected %d Query Groups, got %d with dispositions %+v", len(sources), len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// groupOf is the Query Group of a catalog that carries the given business.
func groupOf(t *testing.T, catalog controlplane.Catalog, business string) controlplane.QueryGroup {
	t.Helper()
	for _, group := range catalog.QueryGroups {
		if group.Plans[0].Identity.BusinessID == business {
			return group
		}
	}
	t.Fatalf("no Query Group for business %s", business)
	return controlplane.QueryGroup{}
}

// asHead is the body a later build writes for the same activation: no
// records, schema v3.
func asHead(state controlplane.ActivationState) controlplane.ActivationState {
	state.SchemaVersion = controlplane.ActivationHeadSchemaVersionForTest
	state.Plans = nil
	return state
}

// retireTimeline closes a Query Group's open Segment at boundary and marks
// its timeline retired, as a cutover retiring it writes.
func (fixture *cutoverFixture) retireTimeline(t *testing.T, group execution.QueryGroupIdentity, boundary execution.EvaluationTime) {
	t.Helper()
	var timeline map[string]any
	if err := json.Unmarshal(fixture.timelineBytes(t, group), &timeline); err != nil {
		t.Fatal(err)
	}
	segments := timeline["segments"].([]any)
	segments[len(segments)-1].(map[string]any)["schedule"].(map[string]any)["Segment"].(map[string]any)["End"] = float64(boundary)
	timeline["retired_at"] = float64(boundary)
	timeline["record_revision"] = timeline["record_revision"].(float64) + 1
	encoded, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group), encoded, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

// A head body is read back whole: readers of the head get no records, the
// Control Leader gets the records its open Segments carry, a worker is
// answered from the timeline, and the next publication cuts over from it
// and writes the body this build writes.
func TestAHeadBodyIsReadBackFromTheOpenSegments(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:activation-head")
	first := headCatalog(t, 80, "AB")
	fixture.publish(t, first, 60)
	before := fixture.activation(t)
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, asHead(before)); err != nil {
		t.Fatal(err)
	}

	head, err := fixture.repository.LoadActivationHead(fixture.ctx)
	if err != nil || head.Plans != nil || head.Current != before.Current || head.RecordRevision != before.RecordRevision ||
		head.ActiveQGSetRef != before.ActiveQGSetRef {
		t.Fatalf("head = (%+v, %v), want the same activation without records", head, err)
	}
	full := fixture.activation(t)
	if full.SchemaVersion != before.SchemaVersion || !reflect.DeepEqual(sortedRecords(full.Plans), sortedRecords(before.Plans)) {
		t.Fatalf("records read back from the open Segments:\n got=%+v\nwant=%+v", full.Plans, before.Plans)
	}

	// A worker without a revision hint is answered from the timeline.
	groupA := groupOf(t, first, "2")
	segment := fixture.openSegment(t, groupA.Identity, 60)
	contract := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: groupA.Identity, EvaluationTime: 60},
		SnapshotRevision: segment.Publication.SnapshotRevision, QueryRevision: segment.QueryRevision,
		ScheduleRevision: segment.ScheduleRevision, ScheduleSegmentStart: segment.Start, DuePlanSetDigest: "due-plans-v1",
	}
	want := recordsOf(before, groupA)
	answer, err := fixture.repository.LoadActivations(fixture.ctx, execution.PlanActivationRequest{
		Contract: contract, Plans: []execution.PlanKey{want[0].Fact.Key()}})
	if err != nil || len(answer.Facts) != 1 || !answer.Facts[0].Equal(want[0].Fact) {
		t.Fatalf("worker answer = (%+v, %v), want %+v", answer.Facts, err, want[0].Fact)
	}

	// The next publication cuts over from the head and writes a head
	// again; the records it wrote are the ones the leader reads back.
	second := headCatalog(t, 90, "AB")
	fixture.publish(t, second, 120)
	after := fixture.activation(t)
	raw, err := controlplane.ActivationBytesForTest(fixture.ctx, fixture.repository)
	if err != nil || !strings.Contains(string(raw), `"alarmd-control-activation-v3"`) || strings.Contains(string(raw), `"fact"`) ||
		len(after.Plans) != len(before.Plans) {
		t.Fatalf("after the next publication body=%s plans=%d (%v)", raw, len(after.Plans), err)
	}
	if cut := fixture.openSegment(t, groupA.Identity, 120); cut.Start != 120 {
		t.Fatalf("the edited Query Group was not cut: %+v", cut)
	}

	// A head that carries records is not a head.
	corrupt := asHead(after)
	corrupt.Plans = after.Plans
	corrupt.RecordRevision++
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, corrupt); err != nil {
		t.Fatal(err)
	}
	var corruptErr *controlplane.PersistedActivationCorruptError
	if _, err := fixture.repository.LoadActivationHead(fixture.ctx); !errors.As(err, &corruptErr) {
		t.Fatalf("a head with records read as %v, want corrupt", err)
	}
}

// unfinishedCutover stands in what a later build leaves when a cutover from
// first to second committed only its retired piece: B retired, the active
// set down to A, the head naming second with progress, A still running
// first's content and C not added.
func unfinishedCutover(t *testing.T, fixture *cutoverFixture) (first, second controlplane.PublishedSnapshot, state controlplane.ActivationState) {
	t.Helper()
	first = fixture.publish(t, headCatalog(t, 80, "AB"), 60)
	before := fixture.activation(t)
	*fixture.now = time.Unix(120, 0)
	var err error
	second, _, err = fixture.repository.PublishCatalog(fixture.ctx, headCatalog(t, 90, "AC"))
	if err != nil {
		t.Fatal(err)
	}
	groupB := groupOf(t, headCatalog(t, 80, "AB"), "3")
	groupA := groupOf(t, headCatalog(t, 80, "AB"), "2")
	fixture.retireTimeline(t, groupB.Identity, 120)
	ref, err := controlplane.PersistActiveQueryGroupSetForTest(fixture.ctx, fixture.repository, []execution.QueryGroupIdentity{groupA.Identity})
	if err != nil {
		t.Fatal(err)
	}
	state = asHead(before)
	state.RecordRevision++
	state.Current = second.Publication
	state.ActiveQGSetRef = ref
	state.Draining = append(append([]controlplane.DrainingQueryGroup(nil), before.Draining...),
		controlplane.DrainingQueryGroup{QueryGroup: groupB.Identity, RetiredBoundary: 120})
	state.CutoverProgress = &controlplane.CutoverProgress{From: first.Publication,
		Cursor:          controlplane.CutoverCursor{Class: controlplane.CutoverClassRetired, QueryGroup: groupB.Identity},
		ChangesetDigest: "changeset", ChangesetCount: 3, Remaining: 2}
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, state); err != nil {
		t.Fatal(err)
	}
	return first, second, state
}

// A cutover a later build left unfinished is finished on the first tick,
// with the source unchanged - not whenever the next publication comes, which
// would leave C not detecting for as long as the source stays still. Until
// then the running content is what each open Segment names, and the repair
// subcommand refuses to cut anything behind the cutover's back.
func TestAnUnfinishedCutoverIsFinishedOnTheFirstTick(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:activation-progress")
	first, second, state := unfinishedCutover(t, fixture)
	groupA := groupOf(t, headCatalog(t, 80, "AB"), "2")
	groupB := groupOf(t, headCatalog(t, 80, "AB"), "3")
	groupC := groupOf(t, headCatalog(t, 90, "AC"), "4")

	published, err := fixture.repository.LoadPublishedContent(fixture.ctx, second.Publication)
	if err != nil {
		t.Fatal(err)
	}
	running, err := fixture.repository.ApplyCutoverProgress(fixture.ctx, state, published.Groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[groupA.Identity].Digest != digestOf(t, groupA) {
		t.Fatalf("running content while unfinished = %+v, want only A on its first content %s", running, digestOf(t, groupA))
	}
	if _, err := controlplane.RepairOpenSegments(fixture.ctx, fixture.repository, controlplane.RepairOpenSegmentsOptions{}); !errors.Is(err, controlplane.ErrCutoverInProgress) {
		t.Fatalf("repair while a cutover is unfinished = %v, want ErrCutoverInProgress", err)
	}

	*fixture.now = time.Unix(180, 0)
	finished, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication)
	if err != nil {
		t.Fatalf("finishing the cutover: %v", err)
	}
	if finished.CutoverProgress != nil || finished.Current != second.Publication || finished.RecordRevision != state.RecordRevision+1 {
		t.Fatalf("finished activation = %+v", finished)
	}
	if cut := fixture.openSegment(t, groupA.Identity, 180); cut.Start != 180 || cut.Publication.SnapshotRevision != second.Publication.SnapshotRevision {
		t.Fatalf("A was not cut to the second publication: %+v", cut)
	}
	if added := fixture.openSegment(t, groupC.Identity, 180); added.Publication.SnapshotRevision != second.Publication.SnapshotRevision {
		t.Fatalf("C was not added: %+v", added)
	}
	var timelineB map[string]any
	if err := json.Unmarshal(fixture.timelineBytes(t, groupB.Identity), &timelineB); err != nil || timelineB["retired_at"] != float64(120) {
		t.Fatalf("B's retirement was rewritten: %+v (%v)", timelineB["retired_at"], err)
	}
	after := fixture.activation(t)
	if len(recordsOf(after, groupB)) != 0 || len(recordsOf(after, groupA)) != len(groupA.Plans) || len(recordsOf(after, groupC)) != len(groupC.Plans) {
		t.Fatalf("records after finishing = %+v", after.Plans)
	}
	raw, err := controlplane.ActivationBytesForTest(fixture.ctx, fixture.repository)
	if err != nil || strings.Contains(string(raw), "cutover_progress") || !strings.Contains(string(raw), `"alarmd-control-activation-v3"`) {
		t.Fatalf("finished body = %s (%v), want a head without progress", raw, err)
	}
	// Finished is finished: the next tick has nothing to do.
	again, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication)
	if err != nil || again.RecordRevision != finished.RecordRevision {
		t.Fatalf("the tick after finishing wrote again: (%+v, %v)", again, err)
	}
	_ = first
}

// Only finishing a cutover left in progress may write an activation naming
// the publication it already names. Without progress on the previous one,
// or with progress kept on the next, it must move to a new publication.
func TestOnlyFinishingAnUnfinishedCutoverMayKeepItsPublication(t *testing.T) {
	for _, test := range []struct {
		name     string
		previous bool // the previous activation carries progress
		next     bool // the next one keeps it
	}{
		{name: "no progress to finish", previous: false, next: false},
		{name: "progress kept", previous: true, next: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCutoverFixture(t, "alarmd:control:activation-finish-guard")
			_, second, state := unfinishedCutover(t, fixture)
			if !test.previous {
				state.CutoverProgress = nil
				if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, state); err != nil {
					t.Fatal(err)
				}
			}
			previous := fixture.activation(t)
			next := previous
			next.RecordRevision++
			if !test.next {
				next.CutoverProgress = nil
			}
			err := fixture.repository.CompareAndSetPublicationScheduleActivation(fixture.ctx, controlplane.ActivationExpectation{
				RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending,
			}, next, 180, &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}})
			if !errors.Is(err, controlplane.ErrCutoverRequest) || next.Current != second.Publication {
				t.Fatalf("a write naming the same publication = %v, want ErrCutoverRequest", err)
			}
		})
	}
}

// afterAddedPiece stands in what a later build leaves when a cutover from
// first to second committed its retired piece and its added piece but not
// its changed one: B retired, C's timeline written - and, since the active
// set is written only after a class's last piece, C not in the set yet - A
// still on first's content. Built by running the real cutover and putting
// A's timeline and the set back.
func afterAddedPiece(t *testing.T, fixture *cutoverFixture) (second controlplane.PublishedSnapshot, state controlplane.ActivationState) {
	t.Helper()
	first := fixture.publish(t, headCatalog(t, 80, "AB"), 60)
	before := fixture.activation(t)
	groupA := groupOf(t, headCatalog(t, 80, "AB"), "2")
	groupB := groupOf(t, headCatalog(t, 80, "AB"), "3")
	groupC := groupOf(t, headCatalog(t, 90, "AC"), "4")
	timelineA := fixture.timelineBytes(t, groupA.Identity)
	second = fixture.publish(t, headCatalog(t, 90, "AC"), 120)
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(groupA.Identity), timelineA, 0).Err(); err != nil {
		t.Fatal(err)
	}
	ref, err := controlplane.PersistActiveQueryGroupSetForTest(fixture.ctx, fixture.repository, []execution.QueryGroupIdentity{groupA.Identity})
	if err != nil {
		t.Fatal(err)
	}
	state = asHead(before)
	state.RecordRevision = fixture.activation(t).RecordRevision + 1
	state.Current = second.Publication
	state.ActiveQGSetRef = ref
	state.Draining = append(append([]controlplane.DrainingQueryGroup(nil), before.Draining...),
		controlplane.DrainingQueryGroup{QueryGroup: groupB.Identity, RetiredBoundary: 120})
	state.CutoverProgress = &controlplane.CutoverProgress{From: first.Publication,
		Cursor:          controlplane.CutoverCursor{Class: controlplane.CutoverClassAdded, QueryGroup: groupC.Identity},
		ChangesetDigest: "changeset", ChangesetCount: 3, Remaining: 1}
	if err := controlplane.WriteActivationForTest(fixture.ctx, fixture.repository, state); err != nil {
		t.Fatal(err)
	}
	return second, state
}

// A Query Group an earlier piece added has its timeline and is not in the
// active set yet. Finishing reads it as running, not as one to add - adding
// it would meet its own timeline and be refused on every tick - so the
// cutover finishes, C's timeline untouched and its records in the body.
func TestAnAddedPieceIsReadAsRunningWhenTheCutoverIsFinished(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:activation-added-piece")
	second, state := afterAddedPiece(t, fixture)
	groupA := groupOf(t, headCatalog(t, 80, "AB"), "2")
	groupC := groupOf(t, headCatalog(t, 90, "AC"), "4")
	timelineC := fixture.timelineBytes(t, groupC.Identity)

	*fixture.now = time.Unix(180, 0)
	finished, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication)
	if err != nil {
		t.Fatalf("finishing after an added piece: %v", err)
	}
	if finished.CutoverProgress != nil || finished.RecordRevision != state.RecordRevision+1 {
		t.Fatalf("finished activation = %+v", finished)
	}
	if cut := fixture.openSegment(t, groupA.Identity, 180); cut.Start != 180 {
		t.Fatalf("A was not cut: %+v", cut)
	}
	if got := fixture.timelineBytes(t, groupC.Identity); !reflect.DeepEqual(got, timelineC) {
		t.Fatal("C, added by an earlier piece, was written again")
	}
	if records := recordsOf(fixture.activation(t), groupC); len(records) != len(groupC.Plans) {
		t.Fatalf("C's records after finishing = %+v", records)
	}
}

// A timeline that does not decode is refused by name while a cutover is in
// progress, not taken for a Query Group that runs nothing: that would pass
// it for one to add, which its own key refuses on every tick, and drop its
// records from the body. Deleting the key is the way out: the next tick
// opens it again.
func TestAnUndecodableTimelineIsRefusedByNameWhileACutoverIsInProgress(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:activation-undecodable")
	second, _ := afterAddedPiece(t, fixture)
	groupA := groupOf(t, headCatalog(t, 80, "AB"), "2")
	key := fixture.prefix + ":schedule_timeline:" + string(groupA.Identity)
	if err := fixture.client.Set(fixture.ctx, key, "not-a-timeline", 0).Err(); err != nil {
		t.Fatal(err)
	}
	*fixture.now = time.Unix(180, 0)
	var corrupt *controlplane.DeterministicScheduleError
	if _, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication); !errors.As(err, &corrupt) || !strings.Contains(err.Error(), string(groupA.Identity)) {
		t.Fatalf("finishing over an undecodable timeline = %v, want a corrupt error naming %s", err, groupA.Identity)
	}
	if err := fixture.client.Del(fixture.ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	finished, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication)
	if err != nil || finished.CutoverProgress != nil {
		t.Fatalf("after deleting the key = (%+v, %v), want the cutover finished", finished, err)
	}
	if opened := fixture.openSegment(t, groupA.Identity, 180); opened.Start != 180 {
		t.Fatalf("A was not opened again: %+v", opened)
	}
}
