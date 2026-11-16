// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// readLoggingStore is the round's fake store, noting which records each
// batched read asked for; leaderEpoch, when set, is the term the next
// Control Leader acquisition is granted.
type readLoggingStore struct {
	*rebalanceOwnershipStore
	asked       [][]execution.QueryGroupIdentity
	leaderEpoch uint64
}

// PublishAssignment answers a content scope publication as the store does,
// the record at the next revision carrying the scope; any other is the
// round fake's.
func (store *readLoggingStore) PublishAssignment(ctx context.Context, authority ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, error) {
	current, exists := store.assignments[decision.QueryGroup]
	if decision.ContentScope == "" || !exists {
		return store.rebalanceOwnershipStore.PublishAssignment(ctx, authority, decision)
	}
	if current.RecordRevision != decision.ExpectedRecordRevision {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentConflict
	}
	current.ContentScope = decision.ContentScope
	current.RecordRevision++
	store.assignments[decision.QueryGroup] = current
	return current, nil
}

func (store *readLoggingStore) AcquireControlLeader(ctx context.Context, leaderID string, at time.Time, ttl time.Duration) (ownership.PublicationAuthority, error) {
	authority, err := store.rebalanceOwnershipStore.AcquireControlLeader(ctx, leaderID, at, ttl)
	if store.leaderEpoch != 0 {
		authority.Fence.OwnerEpoch = store.leaderEpoch
	}
	return authority, err
}

func (store *readLoggingStore) ReadAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	store.asked = append(store.asked, append([]execution.QueryGroupIdentity(nil), queryGroups...))
	return store.rebalanceOwnershipStore.ReadAssignments(ctx, queryGroups)
}

// fakeLeaderActivations is the catalog's activation header and deltas as
// the record cache reads them: changes is what the next read answers.
type fakeLeaderActivations struct {
	changes controlplane.ActivationChanges
	err     error
	since   []uint64
	skip    func(uint64) bool
	hear    func(uint64, map[execution.QueryGroupIdentity]uint64)
	terms   []uint64
}

func (activations *fakeLeaderActivations) BeginLeaderTerm(epoch uint64) {
	activations.terms = append(activations.terms, epoch)
}

func (activations *fakeLeaderActivations) ActivationChangesSince(_ context.Context, since uint64, skip func(uint64) bool) (controlplane.ActivationChanges, error) {
	activations.since = append(activations.since, since)
	activations.skip = skip
	return activations.changes, activations.err
}

func (activations *fakeLeaderActivations) OnRecordTimelineRevisions(hear func(uint64, map[execution.QueryGroupIdentity]uint64)) {
	activations.hear = hear
}

var recordCacheGroups = []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}

// recordCacheRound is a Leader of term 1 over three Query Groups held by
// worker-1, all eligible, reading its activations from activations (nil:
// none).
func recordCacheRound(t *testing.T, activations leaderActivations) (*productionPhaseTwoOwnership, *readLoggingStore, *[]observability.Observation) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
		}
	}
	store := &readLoggingStore{rebalanceOwnershipStore: &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		workers:                    []ownership.WorkerRegistration{worker("worker-1")},
		assignments:                map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
	}}
	for _, queryGroup := range recordCacheGroups {
		store.assignments[queryGroup] = ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1,
			ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		}
	}
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(
		ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	observations := &[]observability.Observation{}
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Hour, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observations = append(*observations, observation)
		}), Reconcile: reconciler, Activations: activations, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
		SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Hour); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	return production, store, observations
}

// recordRound runs one round and answers what its control reads line says
// it read, by why.
func recordRound(t *testing.T, production *productionPhaseTwoOwnership, observations *[]observability.Observation, groups []execution.QueryGroupIdentity) *observability.ControlReadFacts {
	t.Helper()
	*observations = nil
	if err := production.PublishAssignments(context.Background(), groups, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatalf("PublishAssignments() error = %v", err)
	}
	for _, observation := range *observations {
		if observation.Stage == observability.StageControlReadsSpent {
			return observation.ControlReads
		}
	}
	t.Fatal("the round reported nothing about its control reads")
	return nil
}

