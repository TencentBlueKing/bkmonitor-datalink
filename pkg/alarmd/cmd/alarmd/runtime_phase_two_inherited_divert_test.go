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
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// slowLookback reads as a lookback group whose longest whole first read
// took 8.4 s: the reading its owner's lookback hands the read hold.
type slowLookback struct{}

func (slowLookback) QueryDuration(execution.QueryGroupIdentity) (time.Duration, bool) {
	return 8400 * time.Millisecond, true
}

func (slowLookback) RereadBound(execution.QueryGroupIdentity) (time.Duration, bool) {
	return 8400 * time.Millisecond, true
}

// inheritedDivertCatalog serves one Query Group's schedule and freezes its
// Slot in the Segment and with the read hold the source asks for.
type inheritedDivertCatalog struct {
	unavailableSlotCatalog
	schedule execution.FrozenQueryGroupSchedule
	fact     execution.FrozenSlotContractFact
}

func (catalog *inheritedDivertCatalog) ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error) {
	return catalog.schedule, nil
}

func (catalog *inheritedDivertCatalog) FreezeSlotContract(_ context.Context, request execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	fact := catalog.fact
	fact.Contract.ScheduleSegmentStart, fact.Contract.ReadHoldMillis = request.ScheduleSegmentStart, request.ReadHoldMillis
	// The hold moves every deadline of the Slot later by itself.
	fact.DuePlans = append([]execution.DuePlan(nil), fact.DuePlans...)
	for index := range fact.DuePlans {
		fact.DuePlans[index].CompletionDeadlineUnixMilli += request.ReadHoldMillis
	}
	fact.Requirements = append([]execution.DataRequirement(nil), fact.Requirements...)
	for index := range fact.Requirements {
		consumers := append([]execution.DataRequirementConsumer(nil), fact.Requirements[index].Consumers...)
		for at := range consumers {
			consumers[at].ConsumerDeadlineUnixMilli += request.ReadHoldMillis
		}
		fact.Requirements[index].Consumers = consumers
	}
	digest, err := execution.DeriveDuePlanSetDigest(fact.DuePlans, fact.Requirements)
	fact.Contract.DuePlanSetDigest = digest
	return fact, err
}

// inheritedTestHolds is a process's read holds over the shared records in
// store, on its own controller: a new controller over the same store is the
// process that takes the group over.
func inheritedTestHolds(t *testing.T, store *runtimeTestReadHoldControl, now func() time.Time) *productionReadHolds {
	t.Helper()
	holds := &productionReadHolds{now: now, groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{},
		degraded: map[string]uint64{}, links: map[string]uint64{}}
	holds.cfg.PhaseTwo.Worker.ID = "worker"
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Now: now, Owner: holds.owner})
	if err != nil {
		t.Fatal(err)
	}
	return holds
}

// inheritedDivertGroup is one Query Group of strategy 101, its Slot frozen
// as the catalog would, and the query deadline that Slot is judged against.
func inheritedDivertGroup(t *testing.T) (execution.FrozenSlotContractFact, execution.FrozenQueryGroupSchedule, int64) {
	t.Helper()
	catalog := productionG4Catalog(t, 101, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"host"},
		[][]map[string]any{{{"method": "gte", "threshold": 80}}})
	fact, schedule := productionFrozenFactFromCatalog(t, catalog)
	deadline := int64(0)
	for _, requirement := range fact.Requirements {
		for _, consumer := range requirement.Consumers {
			if candidate := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec; deadline == 0 || candidate < deadline {
				deadline = candidate
			}
		}
	}
	return fact, schedule, deadline
}

func inheritedDivertSpec(schedule execution.FrozenQueryGroupSchedule) readhold.GroupSpec {
	return readhold.GroupSpec{QueryGroup: schedule.Segment.QueryGroup, Plans: []readhold.PlanRef{{Key: schedule.Plans[0].Key()}},
		SettlingWait: 30 * time.Second, HoldLimit: 10 * time.Minute}
}

