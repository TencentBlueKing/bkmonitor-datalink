// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// freshFrozenRenewals is what a state double that is not about renewals
// answers: one item per requested series, nothing renewed.
//
// Shared rather than repeated per double so that a double cannot drift into
// answering for a different set of series than it was asked about, which is
// the one thing the caller validates.
func freshFrozenRenewals(request execution.FrozenStateRenewalRequest) execution.FrozenStateRenewalResult {
	result := execution.FrozenStateRenewalResult{Items: make([]execution.FrozenStateRenewalItem, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.FrozenStateRenewalItem{
			Identity: item.Identity, Outcome: execution.FrozenRenewalFresh,
		}
	}
	return result
}

// The candidate set is the series that were read and are not being written.
//
// Every row here is a way of getting that set wrong that would leave no trace.
// Renewing too few is the defect this change exists to fix, arriving back
// silently: the series keeps evaluating, its key expires, and its history
// restarts as a warming series. Renewing too many costs a command, but the
// rows that would do it are the interesting ones -- a key that is about to be
// written does not need renewing, and a key that is missing or corrupt must
// not be given a longer life.
func TestOnlyTheSeriesReadAndNotWrittenAreCandidatesForRenewal(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "strategy"}
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "sibling"}
	identity := func(owner execution.PlanIdentity, series string) execution.StateKeyIdentity {
		return execution.StateKeyIdentity{Plan: owner, StateGeneration: "generation", SeriesIdentityDigest: execution.SeriesIdentityDigest(series)}
	}
	view := func(owner execution.PlanIdentity, series string, status execution.StateLoadStatus, applied int64) execution.RuntimeStateView {
		return execution.RuntimeStateView{
			Identity: identity(owner, series), Status: status,
			PersistedApplyVersion: execution.ApplyVersion{
				StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(applied), SlotDigest: "slot",
			},
		}
	}
	mutation := func(owner execution.PlanIdentity, series string) execution.StateEvaluation {
		return execution.StateEvaluation{Mutation: execution.StateMutation{Identity: identity(owner, series)}}
	}
	for _, test := range []struct {
		name    string
		loaded  []execution.RuntimeStateView
		results []execution.StateEvaluation
		want    []execution.FrozenSeriesState
	}{
		{
			name:   "a series that was found and is not being written",
			loaded: []execution.RuntimeStateView{view(plan, "frozen", execution.StateFoundReady, 1_788_000_000)},
			want: []execution.FrozenSeriesState{
				{Identity: identity(plan, "frozen"), LastApplied: 1_788_000_000},
			},
		},
		{
			name:    "a series that is being written needs no renewal; its write sets the life",
			loaded:  []execution.RuntimeStateView{view(plan, "writing", execution.StateFoundReady, 1_788_000_000)},
			results: []execution.StateEvaluation{mutation(plan, "writing")},
			want:    nil,
		},
		{
			name: "warming and gapped are found too; their keys age exactly the same way",
			loaded: []execution.RuntimeStateView{
				view(plan, "warming", execution.StateFoundWarming, 1_788_000_001),
				view(plan, "gapped", execution.StateFoundGapped, 1_788_000_002),
			},
			want: []execution.FrozenSeriesState{
				{Identity: identity(plan, "warming"), LastApplied: 1_788_000_001},
				{Identity: identity(plan, "gapped"), LastApplied: 1_788_000_002},
			},
		},
		{
			name:   "a series with no stored key has nothing to keep alive",
			loaded: []execution.RuntimeStateView{view(plan, "new", execution.StateMissingWarming, 0)},
			want:   nil,
		},
		{
			name:   "a corrupt record is not given a longer life",
			loaded: []execution.RuntimeStateView{view(plan, "corrupt", execution.StateDeterministicInvalid, 0)},
			want:   nil,
		},
		{
			name:   "a key that could not be read says nothing about whether it is there",
			loaded: []execution.RuntimeStateView{view(plan, "unread", execution.StateRetryableIO, 0)},
			want:   nil,
		},
		{
			name: "another Plan's series belongs to that Plan's retention, not this one's",
			loaded: []execution.RuntimeStateView{
				view(other, "sibling-series", execution.StateFoundReady, 1_788_000_003),
				view(plan, "frozen", execution.StateFoundReady, 1_788_000_000),
			},
			want: []execution.FrozenSeriesState{
				{Identity: identity(plan, "frozen"), LastApplied: 1_788_000_000},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := frozenSeriesOf(plan, execution.StatePreflightResult{Items: test.loaded}, test.results)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("frozen candidates = %+v, want %+v", got, test.want)
			}
		})
	}
}

