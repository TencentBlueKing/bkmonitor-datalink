// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// noContentScopes is the content reader of a fixture whose rounds are not
// about the content contract: it knows no content, so a declaring round
// touches no record's scope.
func noContentScopes(context.Context) (map[execution.QueryGroupIdentity]string, error) {
	return map[execution.QueryGroupIdentity]string{}, nil
}

// The gate of the content contract, decided per round on the ready set: a
// fleet that declares throughout gets the content it is published with; a
// fleet with one member that does not gets every scope withdrawn; and a
// leader that cannot read the current content this round leaves scopes as
// they are -- unknown is not a withdrawal, and a Redis blip must not roll
// the contract back fleet-wide -- and says so.
func TestTheContentContractIsGatedOnTheWholeReadySet(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	declaring := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "d", ExpiresAt: now.Add(time.Minute),
			Capabilities: []string{ownership.CapabilityContentScope},
		}
	}
	silent := declaring("old")
	silent.Capabilities = nil
	digests := map[execution.QueryGroupIdentity]string{"query-group-1": "qg-object-1"}
	cases := []struct {
		name       string
		workers    []ownership.WorkerRegistration
		readErr    error
		wantPolicy scheduler.ContentScopePolicy
		wantDigest bool
		wantReport bool
	}{
		{name: "whole fleet declares", workers: []ownership.WorkerRegistration{declaring("a"), declaring("b")}, wantPolicy: scheduler.ContentScopesDeclared, wantDigest: true},
		{name: "one member does not", workers: []ownership.WorkerRegistration{declaring("a"), silent}, wantPolicy: scheduler.ContentScopesWithdrawn},
		{name: "no ready worker", workers: nil, wantPolicy: scheduler.ContentScopesWithdrawn},
		{name: "content unreadable", workers: []ownership.WorkerRegistration{declaring("a")}, readErr: errors.New("manifest gone"), wantPolicy: scheduler.ContentScopesUntouched, wantReport: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var reports []observability.Observation
			runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					reports = append(reports, observation)
				}),
				ContentScopes: func(context.Context) (map[execution.QueryGroupIdentity]string, error) {
					if test.readErr != nil {
						return nil, test.readErr
					}
					return digests, nil
				},
			}}
			scopes, err := runtime.contentScopesFor(context.Background(), test.workers)
			if err != nil {
				t.Fatalf("contentScopesFor() error = %v", err)
			}
			if scopes.Policy != test.wantPolicy {
				t.Fatalf("policy = %v, want %v", scopes.Policy, test.wantPolicy)
			}
			if test.wantDigest && scopes.Digests["query-group-1"] != "qg-object-1" {
				t.Fatalf("digests = %v, want the published content", scopes.Digests)
			}
			if !test.wantDigest && len(scopes.Digests) != 0 {
				t.Fatalf("digests = %v, want none", scopes.Digests)
			}
			if test.wantReport != (len(reports) == 1) {
				t.Fatalf("reports = %+v, want reported=%t", reports, test.wantReport)
			}
		})
	}
}

type fakeContentScopeSource struct {
	state    controlplane.ActivationState
	manifest controlplane.CatalogManifest
	err      error
	asked    []execution.SnapshotRevision
}

func (source *fakeContentScopeSource) LoadActivation(context.Context) (controlplane.ActivationState, error) {
	return source.state, source.err
}

func (source *fakeContentScopeSource) LoadCatalogManifest(_ context.Context, revision execution.SnapshotRevision) (controlplane.CatalogManifest, error) {
	source.asked = append(source.asked, revision)
	return source.manifest, source.err
}