// A term reads every record once; each round after reads only its share of
// the backstop, in turn, so every record is read again within the population
// over sixty rounds. Rounds used to read every record every time.
func TestATermReadsEveryRecordOnceAndThenOnlyTheBackstopsShare(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	first := recordRound(t, production, observations, recordCacheGroups)
	if first.AssignmentKeys != 3 || !reflect.DeepEqual(first.RecordReads, map[string]int{"term_start": 3}) {
		t.Fatalf("first round read %d keys as %v, want the three as term_start", first.AssignmentKeys, first.RecordReads)
	}
	read := map[execution.QueryGroupIdentity]int{}
	for round := 2; round <= 4; round++ {
		store.asked = nil
		facts := recordRound(t, production, observations, recordCacheGroups)
		if facts.AssignmentKeys != 1 || !reflect.DeepEqual(facts.RecordReads, map[string]int{"backstop": 1}) || len(store.asked) != 1 {
			t.Fatalf("round %d read %d keys as %v in %d reads, want one as backstop", round, facts.AssignmentKeys, facts.RecordReads, len(store.asked))
		}
		read[store.asked[0][0]]++
	}
	if len(read) != 3 {
		t.Fatalf("three rounds of backstop read %v, want each record once", read)
	}
}

// What the term's first read reads is the copy the first round answers from:
// the stored view a new term publishes reads everything, and the round after
// it reads only its backstop share.
func TestTheStoredViewsReadIsTheCopyTheFirstRoundAnswersFrom(t *testing.T) {
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 7, Complete: true}}
	production, _, observations := recordCacheRound(t, activations)
	if _, _, err := production.records.readStoredView(context.Background(), production.authority, 7, recordCacheGroups); err != nil {
		t.Fatal(err)
	}
	if facts := recordRound(t, production, observations, recordCacheGroups); facts.AssignmentKeys != 1 ||
		!reflect.DeepEqual(facts.RecordReads, map[string]int{"term_start": 3, "backstop": 1}) {
		t.Fatalf("the round after the stored view read %d keys, the two reads %v; want the backstop's one", facts.AssignmentKeys, facts.RecordReads)
	}
	if !reflect.DeepEqual(activations.since, []uint64{7}) {
		t.Fatalf("the round read the activations since %v, want since the header the stored view was read at", activations.since)
	}
}