// countingFrozenRenewalStore answers renewals and records what it was asked.
type countingFrozenRenewalStore struct {
	requests []execution.FrozenStateRenewalRequest
	outcome  execution.FrozenRenewalOutcome
	err      error
	// shortAnswer drops the last item, which is the shape of a store that
	// answered for fewer series than it was asked about.
	shortAnswer bool
}

func (store *countingFrozenRenewalStore) LoadRuntime(
	context.Context, execution.StatePreflightRequest,
) (execution.StatePreflightResult, error) {
	return execution.StatePreflightResult{}, errors.New("not used")
}

func (store *countingFrozenRenewalStore) AdmitRuntime(
	context.Context, execution.StateApplyRequest,
) (execution.StateAdmissionResult, error) {
	return execution.StateAdmissionResult{}, errors.New("not used")
}

func (store *countingFrozenRenewalStore) ApplyRuntime(
	context.Context, execution.StateApplyRequest,
) (execution.StateApplyResult, error) {
	return execution.StateApplyResult{}, errors.New("not used")
}

func (store *countingFrozenRenewalStore) RenewFrozenRuntime(
	_ context.Context, request execution.FrozenStateRenewalRequest,
) (execution.FrozenStateRenewalResult, error) {
	store.requests = append(store.requests, request)
	if store.err != nil {
		return execution.FrozenStateRenewalResult{}, store.err
	}
	outcome := store.outcome
	if outcome == "" {
		outcome = execution.FrozenRenewalRenewed
	}
	items := request.Items
	if store.shortAnswer && len(items) > 0 {
		items = items[:len(items)-1]
	}
	result := execution.FrozenStateRenewalResult{Items: make([]execution.FrozenStateRenewalItem, len(items))}
	for index, item := range items {
		result.Items[index] = execution.FrozenStateRenewalItem{Identity: item.Identity, Outcome: outcome}
	}
	return result, nil
}

func frozenRenewalFixture(store execution.StateStore, storeMaxItems uint64) *SlotExecutionCoordinator {
	return &SlotExecutionCoordinator{
		ports: Ports{State: store,
			Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
		budget: ProvisionalBudget{MaxStateMutations: 1024, StoreMaxItems: storeMaxItems},
	}
}

func frozenSeriesFixture(count int) []execution.FrozenSeriesState {
	frozen := make([]execution.FrozenSeriesState, count)
	for index := range frozen {
		frozen[index] = execution.FrozenSeriesState{
			Identity: execution.StateKeyIdentity{
				Plan:                 execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "strategy"},
				StateGeneration:      "generation",
				SeriesIdentityDigest: execution.SeriesIdentityDigest(string(rune('a' + index))),
			},
			LastApplied: 1_788_000_000,
		}
	}
	return frozen
}

// A renewal cannot decide whether a Slot succeeds.
//
// The whole point of the mechanism is to remove a class of Slot failures, so a
// mechanism that can itself fail a Slot would be a worse version of the defect.
// The three ways it can go wrong -- the store errors, the store answers for the
// wrong series, the store answers for too few of them -- all have to land on
// the same place: FAILED counted, nothing returned, the Slot carries on.
func TestAFailedRenewalIsCountedAndCannotFailTheSlot(t *testing.T) {
	for _, test := range []struct {
		name  string
		store *countingFrozenRenewalStore
	}{
		{name: "the store could not be reached", store: &countingFrozenRenewalStore{err: errors.New("redis is down")}},
		{name: "the store answered for fewer series than it was asked about",
			store: &countingFrozenRenewalStore{shortAnswer: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := frozenRenewalFixture(test.store, 0)
			var facts observability.FrozenStateRenewalFacts
			frozen := frozenSeriesFixture(3)
			coordinator.renewFrozenState(context.Background(), execution.SlotExecutionRequest{
				Contract: frozenRenewalContract(), Operation: execution.OperationNormal,
			}, frozenRenewalRetention(), frozen, &facts)
			if facts.Failed != len(frozen) || facts.Renewed != 0 || facts.Missing != 0 || facts.Fresh != 0 {
				t.Fatalf("facts = %+v, want every series counted as failed", facts)
			}
			if facts.Frozen != len(frozen) {
				t.Fatalf("population = %d, want the %d series that were asked about; a failure that "+
					"loses the population makes the outcome counts unreadable", facts.Frozen, len(frozen))
			}
		})
	}
}

