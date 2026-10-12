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
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func noRecheck(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	return execution.ProviderCompletion{}, nil
}

// Nothing configures the lookback and it needs no share of memory: built,
// it runs, counting every source a query can be compiled from, and every
// reason the scheduler refuses a permit with, from the start.
func TestTheLookbackRunsWithoutConfiguration(t *testing.T) {
	owner := &lookbackOwnership{}
	engine, standing, err := buildLookback(noRecheck, nil, owner, nil, time.Now, nil)
	if err != nil || engine == nil || standing != (lookbackStanding{Running: true}) {
		t.Fatalf("built: %v %+v %v", engine, standing, err)
	}
	stats := engine.Stats()
	for _, source := range controlplane.SupportedSourceSemantics {
		if entry, present := stats.Sources[source]; !present || len(entry.Samples) != len(lookback.SampleOutcomes) {
			t.Fatalf("source %s is not counted from the start: %+v", source, entry)
		}
	}
	for _, reason := range append([]string{lookback.RefusedOther}, scheduler.LookbackRefusals...) {
		if count, present := stats.PermitRefusals[reason]; !present || count != 0 {
			t.Fatalf("refusal %q is not counted from the start: %v", reason, stats.PermitRefusals)
		}
	}
	if stats.Coverage.Owned != 0 {
		t.Fatalf("an unbound bundle owns %d Query Groups", stats.Coverage.Owned)
	}
	// The refusals are the scheduler's, and the first samples are spread.
	if options := lookbackOptions(noRecheck, nil, owner, nil, time.Now, nil); !slices.Equal(options.Refusals, scheduler.LookbackRefusals) ||
		options.UnspreadFirstSamples {
		t.Fatalf("refusals %v, unspread %v", options.Refusals, options.UnspreadFirstSamples)
	}
	// Its series tables grow under the memory line it is given.
	var asked uint64
	if options := lookbackOptions(noRecheck, nil, owner, nil, time.Now, func(bytes uint64) bool { asked = bytes; return false }); options.Memory == nil ||
		options.Memory(7) || asked != 7 {
		t.Fatalf("the lookback's memory is not the line it was given (asked %d)", asked)
	}
}

// The lookback's permit is the scheduler's: a refusal carries its reason,
// and a granted permit yields the moment a formal query has to wait.
func TestTheLookbackPermitYieldsToAWaitingFormalQuery(t *testing.T) {
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(scheduler.RecoveryLimits{
		ProcessQueryPermits: 2, RecoveryQueryPermits: 1,
		ReadyQueueCapacity: 8, RecoveryQueueCapacity: 8,
		MaxQueuedItemsPerQG: 2,
		MaxReplaySlots:      3, MaxReplayAge: 10 * time.Minute,
		RetryMinDelay: time.Second, RetryMaxDelay: 8 * time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	permit := lookbackPermit(flights)
	release, yield, refused := permit()
	if release == nil || yield == nil || refused != "" {
		t.Fatalf("an idle pool gave the lookback %v %v %q", release != nil, yield != nil, refused)
	}
	ctx := context.Background()
	formal, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "a", EvaluationTime: 60},
		execution.OperationNormal, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer formal.Release()
	if _, _, again := permit(); again != scheduler.LookbackRefusedFull {
		t.Fatalf("a lookback permit with both of two held = %q, want refused as full", again)
	}
	waiting := make(chan *scheduler.QueryPermit, 1)
	go func() {
		granted, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "b", EvaluationTime: 60},
			execution.OperationNormal, time.Now().Add(time.Minute))
		if err != nil {
			t.Error(err)
		}
		waiting <- granted
	}()
	select {
	case <-yield:
	case <-time.After(5 * time.Second):
		t.Fatal("a formal query waits and the lookback permit was not asked to yield")
	}
	release()
	select {
	case granted := <-waiting:
		granted.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("the permit the lookback gave back did not reach the waiting query")
	}
}

func sampledQuery(queryGroup execution.QueryGroupIdentity) lookback.Query {
	return lookback.Query{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: queryGroup, EvaluationTime: 1_700_000_060}},
		Spec: execution.PhysicalQuerySpec{Digest: "physical", LogicalWindow: execution.QueryWindow{Start: 1_700_000_000, End: 1_700_000_060},
			PlanFacts: execution.QueryPlanFacts{SourceSemantics: []string{controlplane.SupportedSourceSemantics[0]}, StepMillis: 60_000,
				Normalization: execution.DatasetNormalizationSpec{CanonicalValueField: "value"}}},
		Operation: execution.OperationNormal, AttemptNo: 1}
}

