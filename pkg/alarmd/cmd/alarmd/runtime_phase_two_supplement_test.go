// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// supplementingQueryGroup is an owned Query Group whose Runner answers
// supplements from a script.
type supplementingQueryGroup struct {
	fakePhaseTwoQueryGroup
	answers []error
	calls   []execution.EvaluationTime
	scopes  []execution.SupplementScope
	read    []bool
	// clock, when set, is moved on by took for every call that is not
	// refused for the flight: the time the call held it.
	clock *time.Time
	took  time.Duration
}

func (runner *supplementingQueryGroup) Supplement(ctx context.Context, at execution.EvaluationTime, _ int64, scope execution.SupplementScope) (execution.SupplementFacts, error) {
	runner.calls, runner.scopes = append(runner.calls, at), append(runner.scopes, scope)
	if runner.clock != nil && !errors.Is(runner.answers[0], scheduler.ErrSupplementFlightBusy) {
		*runner.clock = runner.clock.Add(runner.took)
	}
	_, kept := access.KeptReadOf(ctx)
	runner.read = append(runner.read, kept)
	if guard := scheduler.SupplementGuardOf(ctx); guard != nil && !guard() {
		runner.answers = runner.answers[1:]
		return execution.SupplementFacts{}, scheduler.ErrSupplementOvertaken
	}
	err := runner.answers[0]
	runner.answers = runner.answers[1:]
	if err != nil {
		return execution.SupplementFacts{}, err
	}
	return execution.SupplementFacts{Candidates: len(scope.Series), Admitted: len(scope.Series)}, nil
}

func supplementOwnership(runner phaseTwoQueryGroupRuntime) *lookbackOwnership {
	ownership := &lookbackOwnership{}
	ownership.bind(&phaseTwoWorkerBundle{runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
		"qg-late": {runner: runner}}})
	return ownership
}

// A supplement runs on its Query Group's Runner with the late series as its
// scope and the kept read on its context. A Query Group whose Slot is
// executing is tried once more, after half the time its rung has left, and
// then counted flight_busy; a Slot past being supplemented is
// contract_expired, anything else failed, and a Query Group this replica
// does not hold is not run.
func TestTheLookbacksSupplementsRunOnTheirQueryGroupsRunner(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	job := lookback.SupplementJob{QueryGroup: "qg-late", EvaluationTime: 1_799_999_940,
		Series: []execution.SeriesIdentityDigest{"a", "b"}, Read: &lookback.KeptRead{}, Deadline: now.Add(40 * time.Second)}
	for _, tc := range []struct {
		name    string
		answers []error
		want    lookback.SupplementOutcome
		waits   []time.Duration
	}{
		{"ran", []error{nil}, lookback.SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 2, Admitted: 2}}, nil},
		{"busy then ran", []error{scheduler.ErrSupplementFlightBusy, nil},
			lookback.SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 2, Admitted: 2}}, []time.Duration{20 * time.Second}},
		{"busy twice", []error{scheduler.ErrSupplementFlightBusy, scheduler.ErrSupplementFlightBusy},
			lookback.SupplementOutcome{Refused: lookback.DirectedFlightBusy}, []time.Duration{20 * time.Second}},
		{"expired", []error{scheduler.ErrSupplementContractExpired}, lookback.SupplementOutcome{Refused: lookback.DirectedContractExpired}, nil},
		{"failed", []error{errors.New("redis: connection refused")}, lookback.SupplementOutcome{Refused: lookback.DirectedFailed}, nil},
	} {
		runner := &supplementingQueryGroup{answers: tc.answers}
		var waits []time.Duration
		run := lookbackSupplement(supplementOwnership(runner), nil, func() time.Time { return now },
			func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil })
		outcome := run(context.Background(), job)
		if outcome != tc.want || len(runner.answers) != 0 || len(waits) != len(tc.waits) {
			t.Fatalf("%s: outcome %+v answers left %d waits %v", tc.name, outcome, len(runner.answers), waits)
		}
		for index := range waits {
			if waits[index] != tc.waits[index] {
				t.Fatalf("%s: waited %v, want %v", tc.name, waits, tc.waits)
			}
		}
		if runner.calls[0] != job.EvaluationTime || len(runner.scopes[0].Series) != 2 || !runner.read[0] {
			t.Fatalf("%s: calls %v scopes %+v kept read %v", tc.name, runner.calls, runner.scopes, runner.read)
		}
	}
	// Past its rung, a busy Query Group is not tried again.
	runner := &supplementingQueryGroup{answers: []error{scheduler.ErrSupplementFlightBusy}}
	late := job
	late.Deadline = now.Add(-time.Second)
	if outcome := lookbackSupplement(supplementOwnership(runner), nil, func() time.Time { return now },
		func(context.Context, time.Duration) error { t.Fatal("waited past the rung"); return nil })(context.Background(), late); outcome.Refused != lookback.DirectedFlightBusy {
		t.Fatalf("past its rung: %+v", outcome)
	}
	// A Query Group this replica does not hold.
	if outcome := lookbackSupplement(supplementOwnership(runner), nil, func() time.Time { return now }, waitWithin)(
		context.Background(), lookback.SupplementJob{QueryGroup: "qg-elsewhere"}); outcome.Ran || outcome.Refused != lookback.DirectedFailed {
		t.Fatalf("not held: %+v", outcome)
	}
}

