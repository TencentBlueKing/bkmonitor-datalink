// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func declining(worker ownership.WorkerRegistration, stage string, queryGroups ...string) ownership.WorkerRegistration {
	for _, queryGroup := range queryGroups {
		worker.Declined = append(worker.Declined, ownership.DeclinedQueryGroup{QueryGroup: queryGroup, Stage: stage})
	}
	return worker
}

func newDeclineReconciler(t *testing.T, store AssignmentStore) *Reconciler {
	t.Helper()
	reconciler, err := NewReconciler(NewRouter(nil), store)
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

// A worker that declines a Query Group - an execution of it hung there and it
// let the lease go (design 02 section 6.5) - is not its holder any more: the
// Query Group is placed on another worker in the same round, outside the
// handover batch, and the decliner is never chosen for it while it declines,
// not even by the count correction.
func TestADeclinedQueryGroupIsPlacedElsewhereAtOnceAndNeverBack(t *testing.T) {
	queryGroups, records := rolloutFleet(40) // alternately old-a and old-b
	workers := []ownership.WorkerRegistration{
		declining(rolloutWorker("old-a", "cap"), execution.SlotStageQuery, "qg-000", "qg-002"),
		rolloutWorker("old-b", "cap"),
	}
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newDeclineReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	for _, declined := range []execution.QueryGroupIdentity{"qg-000", "qg-002"} {
		if got := settlement.Records[declined].DesiredWorkerID; got != "old-b" {
			t.Fatalf("%s is on %s after its holder declined it, want old-b", declined, got)
		}
	}
	if settlement.Replaced != 0 || settlement.Deferred != 0 {
		t.Fatalf("settlement = replaced %d deferred %d, want a declined Query Group moved as a gone holder's, outside the batch",
			settlement.Replaced, settlement.Deferred)
	}
	if got := settlement.Records["qg-004"].DesiredWorkerID; got != "old-a" {
		t.Fatalf("qg-004, not declined, moved to %s; want it left on old-a", got)
	}
	// old-a now holds 18 and old-b 22; the correction would move toward
	// old-a, and must not move a Query Group old-a declines.
	owners := map[execution.QueryGroupIdentity]string{}
	for queryGroup, record := range settlement.Records {
		owners[queryGroup] = record.DesiredWorkerID
	}
	plan := reconciler.PlanRebalanceWithin(owners, workers, ByteReadings{}, rolloutNow, -1)
	for _, move := range plan.Moves {
		if move.To == "old-a" && (move.QueryGroup == "qg-000" || move.QueryGroup == "qg-002") {
			t.Fatalf("the count correction moved %s back to old-a, which declines it", move.QueryGroup)
		}
	}
	if _, err := reconciler.router.Select("qg-000", workers, rolloutNow); err != nil {
		t.Fatal(err)
	}
	if selected, _ := reconciler.router.Select("qg-000", workers, rolloutNow); selected.WorkerID == "old-a" {
		t.Fatal("Select chose old-a for a Query Group it declines")
	}
}

// A Query Group whose execution hangs wherever it runs walks the fleet: each
// worker declines it in turn. Once every worker that could take it declines
// it, it is unplaceable by that name, with how many workers it walked and the
// stage each hung in - the Query Group's own bug, not any worker's.
func TestAQueryGroupDeclinedByEveryWorkerInTurnIsUnplaceableByName(t *testing.T) {
	queryGroups := []execution.QueryGroupIdentity{"qg-poison", "qg-fine"}
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-poison": {QueryGroup: "qg-poison", DesiredWorkerID: "w1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow},
		"qg-fine": {QueryGroup: "qg-fine", DesiredWorkerID: "w1", AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow},
	}
	// Round one: it hung on w1, which declines it; it goes to w2.
	workers := []ownership.WorkerRegistration{
		declining(rolloutWorker("w1", "cap"), execution.SlotStageOutput, "qg-poison"), rolloutWorker("w2", "cap"),
	}
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newDeclineReconciler(t, store)
	first, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Records["qg-poison"].DesiredWorkerID; got != "w2" || first.DeclinedEverywhere != 0 {
		t.Fatalf("round one: qg-poison on %s, declined everywhere %d; want it moved to w2", got, first.DeclinedEverywhere)
	}
	// Round two: it hung on w2 as well.
	workers[1] = declining(rolloutWorker("w2", "cap"), execution.SlotStageOutput, "qg-poison")
	second, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatalf("round two: %v, want the round to go on", err)
	}
	if second.Unplaceable != 1 || second.DeclinedEverywhere != 1 || len(second.DeclinedEverywhereSample) != 1 {
		t.Fatalf("round two settlement = %+v, want one Query Group unplaceable as declined everywhere", second)
	}
	sample := second.DeclinedEverywhereSample[0]
	if sample.QueryGroup != "qg-poison" || sample.Hops != 2 || len(sample.Stages) != 2 ||
		sample.Stages[0] != (DeclinerStage{Worker: "w1", Stage: execution.SlotStageOutput}) ||
		sample.Stages[1] != (DeclinerStage{Worker: "w2", Stage: execution.SlotStageOutput}) {
		t.Fatalf("declined-everywhere sample = %+v, want qg-poison walked 2 workers, hung in output on each", sample)
	}
	if got := second.Records["qg-poison"].DesiredWorkerID; got != "w2" {
		t.Fatalf("qg-poison record after round two names %s, want it kept as it was", got)
	}
	if got := second.Records["qg-fine"].DesiredWorkerID; got != "w1" {
		t.Fatalf("qg-fine on %s, want it untouched by its neighbour's state", got)
	}
}