// The content a Query Group is published with is the ObjectDigest the
// current activation's manifest names for it -- the digest its open Segment
// carries and its Slots declare -- read for the activation's own revision.
func TestCurrentContentScopesReadTheActivationsManifest(t *testing.T) {
	source := &fakeContentScopeSource{
		state: controlplane.ActivationState{Current: controlplane.SnapshotPublicationRef{SnapshotRevision: "snap-9", PublicationEpoch: 2}},
		manifest: controlplane.CatalogManifest{QueryGroups: []controlplane.ManifestQueryGroup{
			{QueryGroup: "qg-a", ObjectDigest: "da"}, {QueryGroup: "qg-b", ObjectDigest: "db"}, {QueryGroup: "", ObjectDigest: "dx"}, {QueryGroup: "qg-c", ObjectDigest: ""},
		}},
	}
	digests, err := currentContentScopes(source)(context.Background())
	if err != nil {
		t.Fatalf("currentContentScopes() error = %v", err)
	}
	if len(digests) != 2 || digests["qg-a"] != "da" || digests["qg-b"] != "db" {
		t.Fatalf("digests = %v, want the two complete entries", digests)
	}
	if len(source.asked) != 1 || source.asked[0] != "snap-9" {
		t.Fatalf("manifest read for %v, want the activation's revision snap-9", source.asked)
	}
	source.err = errors.New("activation unavailable")
	if _, err := currentContentScopes(source)(context.Background()); err == nil {
		t.Fatal("a failed activation read returned digests")
	}
}

// The pre-cutover writer names the new content in the record of each
// changing Query Group, under the same gate as the round and against the
// revision it read; it names nothing for a fleet that does not all declare,
// nothing for a Query Group with no record, and nothing for a record already
// on or pending that content.
func TestThePreCutoverWriterNamesChangedContentUnderTheFleetsGate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(declares bool) ownership.WorkerRegistration {
		registration := ownership.WorkerRegistration{
			WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "d", ExpiresAt: now.Add(time.Minute),
		}
		if declares {
			registration.Capabilities = []string{ownership.CapabilityContentScope}
		}
		return registration
	}
	record := func(scope, pending string) ownership.AssignmentRecord {
		return ownership.AssignmentRecord{
			QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 2, RecordRevision: 7, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRebalance, AssignedAt: now.Add(-time.Minute), ContentScope: scope, PendingContentScope: pending,
		}
	}
	cases := []struct {
		name      string
		declares  bool
		record    ownership.AssignmentRecord
		changes   map[execution.QueryGroupIdentity]execution.ObjectDigest
		wantScope string
	}{
		{name: "changed content is named", declares: true, record: record("old", ""), changes: map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-1": "new"}, wantScope: "new"},
		{name: "a fleet with a silent worker gets nothing", declares: false, record: record("old", ""), changes: map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-1": "new"}},
		{name: "a record already pending that content is left alone", declares: true, record: record("old", "new"), changes: map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-1": "new"}},
		{name: "a record already on that content is left alone", declares: true, record: record("new", ""), changes: map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-1": "new"}},
		{name: "a Query Group with no record is the round's to place", declares: true, record: record("old", ""), changes: map[execution.QueryGroupIdentity]execution.ObjectDigest{"query-group-2": "new"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := &fakePhaseTwoOwnershipStore{now: now, worker: worker(test.declares), assignment: test.record}
			reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(nil), store)
			if err != nil {
				t.Fatal(err)
			}
			runtime := &productionPhaseTwoOwnership{reconciler: reconciler, dependencies: productionPhaseTwoOwnershipDependencies{
				Store: store, WorkerID: "worker-1", Now: func() time.Time { return now }, ControlLeaderTTL: time.Minute,
				Observer: observability.NopObserver{}, Reconcile: reconciler, ContentScopes: noContentScopes,
			}}
			runtime.PublishContentScopes(context.Background(), test.changes)
			if test.wantScope == "" {
				if len(store.decisions) != 0 {
					t.Fatalf("published %+v, want nothing", store.decisions)
				}
				return
			}
			if len(store.decisions) != 1 {
				t.Fatalf("published %+v, want exactly one scope decision", store.decisions)
			}
			decision := store.decisions[0]
			if decision.QueryGroup != "query-group-1" || decision.ContentScope != test.wantScope || decision.WithdrawContentScope ||
				decision.DesiredWorkerID != "worker-1" || decision.PlacementReason != ownership.PlacementRebalance || decision.ExpectedRecordRevision != 7 {
				t.Fatalf("decision = %+v, want the record's placement kept, revision 7 expected and scope %q", decision, test.wantScope)
			}
		})
	}
}

// A round sweeps the retired Assignment records when something could have
// been left behind since the last sweep -- the first round of a term, or a
// Query Group of the last swept set gone -- and not otherwise: the sweep
// walks every record, and a round whose set only grew has left nothing.
func TestARoundSweepsRetiredAssignmentsOnlyWhenARecordCouldHaveBeenLeftBehind(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &fakePhaseTwoOwnershipStore{now: now}
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Now: func() time.Time { return now }, Observer: observability.NopObserver{},
	}}
	authority := func(epoch uint64) ownership.PublicationAuthority {
		return ownership.PublicationAuthority{Fence: execution.OwnerFence{
			QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "worker-1", OwnerEpoch: epoch, LeaseToken: "t",
		}, Deadline: now.Add(time.Minute)}
	}
	set := func(groups ...execution.QueryGroupIdentity) []execution.QueryGroupIdentity { return groups }

	runtime.sweepRetiredAssignments(context.Background(), authority(1), set("a", "b"))
	if len(store.sweeps) != 1 {
		t.Fatalf("first round of a term swept %d times, want once", len(store.sweeps))
	}
	runtime.sweepRetiredAssignments(context.Background(), authority(1), set("a", "b"))
	runtime.sweepRetiredAssignments(context.Background(), authority(1), set("a", "b", "c"))
	if len(store.sweeps) != 1 {
		t.Fatalf("rounds with the same or a grown set swept; %d sweeps, want still 1", len(store.sweeps))
	}
	runtime.sweepRetiredAssignments(context.Background(), authority(1), set("a", "c"))
	if len(store.sweeps) != 2 {
		t.Fatalf("a round that lost b swept %d times in total, want 2", len(store.sweeps))
	}
	if _, kept := store.sweeps[1]["b"]; kept {
		t.Fatal("the sweep was asked to keep the Query Group that left")
	}
	if _, kept := store.sweeps[1]["c"]; !kept {
		t.Fatal("the sweep was not asked to keep a Query Group the round runs")
	}
	runtime.sweepRetiredAssignments(context.Background(), authority(2), set("a", "c"))
	if len(store.sweeps) != 3 {
		t.Fatalf("a new term swept %d times in total, want 3", len(store.sweeps))
	}
}