// A supplement's guard reaches the Query Group's Runner, and a supplement
// its guard turned back is refused as overtaken, its hold counted.
func TestASupplementsGuardReachesItsRunner(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, proceed := range []bool{true, false} {
		clock := now
		runner := &supplementingQueryGroup{answers: []error{nil}, clock: &clock, took: 30 * time.Millisecond}
		job := lookback.SupplementJob{QueryGroup: "qg-late", EvaluationTime: 120, Series: []execution.SeriesIdentityDigest{"a"},
			Deadline: now.Add(time.Minute), Guard: func() bool { return proceed }}
		outcome := lookbackSupplement(supplementOwnership(runner), nil, func() time.Time { return clock }, waitWithin)(context.Background(), job)
		switch {
		case proceed && !outcome.Ran:
			t.Fatalf("a supplement its guard let through: %+v", outcome)
		case !proceed && (outcome.Ran || outcome.Refused != lookback.SupplementOvertaken || outcome.Held != 30*time.Millisecond):
			t.Fatalf("a supplement its guard turned back: %+v, want refused as overtaken with its hold", outcome)
		}
	}
}

// The permits an early read can count on are the budget less every permit
// held, the lookback's own included.
func TestFreeQueryPermitsLeaveOutTheLookbacksOwn(t *testing.T) {
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(scheduler.RecoveryLimits{
		ProcessQueryPermits: 4, RecoveryQueryPermits: 1, ReadyQueueCapacity: 4, RecoveryQueueCapacity: 4,
		MaxQueuedItemsPerQG: 1, MaxReplaySlots: 1, MaxReplayAge: time.Minute, RetryMinDelay: time.Second, RetryMaxDelay: time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	free := freeQueryPermits(flights)
	if got := free(); got != 4 {
		t.Fatalf("free permits %d with none held, want the budget", got)
	}
	permit, refused := flights.TryAcquireLookbackPermit()
	if refused != "" {
		t.Fatal(refused)
	}
	defer permit.Release()
	if got := free(); got != 3 {
		t.Fatalf("free permits %d with the lookback holding one, want one less", got)
	}
}

// The process's lookback hands its late series to supplements: without the
// executor wired in, it reads no Query Group directed at all.
func TestTheLookbackIsWiredToRunSupplements(t *testing.T) {
	if options := lookbackOptions(nil, nil, &lookbackOwnership{}, nil, time.Now, nil); options.Supplement == nil {
		t.Fatal("the lookback has no supplement to hand late series to")
	}
}

// observingCatalog records which freeze it was asked for, and the hint the
// gate put on the read.
type observingCatalog struct {
	productionPhaseTwoSlotCatalog
	observed, plain int
	hints           []uint64
}

func (catalog *observingCatalog) FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	catalog.plain++
	return execution.FrozenSlotContractFact{}, nil
}

func (catalog *observingCatalog) FreezeObservedSlotContract(ctx context.Context, _ execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	catalog.observed++
	catalog.hints = append(catalog.hints, controlplane.TimelineRevisionHint(ctx))
	return execution.FrozenSlotContractFact{}, nil
}

// plainCatalog offers the ordinary freeze only.
type plainCatalog struct {
	productionPhaseTwoSlotCatalog
	plain int
}

func (catalog *plainCatalog) FreezeSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	catalog.plain++
	return execution.FrozenSlotContractFact{}, nil
}

