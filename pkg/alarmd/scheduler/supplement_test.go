// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// observedSlotCatalog is the fake catalog offering the observed freeze.
type observedSlotCatalog struct {
	*fakeSlotCatalog
	observed int
}

func (catalog *observedSlotCatalog) FreezeObservedSlotContract(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	catalog.observed++
	return catalog.fakeSlotCatalog.FreezeSlotContract(ctx, request)
}

// A completed Slot is frozen again for a supplement exactly as the Slot was
// frozen - the same contract, due Plans and boundaries - under the fence
// and content it runs under now, with the supplement operation and no
// recovery facts.
func TestASupplementFreezesItsSlotAsTheSlotWasFrozen(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	at := time.Unix(200, 0)
	slotCatalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	slot, due, _, err := newProductionSlotSourceForTest(t, slotCatalog, foundProgress(120, 60), at).Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	catalog := &observedSlotCatalog{fakeSlotCatalog: &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}}
	source := mustProductionSlotSource(t, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
		&fakeProgressReader{result: foundProgress(180, 120), catalog: catalog.fakeSlotCatalog}, at.Add(time.Minute))
	supplement, err := source.FreezeSupplement(context.Background(), 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	if supplement.Contract != slot.Contract || supplement.EarliestQueryDeadlineUnixMilli != slot.EarliestQueryDeadlineUnixMilli ||
		supplement.RecoveryUntilUnixMilli != slot.RecoveryUntilUnixMilli || supplement.KeepUntilUnixMilli != slot.KeepUntilUnixMilli ||
		supplement.DuePlanTargets.DuePlanSetDigest != slot.DuePlanTargets.DuePlanSetDigest || supplement.ExpectedNextSlot != 120 {
		t.Fatalf("supplement slot %+v, want the Slot as frozen %+v", supplement, slot)
	}
	if supplement.Dispatch.Operation != execution.OperationSupplement || supplement.Dispatch.OwnerFence != testFence(7) ||
		supplement.Recovery != (SlotRecoveryFacts{}) {
		t.Fatalf("dispatch %+v recovery %+v", supplement.Dispatch, supplement.Recovery)
	}
	// Asked with the observed freeze: a completed Slot whose content is gone
	// is not rebuilt from the published catalog.
	if catalog.observed != 1 {
		t.Fatalf("observed freezes %d, want the supplement frozen from the Segment's retained content", catalog.observed)
	}
}

// A Slot past its keep boundary, or whose Segment or content is no longer
// held, is past being supplemented, by name.
func TestASupplementOfASlotNoLongerKeptIsExpired(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	kept, err := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 120), time.Unix(200, 0)).FreezeSupplement(context.Background(), 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	past := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 120), time.UnixMilli(kept.KeepUntilUnixMilli))
	if _, err := past.FreezeSupplement(context.Background(), 120, 0); !errors.Is(err, ErrSupplementContractExpired) {
		t.Fatalf("at the keep boundary: %v, want expired", err)
	}
	for _, gone := range []error{controlplane.ErrCatalogObjectUnavailable, controlplane.ErrScheduleUnavailable, controlplane.ErrSnapshotUnavailable} {
		catalog.freezeErr = gone
		if _, err := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 120), time.Unix(200, 0)).FreezeSupplement(context.Background(), 120, 0); !errors.Is(err, ErrSupplementContractExpired) {
			t.Fatalf("freeze failing with %v: %v, want expired", gone, err)
		}
	}
	catalog.freezeErr = errors.New("redis: connection refused")
	if _, err := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 120), time.Unix(200, 0)).FreezeSupplement(context.Background(), 120, 0); err == nil || errors.Is(err, ErrSupplementContractExpired) {
		t.Fatalf("a failed read: %v, want it returned as it came", err)
	}
}

// supplementSource is a Slot source that freezes one Slot for a supplement.
type supplementSource struct {
	fakeSlotSource
	slot   FrozenSlot
	err    error
	frozen []execution.EvaluationTime
}

func (source *supplementSource) FreezeSupplement(_ context.Context, at execution.EvaluationTime, _ int64) (FrozenSlot, error) {
	source.frozen = append(source.frozen, at)
	return source.slot, source.err
}

type supplementExecutor struct {
	requests []execution.SlotExecutionRequest
	facts    execution.SupplementFacts
	busy     func() bool
}

