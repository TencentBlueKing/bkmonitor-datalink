// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Pools of 1,000 bytes: the byte constraint allows 800 of each.
const projectionPool = 1000

func projectionWorkers(ids ...string) []ownership.WorkerRegistration {
	workers := make([]ownership.WorkerRegistration, 0, len(ids))
	for _, id := range ids {
		workers = append(workers, rolloutWorker(id, "cap-new"))
	}
	return workers
}

func projectionReadings(peaks map[execution.QueryGroupIdentity]uint64, workers ...string) ByteReadings {
	readings := ByteReadings{Pool: map[string]uint64{}, Peak: peaks}
	for _, worker := range workers {
		readings.Pool[worker] = projectionPool
	}
	return readings
}

// preferred is the Worker rendezvous places the Query Group on, and the
// other one: the cases name them by the routing's own answer rather than by
// a hash worked out by hand.
func preferred(t *testing.T, router *Router, queryGroup execution.QueryGroupIdentity, workers []ownership.WorkerRegistration) (string, string) {
	t.Helper()
	selected, err := router.Select(queryGroup, workers, rolloutNow)
	if err != nil {
		t.Fatal(err)
	}
	if selected.WorkerID == workers[0].WorkerID {
		return workers[0].WorkerID, workers[1].WorkerID
	}
	return workers[1].WorkerID, workers[0].WorkerID
}

// A placement goes to the highest rendezvous score the Query Group fits on.
// An unread Query Group counts at the round's estimate - the ninetieth
// percentile of the peaks read - both the one placed and those already on
// a Worker.
func TestAPlacementGoesToTheHighestScoreItFitsOn(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("w-1", "w-2")
	top, other := preferred(t, router, "qg-x", workers)
	owners := map[execution.QueryGroupIdentity]string{"qg-top": top, "qg-other": other}
	// The estimate is 700: qg-x at 700 does not fit beside qg-top's 700 on
	// the preferred Worker (1,400 > 800), and fits beside qg-other's 100.
	readings := projectionReadings(map[execution.QueryGroupIdentity]uint64{"qg-top": 700, "qg-other": 100}, "w-1", "w-2")
	selected, unfit, err := router.selectFitting("qg-x", workers, rolloutNow, newByteProjection(owners, readings))
	if err != nil {
		t.Fatal(err)
	}
	if selected.WorkerID != other || unfit {
		t.Fatalf("placed on %s (unfit %v), want %s: the preferred Worker cannot take it", selected.WorkerID, unfit, other)
	}
	// Without the projection it goes where rendezvous says (the reverse:
	// a placement blind to bytes is what put Query Groups past the share).
	if blind, _, _ := router.selectFitting("qg-x", workers, rolloutNow, nil); blind.WorkerID != top {
		t.Fatalf("placed without a projection on %s, want the rendezvous choice %s", blind.WorkerID, top)
	}
}

// A Query Group that fits no Worker is still placed - an unknown fit is no
// reason to leave it unplaced - on the rendezvous choice, reported unfit.
func TestAPlacementThatFitsNowhereGoesToTheRendezvousChoiceReportedUnfit(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("w-1", "w-2")
	top, other := preferred(t, router, "qg-x", workers)
	owners := map[execution.QueryGroupIdentity]string{"qg-top": top, "qg-other": other}
	readings := projectionReadings(map[execution.QueryGroupIdentity]uint64{"qg-top": 700, "qg-other": 700}, "w-1", "w-2")
	selected, unfit, err := router.selectFitting("qg-x", workers, rolloutNow, newByteProjection(owners, readings))
	if err != nil {
		t.Fatal(err)
	}
	if selected.WorkerID != top || !unfit {
		t.Fatalf("placed on %s (unfit %v), want %s reported unfit", selected.WorkerID, unfit, top)
	}
}

// With no peak read this round there is no estimate, and nothing is judged:
// an unknown counts as nothing, as before the estimate existed, so the
// start of a term places by rendezvous and reports nothing unfit.
func TestWithNoEstimateAPlacementJudgesNothing(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("w-1", "w-2")
	top, other := preferred(t, router, "qg-x", workers)
	owners := map[execution.QueryGroupIdentity]string{"qg-top": top, "qg-other": other}
	readings := projectionReadings(map[execution.QueryGroupIdentity]uint64{}, "w-1", "w-2")
	selected, unfit, err := router.selectFitting("qg-x", workers, rolloutNow, newByteProjection(owners, readings))
	if err != nil {
		t.Fatal(err)
	}
	if selected.WorkerID != top || unfit {
		t.Fatalf("placed on %s (unfit %v) with no estimate, want %s and nothing unfit", selected.WorkerID, unfit, top)
	}
}

