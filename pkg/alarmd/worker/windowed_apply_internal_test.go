// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// windowedPorts are the sibling ports with every store call a window makes
// put on one trace, so a case reads the order the calls went in rather than
// each store's own list.
type windowedPorts struct {
	*activationSiblingPorts
	trace []string
}

func (ports *windowedPorts) Sequence(ctx context.Context, scope execution.SequencingScope, run func(context.Context) error) error {
	ports.trace = append(ports.trace, fmt.Sprintf("sequence:%d/%d", len(scope.StateKeys), len(scope.GapKeys)))
	return run(ctx)
}

func (ports *windowedPorts) ApplyGap(ctx context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	ports.trace = append(ports.trace, "gap")
	return ports.activationSiblingPorts.ApplyGap(ctx, request)
}

func (ports *windowedPorts) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	for _, event := range events {
		ports.trace = append(ports.trace, "event:"+event.EventID)
	}
	return ports.activationSiblingPorts.WriteBatch(ctx, events)
}

func (ports *windowedPorts) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	for _, item := range request.Items {
		ports.trace = append(ports.trace, "state:"+string(item.Identity.SeriesIdentityDigest))
	}
	return ports.activationSiblingPorts.ApplyRuntime(ctx, request)
}

func (ports *windowedPorts) RenewFrozenRuntime(ctx context.Context, request execution.FrozenStateRenewalRequest) (execution.FrozenStateRenewalResult, error) {
	for _, item := range request.Items {
		ports.trace = append(ports.trace, "renew:"+string(item.Identity.SeriesIdentityDigest))
	}
	return ports.activationSiblingPorts.RenewFrozenRuntime(ctx, request)
}

// windowedFixture is one Plan's Slot, part way through its evaluation: no
// marker loaded, nothing evaluated yet. A case adds each window's views and
// results the way the evaluation does - one view per evaluated series, its
// result merged beside it - and applies it.
type windowedFixture struct {
	stream  *streamedExecution
	ports   *windowedPorts
	due     execution.DuePlan
	version execution.ApplyVersion
}

func newWindowedFixture(t *testing.T) *windowedFixture {
	t.Helper()
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
	}
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "stable"}
	due := execution.DuePlan{Identity: plan, CompiledPlan: internalCompiledPlan(t, "stable", 1, 60),
		StateGeneration: "stable-generation", StateApplyEpoch: 1, ScheduleRevision: "stable-schedule"}
	version, err := execution.BuildApplyVersion(contractRef, 1)
	if err != nil {
		t.Fatal(err)
	}
	ports := &windowedPorts{activationSiblingPorts: &activationSiblingPorts{contract: contractRef, stablePlan: plan}}
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10}, ports: Ports{
		Activation: ports, Sequencer: ports, Admission: ports, GapGuard: ports, NoData: SharedNoDataStore, Hosts: SharedHostBusiness,
		Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	header := execution.InternalExecutionHeader{Contract: contractRef, DuePlans: []execution.DuePlan{due}}
	gapItems, err := gapPreflightForHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	stream := &streamedExecution{coordinator: coordinator, request: request, header: header, gapItems: gapItems,
		gaps:       execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: due.GapIdentity(), Status: execution.GapMissing}}},
		planSeries: map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]struct{}{plan: {}},
		evaluated: execution.EvaluationResult{Contract: contractRef, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
			Plans: []execution.PlanEvaluationResult{{Plan: plan, Disposition: execution.PlanDecided}}},
	}
	return &windowedFixture{stream: stream, ports: ports, due: due, version: version}
}

func (fixture *windowedFixture) key(series string) execution.StateKeyIdentity {
	return execution.StateKeyIdentity{Plan: fixture.due.Identity, StateGeneration: fixture.due.StateGeneration,
		SeriesIdentityDigest: execution.SeriesIdentityDigest(series)}
}

// written is a series the window writes: its view as read, its mutation and
// its one event.
func (fixture *windowedFixture) written(series string) {
	stream, key := fixture.stream, fixture.key(series)
	fixture.view(execution.RuntimeStateView{Identity: key, VersionComparison: execution.ApplyVersionPersistedOlder})
	stream.evaluated.Plans[0].StateResults = append(stream.evaluated.Plans[0].StateResults, execution.StateEvaluation{
		Mutation: execution.StateMutation{Identity: key, ApplyVersion: fixture.version, MutationDigest: execution.MutationDigest("digest-" + series)},
		Events:   []contract.TriggerEventV1{{EventID: "event-" + series}},
	})
	stream.planSeries[fixture.due.Identity][key.SeriesIdentityDigest] = struct{}{}
}