// One request must not grow with the size of a query group.
//
// The largest group in production carries thousands of series, and a renewal
// that sent all of them in one call would be the same unbounded request the
// apply path is chunked to avoid -- on the Slot that is already the slowest.
func TestRenewalsAreChunkedAtTheStoresPerCallBound(t *testing.T) {
	store := &countingFrozenRenewalStore{}
	coordinator := frozenRenewalFixture(store, 2)
	var facts observability.FrozenStateRenewalFacts
	frozen := frozenSeriesFixture(5)
	coordinator.renewFrozenState(context.Background(), execution.SlotExecutionRequest{
		Contract: frozenRenewalContract(), Operation: execution.OperationNormal,
	}, frozenRenewalRetention(), frozen, &facts)
	sizes := make([]int, len(store.requests))
	asked := make([]execution.StateKeyIdentity, 0, len(frozen))
	for index, request := range store.requests {
		sizes[index] = len(request.Items)
		for _, item := range request.Items {
			asked = append(asked, item.Identity)
		}
	}
	if !reflect.DeepEqual(sizes, []int{2, 2, 1}) {
		t.Fatalf("request sizes = %v, want the store's own per-call bound of two", sizes)
	}
	if len(asked) != len(frozen) {
		t.Fatalf("asked about %d series, want all %d; a chunk that drops series is a renewal that "+
			"silently stops covering the largest groups", len(asked), len(frozen))
	}
	for index, identity := range asked {
		if identity != frozen[index].Identity {
			t.Fatalf("asked about %+v at position %d, want %+v", identity, index, frozen[index].Identity)
		}
	}
	if facts.Renewed != len(frozen) {
		t.Fatalf("renewed = %d, want every chunk's outcomes folded in (%d)", facts.Renewed, len(frozen))
	}
}

// Every request carries the Plan's own retention.
//
// The life a renewal gives a key has to be the life its write would have
// given it. Two derivations of the same number are two numbers, and the one
// that is wrong here gives a key a life shorter than the window it has to
// outlive -- which is the same expiry this change exists to stop, moved.
func TestARenewalCarriesTheSameRetentionTheWriteWould(t *testing.T) {
	store := &countingFrozenRenewalStore{}
	coordinator := frozenRenewalFixture(store, 0)
	var facts observability.FrozenStateRenewalFacts
	retention := frozenRenewalRetention()
	coordinator.renewFrozenState(context.Background(), execution.SlotExecutionRequest{
		Contract: frozenRenewalContract(), Operation: execution.OperationNormal,
	}, retention, frozenSeriesFixture(1), &facts)
	if len(store.requests) != 1 {
		t.Fatalf("requests = %d, want one", len(store.requests))
	}
	if !reflect.DeepEqual(store.requests[0].Retention, retention) {
		t.Fatalf("retention = %+v, want the Plan's own (%+v)", store.requests[0].Retention, retention)
	}
	if store.requests[0].Now.IsZero() {
		t.Fatal("the request carried no instant to measure ages against, so the store cannot tell a " +
			"key that is running out from one that was written a moment ago")
	}
}