// The count correction judges its destination on the same projection: four
// unread Query Groups on it count at the estimate (150 each), putting it at
// 900 of 800, and nothing moves. Counted at nothing - the rule before - it
// took a Query Group, and the byte constraint moved Query Groups back off.
func TestTheCountCorrectionCountsAnUnreadQueryGroupAtTheEstimate(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("most", "least")
	owners := map[execution.QueryGroupIdentity]string{}
	peaks := map[execution.QueryGroupIdentity]uint64{}
	for index := 0; index < 10; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("m-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "most", 50
	}
	for index := 0; index < 2; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("l-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "least", 150
	}
	for index := 0; index < 4; index++ {
		owners[execution.QueryGroupIdentity(fmt.Sprintf("u-%02d", index))] = "least"
	}
	// Read peaks: ten of 50 and two of 150; the estimate is the ninetieth
	// percentile, 150. least holds 300 read and four unread: 300 + 600.
	plan := router.PlanRebalanceWithBytes(owners, workers, projectionReadings(peaks, "most", "least"), rolloutNow)
	if len(plan.Moves) != 0 {
		t.Fatalf("moves = %+v onto a destination projected past its share, want none", plan.Moves)
	}
	// The same fleet with the unread Query Groups read low moves one.
	for index := 0; index < 4; index++ {
		peaks[execution.QueryGroupIdentity(fmt.Sprintf("u-%02d", index))] = 10
	}
	if plan := router.PlanRebalanceWithBytes(owners, workers, projectionReadings(peaks, "most", "least"), rolloutNow); len(plan.Moves) != 1 {
		t.Fatalf("moves = %+v once the destination is read with room, want one", plan.Moves)
	}
}

// Whether a Worker is overloaded is judged on what it reported, not on the
// estimate: moving a Query Group off takes evidence, and an estimate that
// says "over" would move Query Groups for nothing.
func TestOverloadIsJudgedOnWhatWasRead(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("w-1", "w-2")
	owners := map[execution.QueryGroupIdentity]string{"read": "w-1", "elsewhere": "w-2"}
	peaks := map[execution.QueryGroupIdentity]uint64{"read": 700, "elsewhere": 700}
	for index := 0; index < 3; index++ {
		owners[execution.QueryGroupIdentity(fmt.Sprintf("u-%02d", index))] = "w-1"
	}
	// w-1 reads 700 of 800; its three unread at the estimate (700) would
	// put it far past.
	plan := router.PlanByteMoves(owners, workers, projectionReadings(peaks, "w-1", "w-2"), rolloutNow)
	if len(plan.Overloaded) != 0 || len(plan.Moves) != 0 {
		t.Fatalf("overloaded = %v, moves = %+v; want none on a read sum within the share", plan.Overloaded, plan.Moves)
	}
}

// A round's placements are judged under the projection and the round says
// how many fit nowhere.
func TestARoundCountsThePlacementsThatFitNowhere(t *testing.T) {
	workers := projectionWorkers("w-1", "w-2")
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"full-1": {QueryGroup: "full-1", DesiredWorkerID: "w-1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow.Add(-time.Hour)},
		"full-2": {QueryGroup: "full-2", DesiredWorkerID: "w-2", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow.Add(-time.Hour)},
	}
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	readings := func(owners map[execution.QueryGroupIdentity]string) ByteReadings {
		return projectionReadings(map[execution.QueryGroupIdentity]uint64{"full-1": 700, "full-2": 700}, "w-1", "w-2")
	}
	settlement, err := reconciler.ReconcileRoundSettlingWithBytes(context.Background(), ownership.PublicationAuthority{},
		[]execution.QueryGroupIdentity{"full-1", "full-2", "qg-new"}, workers, rolloutNow, ContentScopes{}, readings)
	if err != nil {
		t.Fatal(err)
	}
	if _, placed := settlement.Records["qg-new"]; !placed || settlement.Unfit != 1 {
		t.Fatalf("qg-new placed %v, unfit %d; want it placed and counted unfit", placed, settlement.Unfit)
	}
	// Without readings nothing is judged and nothing is unfit.
	store = &countingAssignmentStore{workers: workers, records: records}
	reconciler = newRolloutReconciler(t, store)
	settlement, err = reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{},
		[]execution.QueryGroupIdentity{"full-1", "full-2", "qg-new"}, workers, rolloutNow, ContentScopes{})
	if err != nil || settlement.Unfit != 0 {
		t.Fatalf("a round with no readings: unfit %d, err %v; want 0", settlement.Unfit, err)
	}
}