// frozen is a series the window read, found stored, and does not write.
func (fixture *windowedFixture) frozen(series string) {
	older := fixture.version
	older.EvaluationTime -= 60
	fixture.view(execution.RuntimeStateView{Identity: fixture.key(series), Status: execution.StateFoundReady,
		VersionComparison: execution.ApplyVersionPersistedOlder, PersistedApplyVersion: older, BlobRevision: 1})
	fixture.stream.planSeries[fixture.due.Identity][execution.SeriesIdentityDigest(series)] = struct{}{}
}

func (fixture *windowedFixture) view(view execution.RuntimeStateView) {
	stream := fixture.stream
	stream.state.Items = append(stream.state.Items, view)
	stream.stateItems = append(stream.stateItems, execution.StatePreflightItem{Identity: view.Identity, ApplyVersion: fixture.version})
}

// startWindows runs the head, as the first window that would overfill does.
func (fixture *windowedFixture) startWindows(t *testing.T) {
	t.Helper()
	started, err := fixture.stream.coordinator.applyWindowHead(context.Background(), fixture.stream)
	if err != nil || !started {
		t.Fatalf("head started=%v err=%v, want the Slot cut into windows", started, err)
	}
}

func (fixture *windowedFixture) traceIndex(t *testing.T, entry string) int {
	t.Helper()
	for index, traced := range fixture.ports.trace {
		if traced == entry {
			return index
		}
	}
	t.Fatalf("%q is not in the trace %v", entry, fixture.ports.trace)
	return -1
}

// Each window is its own Sequence of its own State keys and the head one of
// the gap keys: no scope holds both, so none can be nested in another.
func (fixture *windowedFixture) assertScopesApart(t *testing.T) {
	t.Helper()
	for _, entry := range fixture.ports.trace {
		var states, gaps int
		if _, err := fmt.Sscanf(entry, "sequence:%d/%d", &states, &gaps); err != nil {
			continue
		}
		if states != 0 && gaps != 0 {
			t.Fatalf("a Sequence held %d State keys and %d gap keys at once; trace %v", states, gaps, fixture.ports.trace)
		}
	}
}

// P2: a Plan's open statement is derived from every binding the completion
// handed it, so an incomplete input of a series only the last window holds
// opens the marker before the first window's events go out.
func TestAnIncompleteInputOnlyInTheLastWindowOpensTheMarkerBeforeTheFirstWindowsEvents(t *testing.T) {
	fixture := newWindowedFixture(t)
	level := fixture.due.CompiledPlan.Levels().At(0).Definition().LevelID
	consumer := execution.ConsumerRef{Plan: fixture.due.Identity, LevelID: level, HasLevel: true}
	whole := execution.NamedInputBinding{Consumer: consumer, Role: execution.InputRolePrimary,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable}
	partial := whole
	partial.Completeness, partial.Disposition = execution.CompletenessPartial, execution.AccessDegraded
	partial.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	for _, series := range []struct {
		digest  string
		binding execution.NamedInputBinding
	}{{"series-1", whole}, {"series-2", whole}, {"series-3", partial}} {
		fixture.stream.windows.prepared = append(fixture.stream.windows.prepared, preparedSeries{due: &fixture.due,
			identity: execution.SeriesIdentityDigest(series.digest),
			inputs:   []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{series.binding}}}})
	}
	// The first window holds the first series only; the partial one is two
	// windows away and not evaluated yet.
	fixture.written("series-1")
	fixture.startWindows(t)
	if err := fixture.stream.coordinator.applyWindow(context.Background(), fixture.stream, false); err != nil {
		t.Fatal(err)
	}

	guards := fixture.ports.guards
	if len(guards) != 1 || len(guards[0].Scopes) != 1 || guards[0].Scopes[0].Kind != execution.GapOpen ||
		guards[0].Scopes[0].Scope != (execution.GapScope{LevelID: level, HasLevel: true}) ||
		guards[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryPartial) {
		t.Fatalf("head statements = %+v, want one open of the partial input's Level with its reason", guards)
	}
	if gap, event := fixture.traceIndex(t, "gap"), fixture.traceIndex(t, "event:event-series-1"); gap > event {
		t.Fatalf("the open statement went out after the first window's events: %v", fixture.ports.trace)
	}
	if _, opened := fixture.stream.windows.headOpened[fixture.due.Identity]; !opened {
		t.Fatal("the head did not note the Plan it opened, so its tail would write a clear beside the open")
	}
	fixture.assertScopesApart(t)
}

