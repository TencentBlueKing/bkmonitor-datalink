// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Design 02 §6.2: the capacity gate excludes only workers that are
// explicitly incompatible, a membership flap must not move everything, and
// handovers go in batches. A rollout that changes the capability digest
// leaves the old replicas, still alive and running their Query Groups,
// ineligible to the new Leader. Their Query Groups move a batch a round, not
// all at once onto the one new replica.

var rolloutNow = time.Unix(1_700_000_000, 0)

func rolloutWorker(id, digest string) ownership.WorkerRegistration {
	return ownership.WorkerRegistration{
		WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: digest, ExpiresAt: rolloutNow.Add(time.Minute),
	}
}

// rolloutFleet is n Query Groups held alternately by old-a and old-b.
func rolloutFleet(n int) ([]execution.QueryGroupIdentity, map[execution.QueryGroupIdentity]ownership.AssignmentRecord) {
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{}
	queryGroups := make([]execution.QueryGroupIdentity, 0, n)
	for index := 0; index < n; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("qg-%03d", index))
		owner := "old-a"
		if index%2 == 1 {
			owner = "old-b"
		}
		records[queryGroup] = ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: owner, AssignmentGeneration: 1, RecordRevision: 1,
			ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: rolloutNow.Add(-time.Hour),
		}
		queryGroups = append(queryGroups, queryGroup)
	}
	return queryGroups, records
}

func newRolloutReconciler(t *testing.T, store AssignmentStore) *Reconciler {
	t.Helper()
	eligibility, err := NewStaticWorkerEligibility(rolloutWorker("new-1", "cap-new").Compatibility())
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

func ownedBy(records map[execution.QueryGroupIdentity]ownership.AssignmentRecord, worker string) int {
	owned := 0
	for _, record := range records {
		if record.DesiredWorkerID == worker {
			owned++
		}
	}
	return owned
}

func TestARolloutThatChangesTheCapabilityDigestMovesABatchARound(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap-old"), rolloutWorker("old-b", "cap-old"), rolloutWorker("new-1", "cap-new")}
	queryGroups, records := rolloutFleet(200)
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	batch := rebalanceBatch(len(queryGroups))
	for round := 1; round <= 3; round++ {
		settled, _, err := reconciler.ReconcileRound(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if moved := ownedBy(settled, "new-1"); moved != round*batch {
			t.Fatalf("after round %d new-1 holds %d of %d, want %d: a batch of %d a round",
				round, moved, len(queryGroups), round*batch, batch)
		}
		if kept := ownedBy(settled, "old-a") + ownedBy(settled, "old-b"); kept != len(queryGroups)-round*batch {
			t.Fatalf("after round %d the old replicas keep %d, want the rest where they were", round, kept)
		}
	}
}

// A holder that is gone is not a handover: nothing is left to drain, and its
// Query Groups are placed at once, whatever the batch.
func TestTheQueryGroupsOfAHolderThatIsGoneArePlacedAtOnce(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-b", "cap-old"), rolloutWorker("new-1", "cap-new")}
	queryGroups, records := rolloutFleet(200)
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	settled, _, err := reconciler.ReconcileRound(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow)
	if err != nil {
		t.Fatal(err)
	}
	if held := ownedBy(settled, "old-a"); held != 0 {
		t.Fatalf("%d Query Groups are still on the departed old-a, want none", held)
	}
	// The live old-b's hundred go a batch at a round; the departed old-a's
	// hundred all go now and do not spend the batch.
	if moved := ownedBy(settled, "new-1"); moved != 100+rebalanceBatch(len(queryGroups)) {
		t.Fatalf("new-1 holds %d, want old-a's 100 plus one batch of old-b's", moved)
	}
}

// A Query Group no ready worker can take does not stop the round: it keeps
// the record it has (or stays unplaced) and every other Query Group is
// settled.
func TestAQueryGroupNoWorkerCanTakeDoesNotStopTheRound(t *testing.T) {
	queryGroups, records := rolloutFleet(4)
	// The only eligible worker is new-1, and it is not ready; old-a is gone.
	newOne := rolloutWorker("new-1", "cap-new")
	newOne.AssignmentReadiness = ownership.WorkerStarting
	workers := []ownership.WorkerRegistration{rolloutWorker("old-b", "cap-old"), newOne}
	queryGroups = append(queryGroups, "qg-new")
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	settled, _, err := reconciler.ReconcileRound(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow)
	if err != nil {
		t.Fatalf("ReconcileRound() = %v, want the round to go on past Query Groups nobody can take", err)
	}
	if held := ownedBy(settled, "old-a"); held != 2 {
		t.Fatalf("old-a's Query Groups = %d in the settled set, want both kept as they were", held)
	}
	if held := ownedBy(settled, "old-b"); held != 2 {
		t.Fatalf("old-b's Query Groups = %d, want both kept", held)
	}
	if _, placed := settled["qg-new"]; placed {
		t.Fatal("a Query Group nobody can take was given a record")
	}
	if store.published != 0 {
		t.Fatalf("%d publications, want none", store.published)
	}
}

// The settlement says what the round did: a batch replaced, the rest
// deferred, and the count correction after it gets what is left of the
// batch, so the round hands over one batch in all.
func TestARoundsSettlementAndCountCorrectionShareOneBatch(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap-old"), rolloutWorker("old-b", "cap-old"), rolloutWorker("new-1", "cap-new")}
	queryGroups, records := rolloutFleet(200)
	store := &countingAssignmentStore{workers: workers, records: records}
	reconciler := newRolloutReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	batch := HandoverBatch(len(queryGroups))
	if settlement.Replaced != batch || settlement.Deferred != len(queryGroups)-batch || settlement.Unplaceable != 0 {
		t.Fatalf("settlement = replaced %d, deferred %d, unplaceable %d; want %d, %d, 0",
			settlement.Replaced, settlement.Deferred, settlement.Unplaceable, batch, len(queryGroups)-batch)
	}
	owners := make(map[execution.QueryGroupIdentity]string, len(settlement.Records))
	for queryGroup, record := range settlement.Records {
		owners[queryGroup] = record.DesiredWorkerID
	}
	// The count correction would move toward new-1 too; with the batch spent
	// it moves nothing this round, and with the whole batch it moves one.
	if plan := reconciler.PlanRebalanceWithin(owners, workers, ByteReadings{}, rolloutNow, batch-settlement.Replaced); len(plan.Moves) != 0 {
		t.Fatalf("count correction after a spent batch planned %d moves, want 0", len(plan.Moves))
	}
	if plan := reconciler.PlanRebalanceWithin(owners, workers, ByteReadings{}, rolloutNow, -1); len(plan.Moves) == 0 {
		t.Fatal("unbounded count correction planned nothing; the shared-batch case above would mean nothing")
	}
}