// A round that places two Query Groups judges the second on the first: the
// first takes the room left on the Worker it fits, so the second does not
// fit there too and is reported unfit.
func TestARoundJudgesEachPlacementOnThePlacementsBeforeIt(t *testing.T) {
	workers := projectionWorkers("w-1", "w-2")
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"held-1": {QueryGroup: "held-1", DesiredWorkerID: "w-1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow.Add(-time.Hour)},
		"held-2": {QueryGroup: "held-2", DesiredWorkerID: "w-2", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow.Add(-time.Hour)},
	}
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	// w-1 holds 700 of 800, w-2 holds 100; the estimate is 700. The first
	// new Query Group fits only on w-2 (100 + 700); after it, neither fits.
	readings := func(map[execution.QueryGroupIdentity]string) ByteReadings {
		return projectionReadings(map[execution.QueryGroupIdentity]uint64{"held-1": 700, "held-2": 100}, "w-1", "w-2")
	}
	settlement, err := reconciler.ReconcileRoundSettlingWithBytes(context.Background(), ownership.PublicationAuthority{},
		[]execution.QueryGroupIdentity{"held-1", "held-2", "new-1", "new-2"}, workers, rolloutNow, ContentScopes{}, readings)
	if err != nil {
		t.Fatal(err)
	}
	onTwo := 0
	for _, queryGroup := range []execution.QueryGroupIdentity{"new-1", "new-2"} {
		if settlement.Records[queryGroup].DesiredWorkerID == "w-2" {
			onTwo++
		}
	}
	if settlement.Unfit != 1 {
		t.Fatalf("unfit = %d, want 1: the second placement does not fit once the first took w-2's room (new Query Groups on w-2: %d)",
			settlement.Unfit, onTwo)
	}
}

// The count correction judges each of its moves on the ones before: two
// moves are allowed, the destination has room for one.
func TestTheCountCorrectionJudgesEachMoveOnTheMovesBeforeIt(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("most", "least")
	owners := map[execution.QueryGroupIdentity]string{}
	peaks := map[execution.QueryGroupIdentity]uint64{}
	for index := 0; index < 30; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("m-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "most", 300
	}
	for index := 0; index < 2; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("l-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "least", 150
	}
	// 32 assigned: a batch of two. least holds 300; one move of 300 brings
	// it to 600, a second to 900 of 800.
	plan := router.PlanRebalanceWithBytes(owners, workers, projectionReadings(peaks, "most", "least"), rolloutNow)
	if plan.Batch != 2 || len(plan.Moves) != 1 {
		t.Fatalf("batch %d, moves %+v; want a batch of two and one move", plan.Batch, plan.Moves)
	}
}

// The estimate is drawn from the peaks of Workers whose pool is known, as
// the byte constraint draws it: a Worker that registered no pool, holding
// very large Query Groups, does not make every unread Query Group look large.
func TestTheEstimateIsDrawnFromJudgedWorkersOnly(t *testing.T) {
	router := NewRouter(nil)
	workers := projectionWorkers("w-1", "w-2")
	top, other := preferred(t, router, "qg-x", workers)
	owners := map[execution.QueryGroupIdentity]string{"qg-top": top, "qg-other": other}
	peaks := map[execution.QueryGroupIdentity]uint64{"qg-top": 100, "qg-other": 100}
	for index := 0; index < 20; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("huge-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "no-pool", 5000
	}
	selected, unfit, err := router.selectFitting("qg-x", workers, rolloutNow, newByteProjection(owners, projectionReadings(peaks, "w-1", "w-2")))
	if err != nil {
		t.Fatal(err)
	}
	if selected.WorkerID != top || unfit {
		t.Fatalf("placed on %s (unfit %v), want %s: the estimate is 100, from the judged Workers", selected.WorkerID, unfit, top)
	}
}