// The stored view a new term publishes reads its records through the copy,
// and fills it.
func TestPublishingTheStoredViewFillsTheCopy(t *testing.T) {
	var observed []observability.Observation
	store := &storedViewStore{records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-active": {DesiredWorkerID: "worker-a", RecordRevision: 3}}}
	runtime, _, _ := storedViewRuntime(t, storedViewSource{active: []execution.QueryGroupIdentity{"qg-active"}}, store, &observed)
	runtime.PublishStoredView(context.Background())
	asked := len(store.asked)
	if _, _, err := runtime.records.ReadAssignments(context.Background(), []execution.QueryGroupIdentity{"qg-active"}); err != nil {
		t.Fatal(err)
	}
	if len(store.asked) != asked {
		t.Fatalf("after the stored view the record was read again: %v", store.asked)
	}
}

// A term the store grants while the copy belongs to another, with no step
// down between - a lease that ran out and was taken again - reads every
// record again on its first read, whatever path reads first.
func TestATermTakenAgainWithoutAStepDownReadsEveryRecord(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	production.records.takeRoundFacts()
	store.leaderEpoch = 2
	production.mu.Lock()
	production.authority.Deadline = time.Unix(0, 0)
	production.mu.Unlock()
	if _, err := production.ensureControlAuthority(context.Background(), time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := production.records.ReadAssignments(context.Background(), recordCacheGroups); err != nil {
		t.Fatal(err)
	}
	if reads, _, _ := production.records.takeRoundFacts(); !reflect.DeepEqual(reads, map[string]int{"term_start": 3}) {
		t.Fatalf("the new term's first read read %v, want every record as term_start", reads)
	}
}

// A new term reads every record again: what the copy held belonged to the
// term that ended.
func TestANewTermReadsEveryRecordAgain(t *testing.T) {
	production, _, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	production.clearControlAuthority(production.authority)
	if leader, err := production.TryAcquireControlLeader(context.Background(), time.Unix(1_700_000_000, 0), time.Hour); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if facts := recordRound(t, production, observations, recordCacheGroups); !reflect.DeepEqual(facts.RecordReads, map[string]int{"term_start": 3}) {
		t.Fatalf("a new term's first round read %v, want every record as term_start", facts.RecordReads)
	}
}

// A Query Group entering the population is read as it enters, and the
// record its placement answered with is the copy's: the next round neither
// reads it nor places it again.
func TestAPlacementsAnswerIsTheCopyAndNotReadAgain(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	grown := append(append([]execution.QueryGroupIdentity(nil), recordCacheGroups...), "query-group-4")
	facts := recordRound(t, production, observations, grown)
	if facts.RecordReads["entered"] != 1 || store.assignments["query-group-4"].DesiredWorkerID != "worker-1" {
		t.Fatalf("the round a Query Group entered read %v and stored %+v, want it read as entered and placed", facts.RecordReads, store.assignments["query-group-4"])
	}
	published := store.published
	store.asked = nil
	facts = recordRound(t, production, observations, grown)
	if facts.RecordReads["entered"] != 0 || store.published != published {
		t.Fatalf("the round after a placement read %v and published %d more, want neither", facts.RecordReads, store.published-published)
	}
}

// A publication that failed without saying whether it was written leaves
// the copy nothing from before it: the next round reads that record.
func TestAPublicationWhoseOutcomeIsUnknownIsReadNextRound(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	grown := append(append([]execution.QueryGroupIdentity(nil), recordCacheGroups...), "query-group-4")
	store.publishErr = errors.New("i/o timeout")
	*observations = nil
	if err := production.PublishAssignments(context.Background(), grown, time.Unix(1_700_000_000, 0)); err == nil {
		t.Fatal("a round whose placement failed did not fail")
	}
	store.publishErr = nil
	if facts := recordRound(t, production, observations, grown); facts.RecordReads["unknown_outcome"] != 1 {
		t.Fatalf("the round after a placement of unknown outcome read %v, want it read as unknown_outcome", facts.RecordReads)
	}
}

// A record refused as changed is read again in the round, and the rest of
// the round goes on.
func TestARecordRefusedAsChangedIsReadAgainInTheRound(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	grown := append(append([]execution.QueryGroupIdentity(nil), recordCacheGroups...), "query-group-4")
	moved := false
	store.beforePublish = func(decision ownership.AssignmentDecision) {
		if !moved && decision.QueryGroup == "query-group-4" {
			moved = true
			store.assignments["query-group-4"] = ownership.AssignmentRecord{QueryGroup: "query-group-4", DesiredWorkerID: "worker-1", RecordRevision: 3}
		}
	}
	facts := recordRound(t, production, observations, grown)
	if facts.RecordConflicts != 1 || facts.RecordConflictsAgain != 0 || facts.RecordReads["conflict"] != 1 {
		t.Fatalf("conflicts %d again %d, reads %v; want one refused, read again as conflict and settled", facts.RecordConflicts, facts.RecordConflictsAgain, facts.RecordReads)
	}
}

// The backstop finds what a Worker's acquire changed - the pending content
// promoted - and counts only those fields, naming none of them.
func TestTheBackstopFindsAWorkersPromotionOnItsOwnFields(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	promoted := store.assignments["query-group-2"]
	promoted.ContentScope, promoted.PendingContentScope, promoted.EffectiveAt = "content-b", "content-c", time.Unix(1_700_000_100, 0)
	store.assignments["query-group-2"] = promoted
	mismatches := map[string]int{}
	var sample []observability.RecordMismatch
	for round := 0; round < 3; round++ {
		facts := recordRound(t, production, observations, recordCacheGroups)
		maps.Copy(mismatches, facts.RecordMismatches)
		sample = append(sample, facts.RecordMismatchSample...)
	}
	if want := map[string]int{"content_scope": 1, "pending_content_scope": 1, "effective_at_ms": 1}; !reflect.DeepEqual(mismatches, want) || len(sample) != 0 {
		t.Fatalf("the backstop counted %v and named %+v, want %v and none named", mismatches, sample, want)
	}
}

// A field no Worker writes found wrong is a write the copy missed: counted,
// and named with both values.
func TestTheBackstopNamesAWriteTheCopyMissed(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	moved := store.assignments["query-group-3"]
	moved.DesiredWorkerID, moved.RecordRevision, moved.PlacementReason, moved.TimelineRecordRevision = "worker-9", 4, ownership.PlacementRebalance, 6
	store.assignments["query-group-3"] = moved
	delete(store.assignments, "query-group-2")
	var named []observability.RecordMismatch
	for round := 0; round < 3; round++ {
		named = append(named, recordRound(t, production, observations, recordCacheGroups).RecordMismatchSample...)
	}
	want := []observability.RecordMismatch{
		{QueryGroup: "query-group-2", Field: "presence", Cached: "true", Stored: "false"},
		{QueryGroup: "query-group-3", Field: "record_revision", Cached: "1", Stored: "4"},
		{QueryGroup: "query-group-3", Field: "desired_worker_id", Cached: "worker-1", Stored: "worker-9"},
		{QueryGroup: "query-group-3", Field: "placement_reason", Cached: string(ownership.PlacementRendezvous), Stored: string(ownership.PlacementRebalance)},
		{QueryGroup: "query-group-3", Field: "timeline_record_revision", Cached: "0", Stored: "6"},
	}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("named %+v, want %+v", named, want)
	}
}

// Another process's activation rewrote a timeline revision onto a record:
// the next round reads the activation's delta and that record, and answers
// with the revision it wrote, so the view carries it. One whose delta cannot
// say has everything read again.
func TestAnotherProcesssActivationIsReadFromItsDelta(t *testing.T) {
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 10}}
	production, store, observations := recordCacheRound(t, activations)
	recordRound(t, production, observations, recordCacheGroups)
	rewritten := store.assignments["query-group-3"]
	rewritten.TimelineRecordRevision = 5
	store.assignments["query-group-3"] = rewritten
	activations.changes = controlplane.ActivationChanges{Revision: 11, Complete: true, QueryGroups: []execution.QueryGroupIdentity{"query-group-3"}}
	settle := func() scheduler.RoundSettlement {
		t.Helper()
		settlement, err := production.reconciler.ReconcileRoundSettling(context.Background(), production.authority, recordCacheGroups,
			store.workers, time.Unix(1_700_000_000, 0), scheduler.ContentScopes{})
		if err != nil {
			t.Fatal(err)
		}
		return settlement
	}
	// Without the delta read, the copy answers what it held.
	if got := settle().Records["query-group-3"].TimelineRecordRevision; got != 0 {
		t.Fatalf("before the delta is read the copy answers %d, want the 0 it held", got)
	}
	if err := production.records.prepareRound(context.Background(), production.authority, recordCacheGroups); err != nil {
		t.Fatal(err)
	}
	settlement := settle()
	if got := activations.since; !reflect.DeepEqual(got, []uint64{0, 10}) {
		t.Fatalf("the activations were read since %v, want 0 for the term's first round and 10 after", got)
	}
	if got := settlement.Records["query-group-3"].TimelineRecordRevision; got != 5 {
		t.Fatalf("the round after another process's activation answers timeline revision %d, want 5", got)
	}
	reads, mismatches, _ := production.records.takeRoundFacts()
	if reads["foreign_cutover"] != 1 || len(mismatches) != 0 {
		t.Fatalf("reads %v, mismatches %v; want the activation's one record read as foreign_cutover, and a change it was read for not counted as missed", reads, mismatches)
	}
	activations.changes = controlplane.ActivationChanges{Revision: 40}
	if facts := recordRound(t, production, observations, recordCacheGroups); !reflect.DeepEqual(facts.RecordReads, map[string]int{"foreign_cutover": 3}) {
		t.Fatalf("a delta that cannot say had %v read, want every record as foreign_cutover", facts.RecordReads)
	}
}

