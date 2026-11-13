// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// closedSegmentFixture publishes catalogs through a real reconciler, one
// cutover a minute from 60 on, and answers activation requests for Slots of
// the Segments those cutovers closed - on the hinted path and on the header
// path, which must agree.
type closedSegmentFixture struct {
	t          *testing.T
	ctx        context.Context
	repository *controlplane.RedisCatalogRepository
	runtime    *controlplane.RedisCatalogRuntime
	reconciler *controlplane.ScheduleActivationReconciler
	hook       *controlReadCountingHook
	now        int64
}

func newClosedSegmentFixture(t *testing.T, prefix string) *closedSegmentFixture {
	t.Helper()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &closedSegmentFixture{t: t, ctx: context.Background(), repository: repository, runtime: runtime,
		hook: newControlReadCountingHook(), now: 0}
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time {
		return time.Unix(fixture.now, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.reconciler = reconciler
	client.AddHook(fixture.hook)
	return fixture
}

// publish activates the catalog a minute after the previous one.
func (fixture *closedSegmentFixture) publish(catalog controlplane.Catalog) controlplane.ActivationState {
	fixture.t.Helper()
	fixture.now += 60
	snapshot, _, err := fixture.repository.PublishCatalog(fixture.ctx, catalog)
	if err != nil {
		fixture.t.Fatal(err)
	}
	state, err := fixture.reconciler.Ensure(fixture.ctx, snapshot.Publication)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return state
}

// contractAt is the contract a Slot at the given time names: the Segment the
// persisted timeline holds it in.
func (fixture *closedSegmentFixture) contractAt(group execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.FrozenExecutionContractRef, bool) {
	fixture.t.Helper()
	schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, group, at)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: group, EvaluationTime: at},
		SnapshotRevision: schedule.Segment.Publication.SnapshotRevision, QueryRevision: schedule.Segment.QueryRevision,
		ScheduleRevision: schedule.Segment.ScheduleRevision, ScheduleSegmentStart: schedule.Segment.Start,
		DuePlanSetDigest: "due-plans-v1",
	}, schedule.Segment.End != nil
}

// openStart is where the Query Group's open Segment starts: a Slot time in
// it, read back from the timeline rather than assumed from the clock.
func (fixture *closedSegmentFixture) openStart(group execution.QueryGroupIdentity) execution.EvaluationTime {
	fixture.t.Helper()
	schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, group, execution.EvaluationTime(fixture.now+3600))
	if err != nil {
		fixture.t.Fatal(err)
	}
	if schedule.Segment.End != nil {
		fixture.t.Fatalf("the Segment holding the future is closed: %+v", schedule.Segment)
	}
	return schedule.Segment.Start
}

// answer is the activation of each strategy's Plan for the contract, asked on
// both paths: the hinted one must read no header and agree with the header
// one.
func (fixture *closedSegmentFixture) answer(contract execution.FrozenExecutionContractRef, plans map[string]execution.PlanIdentity) map[string]execution.PlanActivationFact {
	fixture.t.Helper()
	request := execution.PlanActivationRequest{Contract: contract}
	names := make([]string, 0, len(plans))
	for name, plan := range plans {
		names = append(names, name)
		request.Plans = append(request.Plans, execution.PlanKey{PlanIdentity: plan})
	}
	byHeader, err := fixture.repository.LoadActivations(fixture.ctx, request)
	if err != nil {
		fixture.t.Fatal(err)
	}
	revision, err := fixture.repository.TimelineRecordRevision(fixture.ctx, contract.Slot.QueryGroup)
	if err != nil || revision == 0 {
		fixture.t.Fatalf("timeline revision %d (%v)", revision, err)
	}
	fixture.hook.reset()
	byTimeline, err := fixture.repository.LoadActivations(controlplane.WithTimelineRevisionHint(fixture.ctx, revision), request)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if headers := fixture.hook.count("get", "header"); headers != 0 {
		fixture.t.Fatalf("the hinted request read the header %d times: it fell back, and the hinted path went untested", headers)
	}
	if !reflect.DeepEqual(byTimeline, byHeader) {
		fixture.t.Fatalf("hinted activations %+v, header activations %+v: one answer, two implementations", byTimeline.Facts, byHeader.Facts)
	}
	facts := make(map[string]execution.PlanActivationFact, len(names))
	for index, name := range names {
		facts[name] = byTimeline.Facts[index]
	}
	return facts
}