// ownerMeasures is the group's previous owner: it holds the group at a
// read hold its record already keeps, its lookback reads the group's query
// at 8.4 s, and it freezes the Slot a minute before the one its successor
// is handed - which writes the reading into the record.
func ownerMeasures(t *testing.T, store *runtimeTestReadHoldControl, schedule execution.FrozenQueryGroupSchedule, at time.Time) {
	t.Helper()
	ctx := context.Background()
	qg := schedule.Segment.QueryGroup
	holds := inheritedTestHolds(t, store, func() time.Time { return at })
	session, err := ownership.OpenSession(ctx, &fakePhaseTwoOwnershipStore{}, qg, "owner", at, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holds.bind(qg, session)
	holds.groups[qg].prepared = schedule.Segment
	if err := holds.controller.Configure(inheritedDivertSpec(schedule)); err != nil {
		t.Fatal(err)
	}
	holds.load(ctx, []execution.QueryGroupIdentity{qg})
	// The lookback's reading, handed as buildLookback hands it.
	measure := slowLookback{}.QueryDuration
	holds.queryDuration.Store(&measure)
	lease, _ := session.Current()
	if _, err := holds.SlotReadHold(ctx, schedule, schedule.Segment.Start, lease.Fence); err != nil {
		t.Fatal(err)
	}
	record, err := readhold.Decode(store.values[qg])
	if err != nil || record.QueryDurationMillis != 9_000 {
		t.Fatalf("the owner's record keeps %d ms (%v), want the 8.4 s reading rounded up to 9 s", record.QueryDurationMillis, err)
	}
}

// successorRound is the process that takes the group over: a new controller
// over the same records, wired as the bundle wires it, restores the group's
// record when it opens it and runs the group's round with seven seconds left
// before its query deadline, which the group's read hold moves later. It
// returns the takeover facts the round said. The round is attempted either
// way: diverted, as a replay, or as the normal round it runs as.
// operationExecutor answers every Slot as rejectingSlotExecutor does and
// keeps the operation each reached it as: a round diverted to replay reaches
// it as a replay, one run where it is as a normal round. Only the executor
// sees which a round was, whether or not anything said so.
type operationExecutor struct {
	mu   sync.Mutex
	seen map[execution.EvaluationTime]execution.Operation
}

func (executor *operationExecutor) Execute(_ context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.seen[request.Contract.Slot.EvaluationTime] = request.Operation
	return execution.SlotExecutionResult{}, errRejectingExecutorReached
}

func successorRound(t *testing.T, store *runtimeTestReadHoldControl, fact execution.FrozenSlotContractFact,
	schedule execution.FrozenQueryGroupSchedule, deadline int64, hold time.Duration,
) ([]observability.ReplayTakeoverFacts, *productionReadHolds, execution.Operation) {
	t.Helper()
	ctx := context.Background()
	qg := schedule.Segment.QueryGroup
	// The Segment the successor finds open starts at the round it is handed,
	// a minute after the Slot its predecessor froze last.
	schedule.Segment.Start = fact.Contract.Slot.EvaluationTime
	fixture := newRenewalTestFixture(t)
	if floor := fixture.production.dependencies.RecoveryLimits.TakeoverDivertFloor; floor != 5*time.Second {
		t.Fatalf("the fallback is %v; the case is about a 5 s fallback", floor)
	}
	fixture.clock.mu.Lock()
	fixture.clock.now = time.UnixMilli(deadline + hold.Milliseconds() - 7_000)
	fixture.clock.mu.Unlock()
	now := fixture.clock.Now()
	fixture.store.assignment = ownership.AssignmentRecord{QueryGroup: qg, DesiredWorkerID: "worker-1", AssignmentGeneration: 1,
		RecordRevision: 1, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now}
	executor := &operationExecutor{seen: map[execution.EvaluationTime]execution.Operation{}}
	fixture.production.dependencies.Executor = executor
	holds := inheritedTestHolds(t, store, fixture.clock.Now)
	fixture.production.dependencies.ReadHolds = holds
	fixture.production.dependencies.InheritedQueryLatency = readHoldInheritedQueryLatency(holds)
	fixture.production.dependencies.Catalog = &inheritedDivertCatalog{schedule: schedule, fact: fact}
	fixture.production.dependencies.Progress = &fakeProductionProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		qg: {Status: execution.ProgressMissing}}}
	runner, err := fixture.production.OpenQueryGroup(ctx, qg, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holds.groups[qg].prepared = schedule.Segment
	if err := holds.controller.Configure(inheritedDivertSpec(schedule)); err != nil {
		t.Fatal(err)
	}
	fixture.production.LoadOpened(ctx, []execution.QueryGroupIdentity{qg})
	// The fixture's executor refuses every Slot that reaches it, and the
	// takeover facts read below are said before that, when the round is
	// classified: its refusal is the round being attempted. Any other error
	// ended the round somewhere the case does not mean.
	if _, attempted, err := runner.RunOne(ctx); !attempted || !errors.Is(err, errRejectingExecutorReached) {
		t.Fatalf("the successor's round: attempted=%v error=%v, want it attempted up to the fixture's executor", attempted, err)
	}
	var facts []observability.ReplayTakeoverFacts
	for _, observation := range fixture.observations() {
		if observation.ReplayTakeover != nil && observation.Trace.EvaluationTime == int64(fact.Contract.Slot.EvaluationTime) {
			facts = append(facts, *observation.ReplayTakeover)
		}
	}
	executor.mu.Lock()
	operation := executor.seen[fact.Contract.Slot.EvaluationTime]
	executor.mu.Unlock()
	return facts, holds, operation
}

