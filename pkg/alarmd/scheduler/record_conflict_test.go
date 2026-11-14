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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// casAssignmentStore is the store's compare-and-set on the record revision:
// a publication expecting a revision the record is no longer at is refused,
// and one that lands moves the revision on. stale is what the round's
// batched read answers instead of the stored record, as a copy behind the
// store would; refuse is a record refused however it is asked.
type casAssignmentStore struct {
	countingAssignmentStore
	stale  map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	refuse map[execution.QueryGroupIdentity]bool
}

func (store *casAssignmentStore) ReadAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	found, stats, err := store.countingAssignmentStore.ReadAssignments(ctx, queryGroups)
	for queryGroup, record := range store.stale {
		found[queryGroup] = record
	}
	return found, stats, err
}

func (store *casAssignmentStore) PublishAssignmentReporting(ctx context.Context, authority ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, ownership.HolderKind, error) {
	record, err := store.PublishAssignment(ctx, authority, decision)
	return record, "", err
}

func (store *casAssignmentStore) PublishAssignment(
	_ context.Context,
	_ ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	stored, exists := store.records[decision.QueryGroup]
	if store.refuse[decision.QueryGroup] || (exists && decision.ExpectedRecordRevision != stored.RecordRevision) ||
		(!exists && decision.ExpectedRecordRevision != 0) {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentConflict
	}
	store.published++
	record := stored
	record.QueryGroup, record.DesiredWorkerID, record.PlacementReason = decision.QueryGroup, decision.DesiredWorkerID, decision.PlacementReason
	record.RecordRevision++
	if decision.ContentScope != "" {
		record.ContentScope = decision.ContentScope
	}
	store.records[decision.QueryGroup] = record
	return record, nil
}

func declaredEverywhere(queryGroups []execution.QueryGroupIdentity, digest string) ContentScopes {
	digests := make(map[execution.QueryGroupIdentity]string, len(queryGroups))
	for _, queryGroup := range queryGroups {
		digests[queryGroup] = digest
	}
	return ContentScopes{Policy: ContentScopesDeclared, Digests: digests}
}

// A record that changed since the round read it - here a Worker's acquire
// moved its revision on - is refused, read again and settled once more in the
// same round; the rest of the round goes on. The round used to fail on it.
func TestARecordChangedSinceReadIsReadAgainAndSettledInTheRound(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap"), rolloutWorker("old-b", "cap")}
	queryGroups, records := rolloutFleet(4)
	stale := records["qg-000"]
	moved := stale
	moved.RecordRevision = 2
	records["qg-000"] = moved
	store := &casAssignmentStore{countingAssignmentStore: countingAssignmentStore{workers: workers, records: records},
		stale: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-000": stale}}
	reconciler := newDeclineReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers,
		rolloutNow, declaredEverywhere(queryGroups, "content-new"))
	if err != nil {
		t.Fatalf("a round with one record changed since read failed: %v", err)
	}
	if settlement.Conflicts != 1 || settlement.ConflictsAgain != 0 || store.readCalls != 1 {
		t.Fatalf("conflicts %d, again %d, single reads %d; want one refused, read once and settled", settlement.Conflicts, settlement.ConflictsAgain, store.readCalls)
	}
	for _, queryGroup := range queryGroups {
		if record := settlement.Records[queryGroup]; record.ContentScope != "content-new" || record != store.records[queryGroup] {
			t.Fatalf("%s settled as %+v, stored %+v; want every record carrying the round's content", queryGroup, record, store.records[queryGroup])
		}
	}
}

// One refused again is left as read again - the stored record - for the next
// round, and the round goes on with the rest.
func TestARecordRefusedAgainIsLeftForTheNextRound(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap"), rolloutWorker("old-b", "cap")}
	queryGroups, records := rolloutFleet(4)
	store := &casAssignmentStore{countingAssignmentStore: countingAssignmentStore{workers: workers, records: records},
		refuse: map[execution.QueryGroupIdentity]bool{"qg-001": true}}
	reconciler := newDeclineReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers,
		rolloutNow, declaredEverywhere(queryGroups, "content-new"))
	if err != nil {
		t.Fatalf("a round with one record refused twice failed: %v", err)
	}
	if settlement.Conflicts != 1 || settlement.ConflictsAgain != 1 {
		t.Fatalf("conflicts %d, again %d; want one refused twice", settlement.Conflicts, settlement.ConflictsAgain)
	}
	if record, settled := settlement.Records["qg-001"]; !settled || record != store.records["qg-001"] || record.ContentScope != "" {
		t.Fatalf("qg-001 settled %t as %+v, want the stored record unchanged", settled, record)
	}
	for _, queryGroup := range []execution.QueryGroupIdentity{"qg-000", "qg-002", "qg-003"} {
		if settlement.Records[queryGroup].ContentScope != "content-new" {
			t.Fatalf("%s was not settled after another record was refused: %+v", queryGroup, settlement.Records[queryGroup])
		}
	}
}