// This process's own activation wrote the timeline revisions it says it
// did: the copy takes them as written, without a read, and the delta of
// that activation is passed over.
func TestThisProcesssActivationIsTakenAsWritten(t *testing.T) {
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 10}}
	production, store, observations := recordCacheRound(t, activations)
	recordRound(t, production, observations, recordCacheGroups)
	if activations.hear == nil {
		t.Fatal("the runtime does not hear its own activations")
	}
	rewritten := store.assignments["query-group-3"]
	rewritten.TimelineRecordRevision = 8
	store.assignments["query-group-3"] = rewritten
	activations.hear(11, map[execution.QueryGroupIdentity]uint64{"query-group-3": 8})
	activations.changes = controlplane.ActivationChanges{Revision: 11, Complete: true}
	if err := production.records.prepareRound(context.Background(), production.authority, recordCacheGroups); err != nil {
		t.Fatal(err)
	}
	if !activations.skip(11) || activations.skip(12) {
		t.Fatal("the delta of this process's own activation is read, or another's is passed over")
	}
	settlement, err := production.reconciler.ReconcileRoundSettling(context.Background(), production.authority, recordCacheGroups,
		store.workers, time.Unix(1_700_000_000, 0), scheduler.ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	if got := settlement.Records["query-group-3"].TimelineRecordRevision; got != 8 {
		t.Fatalf("the round after this process's activation answers timeline revision %d, want 8", got)
	}
	if reads, _, _ := production.records.takeRoundFacts(); reads["foreign_cutover"] != 0 || reads["entered"] != 0 {
		t.Fatalf("reads %v, want none for an activation of this process", reads)
	}
}