// The round's census reaches the fleet snapshot with the policy in the
// fleet's words -- every policy has one, and an unknown one reads as
// untouched -- and as a copy the round's next write does not reach into.
func TestTheRoundsAssignmentScopeCensusIsKeptForTheFleetInTheFleetsWords(t *testing.T) {
	for policy, word := range map[scheduler.ContentScopePolicy]string{
		scheduler.ContentScopesDeclared:  fleet.AssignmentScopePolicyDeclared,
		scheduler.ContentScopesWithdrawn: fleet.AssignmentScopePolicyWithdrawn,
		scheduler.ContentScopesUntouched: fleet.AssignmentScopePolicyUntouched,
		scheduler.ContentScopePolicy(99): fleet.AssignmentScopePolicyUntouched,
	} {
		if got := assignmentScopePolicyWord(policy); got != word {
			t.Fatalf("assignmentScopePolicyWord(%v) = %q, want %q", policy, got, word)
		}
	}
	runtime := &productionPhaseTwoOwnership{}
	if runtime.LastAssignmentScope() != nil || (*productionPhaseTwoOwnership)(nil).LastAssignmentScope() != nil {
		t.Fatal("a runtime that has not reconciled claims a census")
	}
	at := time.Unix(1_700_000_000, 0)
	scopes := scheduler.ContentScopes{Policy: scheduler.ContentScopesDeclared, Digests: map[execution.QueryGroupIdentity]string{"qg-1": "c1", "qg-2": "c2"}}
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-1": {QueryGroup: "qg-1", ContentScope: "c1"},
		"qg-2": {QueryGroup: "qg-2"},
	}
	runtime.recordAssignmentScope(at, scopes, records)
	first := runtime.LastAssignmentScope()
	if first == nil || first.Policy != fleet.AssignmentScopePolicyDeclared || first.Total != 2 || first.Current != 1 || first.Undeclared != 1 || !first.At.Equal(at) || !first.Consistent() {
		t.Fatalf("census = %+v, want a declaring round over one current and one undeclared record", first)
	}
	// What a reader is handed is its own: writing on it does not reach the
	// runtime's census.
	first.Current = 99
	if runtime.LastAssignmentScope().Current != 1 {
		t.Fatal("a reader's copy wrote through to the runtime's census")
	}
	first.Current = 1
	records["qg-2"] = ownership.AssignmentRecord{QueryGroup: "qg-2", ContentScope: "c2"}
	runtime.recordAssignmentScope(at.Add(time.Second), scopes, records)
	if first.Current != 1 {
		t.Fatalf("the copy a reader holds moved with the next round: %+v", first)
	}
	if second := runtime.LastAssignmentScope(); second.Current != 2 || second.Undeclared != 0 || !second.At.After(first.At) {
		t.Fatalf("second census = %+v, want both current", second)
	}
}