// A supplement's freeze goes through the gate a Slot's does, and reaches the
// observed freeze of a catalog that offers it - the ordinary freeze of one
// that does not.
func TestTheGatedCatalogPassesASupplementsFreezeThroughTheGate(t *testing.T) {
	gate := newViewExecutionGate()
	gate.attach(mapView{"qg-1": viewstream.Entry{QueryGroup: "qg-1", Content: &viewstream.Content{ObjectDigest: "obj-a"},
		Assignment: viewstream.Assignment{DesiredWorkerID: "w1", Revision: 3, ContentScope: "obj-a", TimelineRecordRevision: 12}}})
	session := openViewGateTestSession(t, newViewGateTestStore(t), "qg-1", 12, "obj-a")
	observing := &observingCatalog{}
	catalog := &viewGatedCatalog{next: observing, gate: gate, queryGroup: "qg-1", session: session}
	if _, err := catalog.FreezeObservedSlotContract(context.Background(), execution.FreezeSlotContractRequest{}); err != nil ||
		observing.observed != 1 || observing.plain != 0 || len(observing.hints) != 1 || observing.hints[0] != 12 {
		t.Fatalf("error %v observed %d plain %d hints %v", err, observing.observed, observing.plain, observing.hints)
	}
	plain := &plainCatalog{}
	catalog = &viewGatedCatalog{next: plain, gate: gate, queryGroup: "qg-1", session: session}
	if _, err := catalog.FreezeObservedSlotContract(context.Background(), execution.FreezeSlotContractRequest{}); err != nil || plain.plain != 1 {
		t.Fatalf("error %v plain %d, want the ordinary freeze", err, plain.plain)
	}
}

// freezingSource is a production Slot source that freezes completed Slots.
type freezingSource struct {
	frozen []execution.EvaluationTime
}

func (source *freezingSource) Next(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
	return scheduler.FrozenSlot{}, false, scheduler.SlotDueFacts{}, nil
}

func (source *freezingSource) FreezeSupplement(_ context.Context, at execution.EvaluationTime, _ int64) (scheduler.FrozenSlot, error) {
	source.frozen = append(source.frozen, at)
	return scheduler.FrozenSlot{ExpectedNextSlot: at}, nil
}

// The observed production Slot source freezes a supplement's Slot through
// the source it wraps, and names a source that cannot.
func TestTheObservedSlotSourcePassesASupplementsFreezeThrough(t *testing.T) {
	next := &freezingSource{}
	if slot, err := (observedProductionSlotSource{next: next}).FreezeSupplement(context.Background(), 120, 0); err != nil ||
		slot.ExpectedNextSlot != 120 || len(next.frozen) != 1 {
		t.Fatalf("slot %+v error %v frozen %v", slot, err, next.frozen)
	}
	if _, err := (observedProductionSlotSource{next: &fakeSlotSourceOnly{}}).FreezeSupplement(context.Background(), 120, 0); !errors.Is(err, scheduler.ErrSupplementUnsupported) {
		t.Fatalf("a source that cannot freeze completed Slots: %v", err)
	}
}

type fakeSlotSourceOnly struct{}

func (fakeSlotSourceOnly) Next(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
	return scheduler.FrozenSlot{}, false, scheduler.SlotDueFacts{}, nil
}

// A supplement that took its Query Group's flight says how long it held
// it, whatever it came to; one refused for the flight held nothing, and a
// retry after one counts only the call that ran.
func TestALookbackSupplementSaysHowLongItHeldTheFlight(t *testing.T) {
	job := lookback.SupplementJob{QueryGroup: "qg-late", EvaluationTime: 1_799_999_940,
		Series: []execution.SeriesIdentityDigest{"a"}, Read: &lookback.KeptRead{}}
	for _, tc := range []struct {
		name    string
		answers []error
		held    time.Duration
	}{
		{"ran", []error{nil}, 2 * time.Second},
		{"expired", []error{scheduler.ErrSupplementContractExpired}, 2 * time.Second},
		{"failed", []error{errors.New("redis: connection refused")}, 2 * time.Second},
		{"busy then ran", []error{scheduler.ErrSupplementFlightBusy, nil}, 2 * time.Second},
		{"busy twice", []error{scheduler.ErrSupplementFlightBusy, scheduler.ErrSupplementFlightBusy}, 0},
	} {
		now := time.Unix(1_800_000_000, 0)
		job.Deadline = now.Add(40 * time.Second)
		runner := &supplementingQueryGroup{answers: tc.answers, clock: &now, took: 2 * time.Second}
		outcome := lookbackSupplement(supplementOwnership(runner), nil, func() time.Time { return now },
			func(context.Context, time.Duration) error { return nil })(context.Background(), job)
		if outcome.Held != tc.held {
			t.Errorf("%s: held %v, want %v", tc.name, outcome.Held, tc.held)
		}
	}
}