// A Query Group a sweep removed is out of the copy: back in the population,
// it is read as it enters.
func TestAQueryGroupSweptIsReadAsItComesBack(t *testing.T) {
	production, _, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	recordRound(t, production, observations, recordCacheGroups[:2])
	if facts := recordRound(t, production, observations, recordCacheGroups); facts.RecordReads["entered"] != 1 {
		t.Fatalf("a Query Group back after a sweep was read %v, want as entered", facts.RecordReads)
	}
}

// racingRecordStore answers reads from records, running during once inside
// a batched read after it has read - a write landing while the read is out -
// and failing one read when failNext is set.
type racingRecordStore struct {
	records  map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	during   func()
	failNext error
	batches  [][]execution.QueryGroupIdentity
}

func (store *racingRecordStore) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, ownership.ControlReadStats, error) {
	return nil, ownership.ControlReadStats{}, nil
}

func (store *racingRecordStore) ReadAssignment(_ context.Context, queryGroup execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	if record, ok := store.records[queryGroup]; ok {
		return record, nil
	}
	return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
}

func (store *racingRecordStore) ReadAssignments(_ context.Context, queryGroups []execution.QueryGroupIdentity) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	store.batches = append(store.batches, append([]execution.QueryGroupIdentity(nil), queryGroups...))
	if err := store.failNext; err != nil {
		store.failNext = nil
		return nil, ownership.ControlReadStats{}, err
	}
	found := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{}
	for _, queryGroup := range queryGroups {
		if record, ok := store.records[queryGroup]; ok {
			found[queryGroup] = record
		}
	}
	if during := store.during; during != nil {
		store.during = nil
		during()
	}
	return found, ownership.ControlReadStats{Keys: len(queryGroups), RoundTrips: 1}, nil
}