// The bundle is what the lookback asks about ownership: nothing is owned
// before it is bound, a Query Group with a Runner is, and the moment its
// Runner is removed its waiting samples are dropped as owner_lost -- the
// call is in removeRunnerLocked, the one way the Runner set shrinks.
func TestAQueryGroupTheBundleStopsOwningLeavesTheLookback(t *testing.T) {
	owner := &lookbackOwnership{}
	// Built as buildLookback builds it, with each first sample taken at once.
	options := lookbackOptions(noRecheck, nil, owner, nil, time.Now, nil)
	options.UnspreadFirstSamples = true
	engine, err := lookback.New(options)
	if err != nil {
		t.Fatal(err)
	}
	const queryGroup = execution.QueryGroupIdentity("qg-1")
	source := controlplane.SupportedSourceSemantics[0]
	if owner.owns(queryGroup) {
		t.Fatal("owned before the bundle was bound")
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Lookback: engine},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{}}
	owner.bind(bundle)
	engine.Begin(sampledQuery(queryGroup)).Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if stats := engine.Stats(); stats.Sources[source].Samples[lookback.OutcomeOwnerLost] != 1 || stats.Pending != 0 {
		t.Fatalf("a read of a Query Group with no Runner was kept: %+v", stats.Sources[source].Samples)
	}
	bundle.mu.Lock()
	bundle.setRunnerLocked(queryGroup, &phaseTwoQueryGroupLifecycle{})
	bundle.mu.Unlock()
	engine.Begin(sampledQuery(queryGroup)).Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if stats := engine.Stats(); stats.Pending != 1 || stats.Coverage != (lookback.Coverage{Owned: 1, Covered: 1, Ratio: 1, CoverableRatio: 1}) {
		t.Fatalf("a read of an owned Query Group was not kept: pending %d coverage %+v", stats.Pending, stats.Coverage)
	}
	bundle.mu.Lock()
	bundle.removeRunnerLocked(queryGroup)
	bundle.mu.Unlock()
	stats := engine.Stats()
	if stats.Pending != 0 || stats.PendingBytes != 0 || stats.Sources[source].Rechecks[lookback.RungNames[0]][lookback.RecheckOwnerLost] != 1 ||
		stats.Coverage.Owned != 0 {
		t.Fatalf("after the Runner left: pending %d bytes %d rechecks %v coverage %+v", stats.Pending, stats.PendingBytes,
			stats.Sources[source].Rechecks[lookback.RungNames[0]], stats.Coverage)
	}
	// A bundle without the lookback removes Runners as before.
	plain := &phaseTwoWorkerBundle{runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{queryGroup: {}}}
	plain.mu.Lock()
	plain.removeRunnerLocked(queryGroup)
	plain.mu.Unlock()
}

// lookback.get says whether the answering process runs the lookback, and
// carries its counts when it does.
func TestLookbackGetSaysWhetherItRunsAndCarriesTheCounts(t *testing.T) {
	read := func(op obchannel.Operation) cliLookbackReading {
		t.Helper()
		out := op.Run(context.Background(), obchannel.Params{})
		reading, ok := out.Value.(cliLookbackReading)
		if !ok || !out.Complete || op.ID != "lookback.get" || !op.Targetable {
			t.Fatalf("lookback.get = %+v", out)
		}
		return reading
	}
	if off := read(cliLookbackOperation(nil, lookbackStanding{})); off.Running || off.Stats != nil {
		t.Fatalf("off = %+v", off)
	}
	engine, standing, err := buildLookback(noRecheck, nil, &lookbackOwnership{}, nil, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	on := read(cliLookbackOperation(engine, standing))
	source := controlplane.SupportedSourceSemantics[0]
	if !on.Running || on.Stats == nil || len(on.Stats.Sources[source].Samples) != len(lookback.SampleOutcomes) ||
		len(on.Stats.Sources[source].Rechecks) != len(lookback.RungNames) {
		t.Fatalf("on = %+v", on)
	}
}

// The lookback's report of an object read early reaches the fleet snapshot
// as it was read: the time_delay the query runs under, the one that would
// have read it complete, and the samples it rests on, a partial revision
// marked as one. A process without a lookback publishes no such line.
func TestAnObjectTheLookbackFindsReadEarlyReachesTheSnapshot(t *testing.T) {
	if lookbackReadEarly(nil) != nil {
		t.Fatal("a process without a lookback publishes a read-early line")
	}
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) { lookbackReadEarlyReachesTheSnapshot(t, partial) })
	}
}