// A group whose query takes nine seconds, taken over with seven seconds left
// before its round's query deadline. Its new owner has no reading of its
// own, and by the 5 s fallback alone would start the round and time out.
// The previous owner's lookback reading went into the group's read-hold
// record as it froze its Slot; the new owner reads the record back when it
// opens the group, before the round is classified, and sends the round to
// replay by it, saying the floor was inherited. Without a record there is
// nothing to inherit, and the round runs on the fallback.
func TestATakenOverRoundIsDivertedByTheQueryDurationItsRecordInherited(t *testing.T) {
	fact, schedule, deadline := inheritedDivertGroup(t)
	qg := schedule.Segment.QueryGroup
	store := &runtimeTestReadHoldControl{values: map[execution.QueryGroupIdentity][]byte{}}
	raw, err := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 30_000})
	if err != nil {
		t.Fatal(err)
	}
	store.values[qg] = raw
	ownerMeasures(t, store, schedule, time.Unix(int64(schedule.Segment.Start)+1, 0))

	facts, holds, operation := successorRound(t, store, fact, schedule, deadline, 30*time.Second)
	if got, ok := holds.controller.InheritedQueryDuration(qg); !ok || got != 9*time.Second {
		t.Fatalf("the successor inherited %v (%v), want 9 s", got, ok)
	}
	if len(facts) != 1 || facts[0].Outcome != observability.ReplayTakeoverDiverted ||
		facts[0].FloorSource != observability.TakeoverFloorInherited || facts[0].FloorSeconds != 9 || facts[0].RemainingSeconds != 7 {
		t.Fatalf("takeover facts %+v, want one diverted with 7 s left against the inherited 9 s", facts)
	}
	if operation != execution.OperationReplay {
		t.Fatalf("the diverted round reached the executor as %q, want a replay", operation)
	}

	empty := &runtimeTestReadHoldControl{values: map[execution.QueryGroupIdentity][]byte{}}
	facts, holds, operation = successorRound(t, empty, fact, schedule, deadline, 0)
	if got, ok := holds.controller.InheritedQueryDuration(qg); ok {
		t.Fatalf("a group without a record inherited %v", got)
	}
	for _, got := range facts {
		if got.Outcome == observability.ReplayTakeoverDiverted {
			t.Fatalf("without a record the round was diverted: %+v; the 5 s fallback leaves it 7 s to run", got)
		}
	}
	// A round left to run where it is says nothing of it, so the case reads
	// what the executor was handed: without it, "not diverted" would hold for
	// a round diverted without a word, or never run at all.
	if operation != execution.OperationNormal {
		t.Fatalf("without a record the round reached the executor as %q, want it run as a normal round", operation)
	}
}

// Without read holds there is no inherited reading; a process whose holds
// have no controller has none either.
func TestNoReadHoldsInheritNoQueryDuration(t *testing.T) {
	if readHoldInheritedQueryLatency(nil) != nil {
		t.Fatal("no read holds gave an inherited reading")
	}
	if _, ok := readHoldInheritedQueryLatency(&productionReadHolds{})("qg"); ok {
		t.Fatal("read holds without a controller gave an inherited reading")
	}
}