func (store *racingRecordStore) PublishAssignment(ctx context.Context, authority ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, error) {
	record, _, err := store.PublishAssignmentReporting(ctx, authority, decision)
	return record, err
}

func (store *racingRecordStore) PublishAssignmentReporting(_ context.Context, _ ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, ownership.HolderKind, error) {
	record := store.records[decision.QueryGroup]
	record.QueryGroup, record.DesiredWorkerID = decision.QueryGroup, decision.DesiredWorkerID
	record.RecordRevision++
	store.records[decision.QueryGroup] = record
	return record, "", nil
}

func racingRecords() *racingRecordStore {
	return &racingRecordStore{records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-1": {QueryGroup: "qg-1", DesiredWorkerID: "worker-1", RecordRevision: 1},
		"qg-2": {QueryGroup: "qg-2", DesiredWorkerID: "worker-1", RecordRevision: 1},
	}}
}

// A write that lands while a read is out is newer than what the read
// found: the copy keeps the write, and answers it. A change the copy could
// only note - this process's activation over a record it did not hold yet -
// leaves that record unknown, read as entered next.
func TestAWriteLandingWhileARecordIsReadOutlivesTheRead(t *testing.T) {
	store := racingRecords()
	cache := newLeaderRecordCache(store, nil)
	cache.lead(1)
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{OwnerEpoch: 1}}
	var published ownership.AssignmentRecord
	store.during = func() {
		var err error
		published, err = cache.PublishAssignment(context.Background(), authority, ownership.AssignmentDecision{QueryGroup: "qg-1", DesiredWorkerID: "worker-2"})
		if err != nil {
			t.Fatal(err)
		}
		cache.noteActivation(5, map[execution.QueryGroupIdentity]uint64{"qg-2": 4})
	}
	answer, _, err := cache.ReadAssignments(context.Background(), []execution.QueryGroupIdentity{"qg-1", "qg-2"})
	if err != nil {
		t.Fatal(err)
	}
	if answer["qg-1"] != published || answer["qg-2"] != store.records["qg-2"] {
		t.Fatalf("answered %+v; want the publication for qg-1 and what was read for qg-2", answer)
	}
	store.batches = nil
	again, _, err := cache.ReadAssignments(context.Background(), []execution.QueryGroupIdentity{"qg-1", "qg-2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]execution.QueryGroupIdentity{{"qg-2"}}; !reflect.DeepEqual(store.batches, want) || again["qg-1"] != published {
		t.Fatalf("the next read asked for %v and answered %+v; want qg-2 alone read and qg-1 the publication", store.batches, again)
	}
	if reads, _, _ := cache.takeRoundFacts(); reads["entered"] != 1 {
		t.Fatalf("reads %v, want qg-2 read as entered", reads)
	}
}

// A term that ends while a read is out answers nothing: what the read would
// answer for the records it did not read belongs to neither term, and a
// record missing from the answer would be placed.
func TestATermEndingWhileARecordIsReadAnswersNothing(t *testing.T) {
	store := racingRecords()
	cache := newLeaderRecordCache(store, nil)
	cache.lead(1)
	store.during = cache.stepDown
	if answer, _, err := cache.ReadAssignments(context.Background(), []execution.QueryGroupIdentity{"qg-1", "qg-2"}); !errors.Is(err, errRecordTermChanged) || answer != nil {
		t.Fatalf("a read across a term's end answered %+v, %v; want nothing and the term change", answer, err)
	}
}