// P1: a window renews the keys it read and does not write, from its own
// views against its own results. Renewed from every view kept so far, the
// second window would renew the first window's frozen key again; and a key
// is never in both an apply and a renewal of one Slot.
func TestEachWindowRenewsItsOwnFrozenSeriesAndNoKeyIsAskedForTwice(t *testing.T) {
	fixture := newWindowedFixture(t)
	fixture.written("series-1")
	fixture.frozen("series-2")
	fixture.startWindows(t)
	if err := fixture.stream.coordinator.applyWindow(context.Background(), fixture.stream, false); err != nil {
		t.Fatal(err)
	}
	fixture.written("series-3")
	fixture.frozen("series-4")
	if err := fixture.stream.coordinator.applyWindow(context.Background(), fixture.stream, true); err != nil {
		t.Fatal(err)
	}

	var renewed [][]execution.StateKeyIdentity
	for _, request := range fixture.ports.frozenRenewals {
		var keys []execution.StateKeyIdentity
		for _, item := range request.Items {
			keys = append(keys, item.Identity)
		}
		renewed = append(renewed, keys)
	}
	if want := [][]execution.StateKeyIdentity{{fixture.key("series-2")}, {fixture.key("series-4")}}; !reflect.DeepEqual(renewed, want) {
		t.Fatalf("renewals = %v, want each window's own frozen key once", renewed)
	}
	if want := []execution.StateKeyIdentity{fixture.key("series-1"), fixture.key("series-3")}; !reflect.DeepEqual(fixture.ports.stateApplied, want) {
		t.Fatalf("State writes = %v, want %v", fixture.ports.stateApplied, want)
	}
	asked := map[execution.StateKeyIdentity]int{}
	for _, key := range fixture.ports.stateApplied {
		asked[key]++
	}
	for _, keys := range renewed {
		for _, key := range keys {
			asked[key]++
		}
	}
	for key, times := range asked {
		if times != 1 {
			t.Fatalf("key %s was in %d of the Slot's apply and renewal requests, want 1", key.SeriesIdentityDigest, times)
		}
	}
	// Each window's events before its State, the second window after the
	// first.
	order := []string{"event:event-series-1", "state:series-1", "event:event-series-3", "state:series-3"}
	for index := 1; index < len(order); index++ {
		if fixture.traceIndex(t, order[index-1]) > fixture.traceIndex(t, order[index]) {
			t.Fatalf("trace %v, want %v in that order", fixture.ports.trace, order)
		}
	}
	fixture.assertScopesApart(t)
}

// A window that would ask for a key the Slot already wrote or renewed is
// refused by name before anything of it is sent: the store would refuse the
// second statement at the same version anyway, and the name says it was the
// window cut.
func TestAWindowAskingForAKeyTheSlotAlreadyRenewedIsRefusedByName(t *testing.T) {
	fixture := newWindowedFixture(t)
	fixture.written("series-1")
	fixture.frozen("series-2")
	fixture.startWindows(t)
	if err := fixture.stream.coordinator.applyWindow(context.Background(), fixture.stream, false); err != nil {
		t.Fatal(err)
	}
	// A window cut through a series: the key the first window renewed comes
	// back, this time to be written.
	fixture.written("series-2")
	events, applied := len(fixture.ports.events), len(fixture.ports.stateApplied)
	err := fixture.stream.coordinator.applyWindow(context.Background(), fixture.stream, true)
	var repeated *StateKeyRepeatedInSlotError
	if !errors.As(err, &repeated) || repeated.Identity != fixture.key("series-2") ||
		repeated.First != slotKeyRenewal || repeated.Again != slotKeyApply {
		t.Fatalf("err = %v, want the repeated key named with the renewal it was first in", err)
	}
	if len(fixture.ports.events) != events || len(fixture.ports.stateApplied) != applied {
		t.Fatalf("the refused window sent events %d->%d and State %d->%d", events, len(fixture.ports.events),
			applied, len(fixture.ports.stateApplied))
	}

	keys := slotStateKeys{}
	key := fixture.key("series-9")
	if err := keys.claim(nil, []execution.StateMutation{{Identity: key}, {Identity: key}}); !errors.As(err, &repeated) {
		t.Fatalf("one part naming a key twice: err = %v, want it refused", err)
	}
}