func lookbackReadEarlyReachesTheSnapshot(t *testing.T, partial bool) {
	// Partial: of two series, one comes back revised and one as it was. A
	// series is delivered as a dataset of its own.
	points := func(end int64, value string) []*execution.Dataset {
		if !partial {
			return []*execution.Dataset{lookbackPoint(end, value)}
		}
		return []*execution.Dataset{lookbackSeries(end, "h1", value), lookbackSeries(end, "h2", "5")}
	}
	var mu sync.Mutex
	now := time.Unix(1_700_000_060, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	set := func(at time.Time) { mu.Lock(); now = at; mu.Unlock() }
	revised := func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		for _, dataset := range points(spec.LogicalWindow.End, "3") {
			_ = sink.ConsumeProviderSeries(ctx, execution.ProviderSeriesBatch{Dataset: dataset})
		}
		return execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil
	}
	engine, err := lookback.New(lookback.Options{Now: clock, Recheck: revised, UnspreadFirstSamples: true,
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	const queryGroup = execution.QueryGroupIdentity("qg-late")
	for sample := 0; sample < 2; sample++ {
		query := sampledQuery(queryGroup)
		end := clock().Unix()
		query.Contract.Slot.EvaluationTime = execution.EvaluationTime(end)
		query.Spec.LogicalWindow = execution.QueryWindow{Start: end - 60, End: end}
		query.Spec.PlanFacts.QueryDelaySeconds = 60
		read := engine.Begin(query)
		for _, dataset := range points(end, "1") {
			read.Series(dataset, 10)
		}
		read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
		readAt := clock()
		// Each rung's moment in turn, until the sample is counted complete.
		completed := func() uint64 {
			return engine.Stats().Sources[controlplane.SupportedSourceSemantics[0]].Samples[lookback.OutcomeCompleted]
		}
		for rung := range lookback.RungSteps {
			set(readAt.Add(time.Duration(lookback.RungSteps[rung] * float64(time.Minute))))
			engine.Step(context.Background())
			for settle := time.Now().Add(200 * time.Millisecond); time.Now().Before(settle) && completed() <= uint64(sample); {
				time.Sleep(time.Millisecond)
			}
			if completed() > uint64(sample) {
				break
			}
		}
		if completed() != uint64(sample+1) {
			t.Fatalf("sample %d was not counted complete: %+v", sample, engine.Stats().Sources[controlplane.SupportedSourceSemantics[0]].Samples)
		}
		set(readAt.Add(2 * time.Hour))
	}
	facts := lookbackReadEarly(engine)()
	got, reported := facts[string(queryGroup)]
	if !reported || got.CurrentDelaySeconds != 60 || got.SuggestedDelaySeconds != 180 || got.StepSeconds != 60 {
		t.Fatalf("facts %+v, want the object with 60 s now and 180 s suggested", facts)
	}
	// The samples it was read from ride with it, as the lookback has them.
	readings := engine.Stats().ReadEarly
	if len(readings) != 1 || len(readings[0].Samples) != 2 || len(got.Samples) != 2 {
		t.Fatalf("lookback read_early %+v, facts %+v, want the two samples on both", readings, got)
	}
	for index, sample := range got.Samples {
		want := readings[0].Samples[index]
		if sample.EvaluationTime != int64(want.EvaluationTime) || sample.Rung != lookback.RungNames[0] || sample.Rung != want.Rung ||
			sample.FirstReadAgeSeconds != want.FirstReadAgeSeconds || sample.CompletionAgeSeconds != want.CompletionAgeSeconds ||
			sample.ChangedAgeSeconds != want.ChangedAgeSeconds || len(sample.Buckets) != 1 || sample.Buckets[0] != want.Buckets[0] ||
			sample.PartialRevised != partial || want.PartialRevised != partial {
			t.Fatalf("sample %d on the snapshot %+v, want %+v", index, sample, want)
		}
	}
}

// lookbackSeries is one series' point at the window's start.
func lookbackSeries(end int64, host, value string) *execution.Dataset {
	return execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: host, SourceTime: end - 60,
		DimensionIdentity: contract.DimensionIdentityV2{Digest: "digest-" + host},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(value)}}})
}

func lookbackPoint(end int64, value string) *execution.Dataset {
	return execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: "h1", SourceTime: end - 60,
		DimensionIdentity: contract.DimensionIdentityV2{Digest: "digest-h1"},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(value)}}})
}