// The copy holds the activations up to the earliest header read before the
// read of everything that filled it: one read later may be past an
// activation the fill read from before.
func TestTheCopyHoldsTheEarliestHeaderReadBeforeItsFill(t *testing.T) {
	store := racingRecords()
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 12}}
	cache := newLeaderRecordCache(store, activations)
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{OwnerEpoch: 1}}
	groups := []execution.QueryGroupIdentity{"qg-1", "qg-2"}
	store.failNext = errors.New("i/o timeout")
	if _, _, err := cache.readStoredView(context.Background(), authority, 10, groups); err == nil {
		t.Fatal("a stored view whose read failed answered")
	}
	if err := cache.prepareRound(context.Background(), authority, groups); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.ReadAssignments(context.Background(), groups); err != nil {
		t.Fatal(err)
	}
	activations.since = nil
	if err := cache.prepareRound(context.Background(), authority, groups); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(activations.since, []uint64{10}) {
		t.Fatalf("after the fill the activations were read since %v, want 10, the earlier header", activations.since)
	}
}

// Outside a term there is no copy: a read reads every record and keeps
// none, and a write or an activation heard is not kept.
func TestOutsideATermEveryReadReadsEveryRecord(t *testing.T) {
	store := racingRecords()
	cache := newLeaderRecordCache(store, nil)
	groups := []execution.QueryGroupIdentity{"qg-1", "qg-2"}
	if _, err := cache.PublishAssignment(context.Background(), ownership.PublicationAuthority{}, ownership.AssignmentDecision{QueryGroup: "qg-1", DesiredWorkerID: "worker-2"}); err != nil {
		t.Fatal(err)
	}
	cache.noteActivation(3, map[execution.QueryGroupIdentity]uint64{"qg-1": 2})
	for read := 0; read < 2; read++ {
		if _, _, err := cache.ReadAssignments(context.Background(), groups); err != nil {
			t.Fatal(err)
		}
	}
	if want := [][]execution.QueryGroupIdentity{groups, groups}; !reflect.DeepEqual(store.batches, want) {
		t.Fatalf("reads outside a term asked for %v, want every record each time", store.batches)
	}
	if reads, _, _ := cache.takeRoundFacts(); len(reads) != 0 {
		t.Fatalf("reads outside a term counted %v, want none: there is no copy they were for", reads)
	}
}

// The catalog's renewals belong to the term: each term the Leader takes, the
// catalog is told, so its first refresh round renews every timeline afresh.
func TestEachTermTheLeaderTakesBeginsTheCatalogsRenewals(t *testing.T) {
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 10}}
	production, store, _ := recordCacheRound(t, activations)
	store.leaderEpoch = 2
	production.mu.Lock()
	production.authority.Deadline = time.Unix(0, 0)
	production.mu.Unlock()
	if _, err := production.ensureControlAuthority(context.Background(), time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if want := []uint64{1, 2}; !reflect.DeepEqual(activations.terms, want) {
		t.Fatalf("the catalog was told terms %v, want %v", activations.terms, want)
	}
}