// A real Slot asks the store to keep its frozen series alive.
//
// The tests above check the candidate set and the call in isolation, and both
// would still pass if nothing in the Slot ever called either. That is not a
// hypothetical gap: a port wired by every test double and by no production
// path shipped twice on this component, and the only symptom was a capability
// that silently never happened. So this drives finalizePrepared -- the
// function the Slot actually runs -- and reads what reached the store.
func TestASlotAsksTheStoreToKeepItsFrozenSeriesAlive(t *testing.T) {
	// The event sink refuses the first Plan retryably, as the fixture's other
	// users do: the Slot then runs every Plan's state path without reaching a
	// Progress commit, which needs a whole Slot's worth of setup this test has
	// no interest in.
	fixture := newPlanIsolationFixture(t, &retryablePlanEventError{err: errors.New("broker ACK unavailable")})
	healthyPlan := fixture.healthyState.Plan
	frozen := execution.StateKeyIdentity{
		Plan: healthyPlan, StateGeneration: fixture.healthyState.StateGeneration,
		SeriesIdentityDigest: "frozen-series",
	}
	// Found, written some Slots ago, and no mutation this round: a Level held
	// by incomplete inputs, which is the ordinary state of a series whose data
	// source is having a bad hour.
	fixture.loaded.Items = append(fixture.loaded.Items, execution.RuntimeStateView{
		Identity: frozen, Status: execution.StateFoundReady, BlobRevision: 1,
		PersistedApplyVersion: execution.ApplyVersion{
			StateApplyEpoch: 1, EvaluationTime: fixture.request.Contract.Slot.EvaluationTime - 600, SlotDigest: "older-slot",
		},
		VersionComparison: execution.ApplyVersionPersistedOlder,
	})
	// The Plan's other series was found too, and is being written. It is here
	// so that the two halves of the rule are separated by this test and not
	// only by the unit table: without it the written series would be excluded
	// for want of a status, and a candidate set that ignored the writes
	// entirely would still look right.
	for index, view := range fixture.loaded.Items {
		if view.Identity != fixture.healthyState {
			continue
		}
		view.Status, view.BlobRevision = execution.StateFoundReady, 1
		view.PersistedApplyVersion = execution.ApplyVersion{
			StateApplyEpoch: 1, EvaluationTime: fixture.request.Contract.Slot.EvaluationTime - 600, SlotDigest: "older-slot",
		}
		fixture.loaded.Items[index] = view
	}
	// A stored record the write is advancing from, so the mutation reads as a
	// clean advance rather than a conflict.
	for planIndex, planResult := range fixture.evaluated.Plans {
		for stateIndex, stateResult := range planResult.StateResults {
			if stateResult.Mutation.Identity != fixture.healthyState {
				continue
			}
			stateResult.Mutation.ExpectedBlobRevision = 1
			fixture.evaluated.Plans[planIndex].StateResults[stateIndex] = stateResult
		}
	}
	if _, err := fixture.coordinator.finalizePrepared(
		context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	); err != nil {
		t.Fatalf("finalizePrepared() error = %v", err)
	}
	asked := make([]execution.StateKeyIdentity, 0, 1)
	for _, request := range fixture.base.frozenRenewals {
		for _, item := range request.Items {
			asked = append(asked, item.Identity)
		}
	}
	if !reflect.DeepEqual(asked, []execution.StateKeyIdentity{frozen}) {
		t.Fatalf("the Slot asked to renew %+v, want exactly the one series it read and did not write "+
			"(%+v). Too few and the state expires under a Plan that is still evaluating it; too many "+
			"and it is renewing keys its own writes already refreshed", asked, frozen)
	}
	want, err := execution.DeriveStateRetentionRequirement(duePlanForTest(t, fixture, healthyPlan).CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.base.frozenRenewals[0].Retention, want) {
		t.Fatalf("renewal retention = %+v, want the Plan's own (%+v)",
			fixture.base.frozenRenewals[0].Retention, want)
	}
}

func duePlanForTest(t *testing.T, fixture *planIsolationFixture, plan execution.PlanIdentity) execution.DuePlan {
	t.Helper()
	for _, due := range fixture.header.DuePlans {
		if due.Identity == plan {
			return due
		}
	}
	t.Fatalf("the fixture has no due Plan %+v", plan)
	return execution.DuePlan{}
}

func frozenRenewalContract() execution.FrozenExecutionContractRef {
	return execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_060},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_788_000_000, DuePlanSetDigest: "due-set-v1",
	}
}

func frozenRenewalRetention() []execution.StateRetentionRequirement {
	return []execution.StateRetentionRequirement{{LevelID: 1, RetentionPoints: 5, EvaluationInterval: time.Minute}}
}

// A Slot says where its series went even when it froze none of them.
//
// This is the reading the census exists for. A replica whose candidate set was
// computed wrongly and a replica with genuinely nothing frozen both published
// an empty outcome family and no line at all, and separating them took a
// deployment and a wrong estimate. With the census, a Slot that read every
// series it meant to evaluate and wrote them all says so in three numbers, and
// zero frozen beside them is a measurement rather than a silence.
func TestASlotReportsWhereItsSeriesWentEvenWithNothingFrozen(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	// No FOUND views, so nothing is a renewal candidate: the fixture's two
	// series are both written.
	if _, err := fixture.coordinator.finalizePrepared(
		context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	); err != nil && !isProgressContractError(err) {
		t.Fatalf("finalizePrepared() error = %v", err)
	}
	var facts *observability.FrozenStateRenewalFacts
	for _, observation := range fixture.observations {
		if observation.Stage == observability.StageFrozenStateRenewed {
			facts = observation.FrozenStateRenewal
		}
	}
	if facts == nil {
		t.Fatal("the Slot reported no census. A Slot that froze nothing and a Slot whose candidate set " +
			"is wrong both report nothing, and that is the reading this exists to end")
	}
	if facts.Frozen != 0 {
		t.Fatalf("frozen = %d, want none: no view in this fixture was found", facts.Frozen)
	}
	if facts.Due != len(fixture.loaded.Items) || facts.Read != len(fixture.loaded.Items) {
		t.Fatalf("census = %+v, want due and read to be the %d series the Slot had",
			facts, len(fixture.loaded.Items))
	}
	if facts.Written != 2 {
		t.Fatalf("written = %d, want the two series whose mutations were applied", facts.Written)
	}
}

// isProgressContractError lets this test drive the finalizer for its census
// without also standing up a valid Progress commit, which it is not about.
func isProgressContractError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid progress commit")
}