func planIdentityOf(t *testing.T, catalog controlplane.Catalog, strategyID string) execution.PlanIdentity {
	t.Helper()
	for _, plan := range catalog.QueryGroups[0].Plans {
		if plan.Identity.StrategyID == strategyID {
			return plan.Identity
		}
	}
	t.Fatalf("no Plan of strategy %s in %+v", strategyID, catalog.QueryGroups[0].Plans)
	return execution.PlanIdentity{}
}

func activationRecordFact(t *testing.T, state controlplane.ActivationState, strategyID string) execution.PlanActivationFact {
	t.Helper()
	return activationRecordByStrategy(t, state, strategyID).Fact
}

// An edit that moves the Plan's state generation closes the Segment the Slot
// in flight falls in. The Plan stayed: that Slot is answered with the record
// it ran under - the old generation and epoch, as the Segment held them - on
// both paths, not ActivationNone, which threw it away as PLAN_NOT_ACTIVE.
func TestAClosedSegmentAnswersAPlanThatStayedWithTheRecordItRanUnder(t *testing.T) {
	fixture := newClosedSegmentFixture(t, "alarmd:control:closed-segment-edit")
	before := validCatalog(t, 80)
	group := before.QueryGroups[0].Identity
	plan := planIdentityOf(t, before, "1001")
	initial := fixture.publish(before)
	slot := fixture.openStart(group)
	edited := validCatalog(t, 90)
	if edited.QueryGroups[0].Identity != group {
		t.Fatal("the threshold edit changed the Query Group, so it does not close a Segment of this timeline")
	}
	after := fixture.publish(edited)
	if activationRecordFact(t, after, "1001").Selected.StateGeneration == activationRecordFact(t, initial, "1001").Selected.StateGeneration {
		t.Fatal("the threshold edit did not move the state generation")
	}
	contract, closed := fixture.contractAt(group, slot)
	if !closed {
		t.Fatal("the edit did not close the Segment the Slot falls in")
	}
	got := fixture.answer(contract, map[string]execution.PlanIdentity{"plan": plan})["plan"]
	if !reflect.DeepEqual(got, activationRecordFact(t, initial, "1001")) {
		t.Fatalf("the closed Segment answered %+v, want the record the Slot ran under %+v", got, activationRecordFact(t, initial, "1001"))
	}
}

// Adding a strategy to a Query Group, or deleting one from it, closes the
// Segment for every Plan of the group. The Plan that stayed keeps its record
// for the closed Segment's Slots; the one added was not in that Segment and
// the one deleted is in no Segment after it, so both are ActivationNone.
func TestASharedQueryGroupsClosedSegmentAnswersOnlyThePlansThatStayed(t *testing.T) {
	fixture := newClosedSegmentFixture(t, "alarmd:control:closed-segment-shared")
	onlyA := sharedQueryGroupCatalog(t, true, false)
	group := onlyA.QueryGroups[0].Identity
	both := sharedQueryGroupCatalog(t, true, true)
	plans := map[string]execution.PlanIdentity{"A": planIdentityOf(t, both, "1001"), "B": planIdentityOf(t, both, "1002")}
	first := fixture.publish(onlyA)
	firstSlot := fixture.openStart(group)
	second := fixture.publish(both)
	// B is new to the second activation, and forced to warm up as one the
	// Worker may find never ran; A stayed, and is neither.
	if b, a := activationRecordFact(t, second, "1002").Selected, activationRecordFact(t, second, "1001").Selected; !b.Arrived || !b.ForceWarming || a.Arrived {
		t.Fatalf("B arrived=%t warming=%t, A arrived=%t; want only B new to the activation", b.Arrived, b.ForceWarming, a.Arrived)
	}
	secondSlot := fixture.openStart(group)
	added, closed := fixture.contractAt(group, firstSlot)
	if !closed {
		t.Fatal("adding B did not close the Segment A ran in")
	}
	got := fixture.answer(added, plans)
	if !reflect.DeepEqual(got["A"], activationRecordFact(t, first, "1001")) || got["B"].Selection != execution.ActivationNone {
		t.Fatalf("after adding B the closed Segment answered A %+v and B %+v; want A's own record and B none", got["A"], got["B"])
	}
	fixture.publish(sharedQueryGroupCatalog(t, false, true))
	deleted, closed := fixture.contractAt(group, secondSlot)
	if !closed {
		t.Fatal("deleting A did not close the Segment A and B ran in")
	}
	got = fixture.answer(deleted, plans)
	if got["A"].Selection != execution.ActivationNone || !reflect.DeepEqual(got["B"], activationRecordFact(t, second, "1002")) {
		t.Fatalf("after deleting A the closed Segment answered A %+v and B %+v; want A none and B's own record", got["A"], got["B"])
	}
}