// A delta that cannot say has every record the copy holds read again, as the
// reads come to each: a read of a few first - the content scopes' - reads
// those, and the round after reads the rest. Unfilling the copy instead let
// that read of a few end the "read of everything", and the rest kept the
// timeline revisions from before the activation until the backstop.
func TestADeltaThatCannotSayHasEveryHeldRecordReadAgainWhoeverReadsFirst(t *testing.T) {
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 10}}
	production, store, observations := recordCacheRound(t, activations)
	recordRound(t, production, observations, recordCacheGroups)
	for _, queryGroup := range recordCacheGroups {
		rewritten := store.assignments[queryGroup]
		rewritten.TimelineRecordRevision = 9
		store.assignments[queryGroup] = rewritten
	}
	activations.changes = controlplane.ActivationChanges{Revision: 40}
	if err := production.records.prepareRound(context.Background(), production.authority, recordCacheGroups); err != nil {
		t.Fatal(err)
	}
	if _, _, err := production.records.ReadAssignments(context.Background(), recordCacheGroups[:1]); err != nil {
		t.Fatal(err)
	}
	if reads, _, _ := production.records.takeRoundFacts(); !reflect.DeepEqual(reads, map[string]int{"foreign_cutover": 1}) {
		t.Fatalf("the read of one after a delta that cannot say read %v, want it as foreign_cutover", reads)
	}
	activations.changes = controlplane.ActivationChanges{Revision: 40, Complete: true}
	settlement, err := production.reconciler.ReconcileRoundSettling(context.Background(), production.authority, recordCacheGroups,
		store.workers, time.Unix(1_700_000_000, 0), scheduler.ContentScopes{})
	if err != nil {
		t.Fatal(err)
	}
	if reads, _, _ := production.records.takeRoundFacts(); !reflect.DeepEqual(reads, map[string]int{"foreign_cutover": 2}) {
		t.Fatalf("the round after it read %v, want the other two as foreign_cutover", reads)
	}
	for _, queryGroup := range recordCacheGroups {
		if got := settlement.Records[queryGroup].TimelineRecordRevision; got != 9 {
			t.Fatalf("%s answers timeline revision %d, want the 9 the activation wrote", queryGroup, got)
		}
	}
}

// The content scopes the refresh publishes ahead of a cutover read the
// records from the copy and keep what each publication answered: no record
// is read, and the copy answers the publication's record after it.
func TestTheContentScopesPublicationsAreTheCopysAndReadNothing(t *testing.T) {
	production, store, observations := recordCacheRound(t, nil)
	recordRound(t, production, observations, recordCacheGroups)
	store.workers[0].Capabilities = []string{ownership.CapabilityContentScope}
	asked := len(store.asked)
	production.PublishContentScopes(context.Background(), map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-2": "content-b"})
	published := store.assignments["query-group-2"]
	if published.ContentScope != "content-b" || len(store.asked) != asked {
		t.Fatalf("the content scopes stored %+v after %d reads, want the scope published and no record read", published, len(store.asked)-asked)
	}
	answer, _, err := production.records.ReadAssignments(context.Background(), []execution.QueryGroupIdentity{"query-group-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.asked) != asked || answer["query-group-2"] != published {
		t.Fatalf("after the publication the copy answers %+v after %d reads, want %+v and no read", answer["query-group-2"], len(store.asked)-asked, published)
	}
}

// A copy begun by a read before any header was read - the content scopes'
// in a term's first tick - has no revision to start from: once one is read,
// every record it holds is read again, as the term's start, and the deltas
// are read from that header on.
func TestACopyBegunBeforeAnyHeaderIsReadAgainAsTheTermsStart(t *testing.T) {
	store := racingRecords()
	activations := &fakeLeaderActivations{changes: controlplane.ActivationChanges{Revision: 5}}
	cache := newLeaderRecordCache(store, activations)
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{OwnerEpoch: 1}}
	groups := []execution.QueryGroupIdentity{"qg-1", "qg-2"}
	cache.lead(1)
	if _, _, err := cache.ReadAssignments(context.Background(), groups); err != nil {
		t.Fatal(err)
	}
	cache.takeRoundFacts()
	if err := cache.prepareRound(context.Background(), authority, groups); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.ReadAssignments(context.Background(), groups); err != nil {
		t.Fatal(err)
	}
	if reads, _, _ := cache.takeRoundFacts(); !reflect.DeepEqual(reads, map[string]int{"term_start": 2}) {
		t.Fatalf("after the first header the copy read %v, want both again as term_start", reads)
	}
	activations.changes = controlplane.ActivationChanges{Revision: 5, Complete: true}
	if err := cache.prepareRound(context.Background(), authority, groups); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(activations.since, []uint64{0, 5}) {
		t.Fatalf("the activations were read since %v, want 0 and then the header read", activations.since)
	}
}