// The last sweep is kept for the fleet with its numbers, success or failure:
// a sweep that ran and reclaimed six records on a live deployment was known
// only to the Pod. Before any sweep there is nothing; after one, the five
// numbers and when; after one that failed, the failure's word beside the
// numbers it got that far with; and the copy a reader holds is its own.
func TestTheLastSweepIsKeptForTheFleetWithItsNumbersAndItsFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &fakePhaseTwoOwnershipStore{now: now, sweep: ownership.AssignmentSweep{Scanned: 2407, Retired: 6, Reclaimed: 5, HeldByLease: 1, Duration: 40 * time.Millisecond}}
	var observed []observability.Observation
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Now: func() time.Time { return now },
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observation)
		}),
	}}
	authority := func(epoch uint64) ownership.PublicationAuthority {
		return ownership.PublicationAuthority{Fence: execution.OwnerFence{
			QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "worker-1", OwnerEpoch: epoch, LeaseToken: "t",
		}, Deadline: now.Add(time.Minute)}
	}
	if runtime.LastAssignmentSweep() != nil || (*productionPhaseTwoOwnership)(nil).LastAssignmentSweep() != nil {
		t.Fatal("a runtime that has not swept claims a sweep")
	}
	runtime.sweepRetiredAssignments(context.Background(), authority(1), []execution.QueryGroupIdentity{"a", "b"})
	first := runtime.LastAssignmentSweep()
	want := fleet.AssignmentSweepFacts{At: now, Result: "success", Scanned: 2407, Retired: 6, Reclaimed: 5, HeldByLease: 1, DurationSeconds: 0.04}
	if first == nil || *first != want || !first.Consistent() {
		t.Fatalf("sweep facts = %+v, want %+v", first, want)
	}
	// The line carries the same five numbers.
	if len(observed) != 1 || observed[0].Stage != observability.StageAssignmentSwept || observed[0].AssignmentSweep == nil ||
		observed[0].AssignmentSweep.Scanned != 2407 || observed[0].AssignmentSweep.Reclaimed != 5 {
		t.Fatalf("observation = %+v, want assignment_swept with the sweep's numbers", observed)
	}
	// A reader's copy is its own.
	first.Reclaimed = 99
	if runtime.LastAssignmentSweep().Reclaimed != 5 {
		t.Fatal("a reader's copy wrote through to the runtime's sweep")
	}
	// A new term sweeps again and fails: the fleet reads the failure's word
	// and the numbers the sweep got that far with.
	store.sweepErr, store.sweep = ownership.ErrStaleFence, ownership.AssignmentSweep{Scanned: 1200}
	runtime.sweepRetiredAssignments(context.Background(), authority(2), []execution.QueryGroupIdentity{"a", "b"})
	failed := runtime.LastAssignmentSweep()
	if failed == nil || failed.Result != "failed" || failed.Reason == "" || failed.Reason == "none" || failed.Scanned != 1200 {
		t.Fatalf("failed sweep facts = %+v, want failed with a reason word and the partial scan", failed)
	}
	if string(observed[len(observed)-1].ReasonCode) != failed.Reason {
		t.Fatalf("fleet reason %q differs from the line's %q", failed.Reason, observed[len(observed)-1].ReasonCode)
	}
}