// A handover refused as changed did not happen: its place in the round's
// batch is given back, so the record read again is handed over in the same
// round and not deferred behind a handover that never was.
func TestAHandoverRefusedAsChangedGivesItsPlaceInTheBatchBack(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap-old"), rolloutWorker("old-b", "cap-old"), rolloutWorker("new-1", "cap-new")}
	queryGroups, records := rolloutFleet(4)
	stale := records["qg-000"]
	moved := stale
	moved.RecordRevision = 2
	records["qg-000"] = moved
	store := &casAssignmentStore{countingAssignmentStore: countingAssignmentStore{workers: workers, records: records},
		stale: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-000": stale}}
	reconciler := newRolloutReconciler(t, store)
	if batch := rebalanceBatch(len(queryGroups)); batch != 1 {
		t.Fatalf("batch %d, want 1 for the case to mean anything", batch)
	}
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	if settlement.Records["qg-000"].DesiredWorkerID != "new-1" || settlement.Replaced != 1 || settlement.Deferred != len(queryGroups)-1 {
		t.Fatalf("qg-000 on %s, replaced %d, deferred %d; want qg-000 handed over and the other three deferred",
			settlement.Records["qg-000"].DesiredWorkerID, settlement.Replaced, settlement.Deferred)
	}
}

// A handover refused twice did not happen either: its place in the batch
// goes to the next live, ineligible holder's Query Group in the round.
func TestAHandoverRefusedTwiceGivesItsPlaceToTheNext(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap-old"), rolloutWorker("old-b", "cap-old"), rolloutWorker("new-1", "cap-new")}
	queryGroups, records := rolloutFleet(4)
	store := &casAssignmentStore{countingAssignmentStore: countingAssignmentStore{workers: workers, records: records},
		refuse: map[execution.QueryGroupIdentity]bool{"qg-000": true}}
	reconciler := newRolloutReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers, rolloutNow, ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	if settlement.Records["qg-001"].DesiredWorkerID != "new-1" || settlement.Replaced != 1 || settlement.ConflictsAgain != 1 {
		t.Fatalf("qg-001 on %s, replaced %d, refused twice %d; want qg-000 refused twice and qg-001 handed over in its place",
			settlement.Records["qg-001"].DesiredWorkerID, settlement.Replaced, settlement.ConflictsAgain)
	}
}

// A record refused as changed and gone when read again - a sweep took it
// between - is a Query Group with no record: placed, not a failed round.
func TestARecordGoneWhenReadAgainIsPlaced(t *testing.T) {
	workers := []ownership.WorkerRegistration{rolloutWorker("old-a", "cap"), rolloutWorker("old-b", "cap")}
	queryGroups, records := rolloutFleet(4)
	stale := records["qg-000"]
	delete(records, "qg-000")
	store := &casAssignmentStore{countingAssignmentStore: countingAssignmentStore{workers: workers, records: records},
		stale: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-000": stale}}
	reconciler := newDeclineReconciler(t, store)
	settlement, err := reconciler.ReconcileRoundSettling(context.Background(), ownership.PublicationAuthority{}, queryGroups, workers,
		rolloutNow, declaredEverywhere(queryGroups, "content-new"))
	if err != nil {
		t.Fatalf("a round whose refused record was gone when read again failed: %v", err)
	}
	if placed, exists := store.records["qg-000"]; !exists || settlement.Records["qg-000"] != placed || settlement.Conflicts != 1 {
		t.Fatalf("qg-000 stored %t as %+v, settled %+v, conflicts %d; want it placed after one refusal", exists, placed, settlement.Records["qg-000"], settlement.Conflicts)
	}
}