func (executor *supplementExecutor) Execute(_ context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.requests = append(executor.requests, request)
	if executor.busy != nil && executor.busy() {
		return execution.SlotExecutionResult{}, errors.New("the flight was not held during the supplement")
	}
	facts := executor.facts
	return execution.SlotExecutionResult{Supplement: &facts}, nil
}

func supplementSlot(t *testing.T) FrozenSlot {
	t.Helper()
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	slot, err := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 120), time.Unix(200, 0)).FreezeSupplement(context.Background(), 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	return slot
}

// A supplement runs under the Query Group's flight, held for as long as it
// runs, with the supplement operation, its scope, the Slot as frozen again
// and the fence current now; it returns what the execution counted.
func TestASupplementRunsUnderTheFlightWithTheSlotFrozenAgain(t *testing.T) {
	slot := supplementSlot(t)
	flights := NewFlightCoordinator()
	source := &supplementSource{slot: slot}
	executor := &supplementExecutor{facts: execution.SupplementFacts{Candidates: 2, Admitted: 2, Points: 2}}
	executor.busy = func() bool {
		release, acquired := flights.TryMaintenance("query-group-1")
		if acquired {
			release()
		}
		return acquired
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, source, executor, flights, func() time.Time { return time.Unix(260, 0) })
	if err != nil {
		t.Fatal(err)
	}
	scope := execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a", "b"}}
	facts, err := runner.Supplement(context.Background(), 120, 0, scope)
	if err != nil || facts != executor.facts {
		t.Fatalf("facts %+v error %v", facts, err)
	}
	if len(source.frozen) != 1 || source.frozen[0] != 120 || len(executor.requests) != 1 {
		t.Fatalf("frozen %v requests %d", source.frozen, len(executor.requests))
	}
	request := executor.requests[0]
	if request.Operation != execution.OperationSupplement || request.Supplement == nil || len(request.Supplement.Series) != 2 ||
		request.Contract != slot.Contract || request.OwnerFence != testFence(7) || request.AttemptNo != 1 ||
		request.ExpectedNextSlot != 120 || request.KeepUntilUnixMilli != slot.KeepUntilUnixMilli {
		t.Fatalf("request %+v", request)
	}
	if release, acquired := flights.TryMaintenance("query-group-1"); !acquired {
		t.Fatal("the flight was not released after the supplement")
	} else {
		release()
	}
}

// A supplement never waits behind the Query Group's executing Slot: it is
// refused at once, by name, and nothing is frozen or executed.
func TestASupplementDoesNotWaitForAnExecutingSlot(t *testing.T) {
	flights := NewFlightCoordinator()
	release, _ := flights.TryMaintenance("query-group-1")
	defer release()
	source := &supplementSource{slot: supplementSlot(t)}
	executor := &supplementExecutor{}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, source, executor, flights, func() time.Time { return time.Unix(260, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Supplement(context.Background(), 120, 0, execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}}); !errors.Is(err, ErrSupplementFlightBusy) ||
		len(source.frozen) != 0 || len(executor.requests) != 0 {
		t.Fatalf("error %v frozen %v requests %d", err, source.frozen, len(executor.requests))
	}
}

// A supplement's guard is asked once the supplement holds the flight, and
// no earlier; told the Query Group moved on, the supplement gives the
// flight back having frozen and executed nothing. Told it has not, it runs.
func TestASupplementAsksItsGuardUnderTheFlight(t *testing.T) {
	for _, proceed := range []bool{false, true} {
		flights := NewFlightCoordinator()
		source := &supplementSource{slot: supplementSlot(t)}
		executor := &supplementExecutor{}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, source, executor, flights, func() time.Time { return time.Unix(260, 0) })
		if err != nil {
			t.Fatal(err)
		}
		heldAtAsk := ""
		ctx := WithSupplementGuard(context.Background(), func() bool {
			heldAtAsk, _ = flights.FlightHeld("query-group-1")
			return proceed
		})
		_, err = runner.Supplement(ctx, 120, 0, execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}})
		if heldAtAsk != FlightHeldBySupplement {
			t.Fatalf("proceed %v: the guard was asked with the flight held by %q, want the supplement", proceed, heldAtAsk)
		}
		if !proceed && (!errors.Is(err, ErrSupplementOvertaken) || len(source.frozen) != 0 || len(executor.requests) != 0) {
			t.Fatalf("an overtaken supplement: error %v frozen %v requests %d, want refused with nothing done", err, source.frozen,
				len(executor.requests))
		}
		if proceed && (err != nil || len(executor.requests) != 1) {
			t.Fatalf("a supplement its guard let through: error %v requests %d", err, len(executor.requests))
		}
		if _, held := flights.FlightHeld("query-group-1"); held {
			t.Fatalf("proceed %v: the flight is still held after the supplement", proceed)
		}
	}
}