// A Plan that left and came back is not one that stayed: a Segment between
// does not carry it. The Slots of the Segment before it left stay
// ActivationNone - replayed, they would raise alerts the absent close already
// closed, under an epoch nothing recovers any more - while the Segment it
// came back in, once closed, still answers the record it came back with.
func TestAPlanThatLeftAndCameBackIsNotAnsweredForTheSegmentBeforeItLeft(t *testing.T) {
	fixture := newClosedSegmentFixture(t, "alarmd:control:closed-segment-returned")
	both := sharedQueryGroupCatalog(t, true, true)
	group := both.QueryGroups[0].Identity
	plans := map[string]execution.PlanIdentity{"A": planIdentityOf(t, both, "1001"), "B": planIdentityOf(t, both, "1002")}
	first := fixture.publish(both)
	firstSlot := fixture.openStart(group)
	fixture.publish(sharedQueryGroupCatalog(t, false, true))
	returned := fixture.publish(both)
	backSlot := fixture.openStart(group)
	beforeLeaving, closed := fixture.contractAt(group, firstSlot)
	if !closed {
		t.Fatal("the Segment before A left is not closed")
	}
	got := fixture.answer(beforeLeaving, plans)
	if got["A"].Selection != execution.ActivationNone || !reflect.DeepEqual(got["B"], activationRecordFact(t, first, "1002")) {
		t.Fatalf("the Segment before A left answered A %+v and B %+v; want A none and B's own record", got["A"], got["B"])
	}
	backIn, open := fixture.contractAt(group, backSlot)
	if open {
		t.Fatal("the Segment A came back in is already closed")
	}
	whileOpen := fixture.answer(backIn, plans)["A"]
	if !reflect.DeepEqual(whileOpen, activationRecordFact(t, returned, "1001")) || !whileOpen.Selected.ForceWarming {
		t.Fatalf("A came back as %+v, want its returned record, forced to warm", whileOpen)
	}
	fixture.publish(catalogWithSchedule(t, both, 60, 30))
	if _, closed := fixture.contractAt(group, backSlot); !closed {
		t.Fatal("the schedule change did not close the Segment A came back in")
	}
	if afterClose := fixture.answer(backIn, plans)["A"]; !reflect.DeepEqual(afterClose, whileOpen) {
		t.Fatalf("closed, the Segment A came back in answered %+v; open it answered %+v", afterClose, whileOpen)
	}
}

// A Query Group no Plan is left in retires: the cutover closes its last
// Segment and opens none. Its remaining Slots are the draining backlog, and
// every Plan of them is ActivationNone. Here the Plan left by an edit to its
// query, which names another Query Group: the in-flight Slot of the old one
// is still lost, the boundary of the closed-Segment answer (the Plan is in
// no later Segment of this timeline), as it is when a deletion retires it.
func TestARetiredQueryGroupsLastSegmentAnswersNoPlan(t *testing.T) {
	fixture := newClosedSegmentFixture(t, "alarmd:control:closed-segment-retired")
	catalog := validCatalog(t, 80)
	group := catalog.QueryGroups[0].Identity
	fixture.publish(catalog)
	slot := fixture.openStart(group)
	facts := queryFacts(t)
	facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
	facts.QueryList[0].TimeAggregation.Method = "max"
	facts.QueryRevision = ""
	moved, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: realThresholdDocuments(t)[0],
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: moved},
	})
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere.QueryGroups[0].Identity == group {
		t.Fatal("the query edit did not move the Plan to another Query Group")
	}
	fixture.publish(elsewhere)
	contract, closed := fixture.contractAt(group, slot)
	if !closed {
		t.Fatal("retiring the Query Group did not close its last Segment")
	}
	if got := fixture.answer(contract, map[string]execution.PlanIdentity{"plan": planIdentityOf(t, catalog, "1001")})["plan"]; got.Selection != execution.ActivationNone {
		t.Fatalf("the retired Query Group's last Segment answered %+v, want ActivationNone", got)
	}
}