// First-seen needs the Plan's marker missing (firstSeenActivations), a
// recovery clear needs it found (execution.PlanGapRecoveryMutation), so the
// head can decide first-seen on "no open statement" alone.
func TestFirstSeenAndARecoveryClearAreMutuallyExclusive(t *testing.T) {
	fixture := newWindowedFixture(t)
	due := fixture.due
	fact := execution.PlanActivationFact{Plan: due.Identity, Selection: execution.ActivationCurrent,
		Selected: execution.ActivatedPlan{Identity: due.Identity, StateGeneration: due.StateGeneration, StateApplyEpoch: 1,
			ScheduleRevision: due.ScheduleRevision, RequiredFullSlots: 1, Arrived: true, ForceWarming: true}}
	forced := execution.PlanActivationResult{Contract: fixture.stream.header.Contract, Facts: []execution.PlanActivationFact{fact}}
	unseen := map[execution.PlanKey]struct{}{fact.Key(): {}}
	older := fixture.version
	older.EvaluationTime -= 60
	for _, marker := range []execution.GapGuardSnapshot{
		{Identity: due.GapIdentity(), Status: execution.GapMissing},
		{Identity: due.GapIdentity(), Status: execution.GapFound, MarkerRevision: 1, PersistedApplyVersion: older,
			LastScheduleRevision: due.ScheduleRevision, Scopes: []execution.GapScopeState{{
				Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming), RequiredFullSlots: 1}}},
	} {
		gaps := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{marker}}
		_, firstSeen := firstSeenActivations(forced, gaps, unseen)
		recovery, err := execution.PlanGapRecoveryMutation(fixture.stream.header.Contract, due, gaps)
		if err != nil {
			t.Fatal(err)
		}
		if (len(firstSeen) != 0) == (recovery != nil) {
			t.Fatalf("marker %s: first-seen %v and recovery clear %v, want exactly one", marker.Status, len(firstSeen) != 0, recovery != nil)
		}
		if undecided := firstSeenUndecided(forced, gaps); undecided != (len(firstSeen) != 0) {
			t.Fatalf("marker %s: undecided=%v, want it to follow whether the Plan could be first-seen", marker.Status, undecided)
		}
	}
}

// A Plan written in several windows is decided once its last window is
// written, from its keys counted across them, with the series this attempt
// resumed counted as landed: deciding it per apply would record it from its
// first window while its later keys were not sent.
func TestAPlanWrittenInWindowsIsRecordedAppliedFromEveryWindowAndItsResumedSeries(t *testing.T) {
	fixture := newWindowedFixture(t)
	ctx, recorder := withAppliedPlans(context.Background())
	fixture.stream.noteResumed(fixture.due.Identity)
	fixture.written("series-1")
	fixture.startWindows(t)
	if err := fixture.stream.coordinator.applyWindow(ctx, fixture.stream, false); err != nil {
		t.Fatal(err)
	}
	if len(recorder.unrecorded()) != 0 {
		t.Fatalf("the Plan was recorded after its first window: %v", recorder.unrecorded())
	}
	fixture.written("series-2")
	if err := fixture.stream.coordinator.applyWindow(ctx, fixture.stream, true); err != nil {
		t.Fatal(err)
	}
	if got := recorder.unrecorded(); len(got) != 1 || got[0] != fixture.due.Identity {
		t.Fatalf("applied Plans = %v, want the Plan once every window landed", got)
	}
}

// A retry that finds every series of a Plan already written at this Slot -
// resumed, none evaluated again - sends none of its keys in any window, and
// still records the Plan applied: each of its series is in the store at this
// Slot, which is what the mark vouches for, so the attempt whose Progress
// then fails leaves it for the next. A Plan that neither resumed nor sent
// anything has nothing landed to vouch for and is not recorded. The windows
// are cut by the Slot's other series; the fixture holds the one Plan, so its
// last window carries nothing of it.
func TestAPlanWhoseEverySeriesResumedIsRecordedAppliedAndOneWithNothingIsNot(t *testing.T) {
	for _, resumed := range []int{2, 0} {
		t.Run(fmt.Sprintf("%d resumed", resumed), func(t *testing.T) {
			fixture := newWindowedFixture(t)
			ctx, recorder := withAppliedPlans(context.Background())
			for range resumed {
				fixture.stream.noteResumed(fixture.due.Identity)
			}
			fixture.startWindows(t)
			if err := fixture.stream.coordinator.applyWindow(ctx, fixture.stream, true); err != nil {
				t.Fatal(err)
			}
			if len(fixture.ports.events) != 0 || len(fixture.ports.stateApplied) != 0 {
				t.Fatalf("the window sent %d events and %d State keys, want none", len(fixture.ports.events), len(fixture.ports.stateApplied))
			}
			got := recorder.unrecorded()
			if resumed == 0 {
				if len(got) != 0 {
					t.Fatalf("applied Plans = %v, want none for a Plan with nothing landed", got)
				}
				return
			}
			if len(got) != 1 || got[0] != fixture.due.Identity {
				t.Fatalf("applied Plans = %v, want the Plan whose every series resumed", got)
			}
		})
	}
}