// A fence that moved since the Slot was frozen again is refused before
// anything is executed; a source that cannot freeze completed Slots is
// refused by name.
func TestASupplementIsRefusedAMovedFenceOrAnUnsupportedSource(t *testing.T) {
	source := &supplementSource{slot: supplementSlot(t)}
	executor := &supplementExecutor{}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(8)}, source, executor, NewFlightCoordinator(), func() time.Time { return time.Unix(260, 0) })
	if err != nil {
		t.Fatal(err)
	}
	scope := execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}}
	if _, err := runner.Supplement(context.Background(), 120, 0, scope); !errors.Is(err, ErrSlotOwnershipChanged) || len(executor.requests) != 0 {
		t.Fatalf("moved fence: error %v requests %d", err, len(executor.requests))
	}
	plain, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, &fakeSlotSource{}, executor, NewFlightCoordinator(), func() time.Time { return time.Unix(260, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Supplement(context.Background(), 120, 0, scope); !errors.Is(err, ErrSupplementUnsupported) {
		t.Fatalf("unsupported source: %v", err)
	}
}

// driftingSlotCatalog freezes a contract that names another snapshot than
// the Segment it was frozen from.
type driftingSlotCatalog struct{ *fakeSlotCatalog }

func (catalog driftingSlotCatalog) FreezeObservedSlotContract(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	fact, err := catalog.fakeSlotCatalog.FreezeSlotContract(ctx, request)
	fact.Contract.SnapshotRevision = "snapshot-2"
	return fact, err
}

// A contract frozen again that is not the Segment's is not the Slot's
// contract, and nothing is supplemented under it.
func TestASupplementRefusesAContractThatDriftedFromItsSegment(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := driftingSlotCatalog{&fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}}
	source := mustProductionSlotSource(t, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
		&fakeProgressReader{result: foundProgress(180, 120), catalog: catalog.fakeSlotCatalog}, time.Unix(200, 0))
	if _, err := source.FreezeSupplement(context.Background(), 120, 0); !errors.Is(err, ErrSlotContractDrift) {
		t.Fatalf("drifted contract: %v, want ErrSlotContractDrift", err)
	}
}

// blockingSupplementExecutor holds a supplement in execution until released.
type blockingSupplementExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (executor *blockingSupplementExecutor) Execute(_ context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	close(executor.started)
	<-executor.release
	return execution.SlotExecutionResult{Supplement: &execution.SupplementFacts{}}, nil
}

// A supplement and its Query Group's Slot never run at once. The supplement
// writes its Slot's events and then its State from a view read before it
// wrote; a Slot of the same Query Group writing the same series in between
// would leave events with no State behind them. So while a supplement runs,
// the Slot is refused its flight - it is dispatched again later, as for any
// Slot in flight - and while a Slot runs, the supplement is refused its own.
func TestASupplementAndItsQueryGroupsSlotNeverRunAtOnce(t *testing.T) {
	source := &supplementSource{slot: supplementSlot(t)}
	executor := &blockingSupplementExecutor{started: make(chan struct{}), release: make(chan struct{})}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, source, executor, NewFlightCoordinator(),
		func() time.Time { return time.Unix(260, 0) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runner.Supplement(context.Background(), 120, 0, execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}})
		done <- err
	}()
	<-executor.started
	_, _, err = runner.RunOne(context.Background())
	var inFlight *SlotInFlightError
	if !errors.Is(err, ErrSlotInFlight) || source.calls != 0 {
		t.Fatalf("a Slot ran beside the supplement: error %v, source calls %d", err, source.calls)
	}
	if !errors.As(err, &inFlight) || inFlight.HeldBy != FlightHeldBySupplement {
		t.Fatalf("the Slot was told %v, want the supplement named as what held the flight", err)
	}
	close(executor.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
