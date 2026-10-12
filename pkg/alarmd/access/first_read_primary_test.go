// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

// windowedHostProvider answers every physical query with one point per host
// in the last minute of that query's own window, so a dependency on
// yesterday's window gets yesterday's point.
type windowedHostProvider struct{ hosts []string }

func (provider *windowedHostProvider) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	ref := execution.ProviderResultRef("provider-result")
	var delivery execution.SeriesDelivery
	sourceTime := attempt.Spec.LogicalWindow.End - 60
	for _, host := range provider.hosts {
		fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"` + host + `"`)}}
		dimension, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		recordID, err := contract.DeriveRecordIDV2(dimension, sourceTime)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		records := []contract.CanonicalRecordV2{{RecordID: recordID, SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimension},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`60`)},
			Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"` + host + `"`),
				"bk_target_ip": json.RawMessage(`"` + host + `"`), "bk_target_cloud_id": json.RawMessage(`0`)},
			ReceivedTime: int64(attempt.Slot.EvaluationTime)}}
		digest, err := contract.DeriveCanonicalDigestV2("test-delivery", records)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		batch := execution.ProviderSeriesBatch{PhysicalQuery: attempt.Spec.Digest, CompletionRef: ref,
			Dataset: execution.NewDataset(records),
			Delivery: execution.SeriesDelivery{PhysicalQuery: attempt.Spec.Digest,
				QueryRevision: attempt.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: digest}}
		if err := sink.ConsumeProviderSeries(ctx, batch); err != nil {
			return execution.ProviderCompletion{}, err
		}
		if delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery); err != nil {
			return execution.ProviderCompletion{}, err
		}
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef},
		Stats:      execution.ProviderStats{Series: uint64(len(provider.hosts)), Records: uint64(len(provider.hosts))}}, nil
}

// withYesterdayDependency gives the frozen Slot a second physical query: an
// algorithm dependency on yesterday's minute, the shape of a year-on-year
// strategy, beside the primary window that ends at the Slot.
func withYesterdayDependency(t *testing.T, frozen *FrozenPlan) {
	t.Helper()
	withYesterdayMerge(t, frozen, "a + 1")
}

func withYesterdayMerge(t *testing.T, frozen *FrozenPlan, merge string) {
	t.Helper()
	primaryFacts := frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef]
	yesterdayFacts, err := primaryFacts.WithMetricMerge(merge)
	if err != nil {
		t.Fatal(err)
	}
	yesterdayRef := execution.LogicalQueryRef(yesterdayFacts.QueryRevision)
	yesterday := frozen.Requirements[0]
	yesterday.RequirementID, yesterday.DatasetName = "yesterday", "yesterday"
	yesterday.Role = execution.InputRoleAlgorithmDependency
	yesterday.LogicalQueryRef = yesterdayRef
	yesterday.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -86460, EndOffsetSeconds: -86400, HalfOpen: true}
	frozen.Requirements = append(frozen.Requirements, yesterday)
	frozen.QueryFacts[yesterdayRef] = yesterdayFacts
}

// The h design, section 14 (rule from 2026-10-09): a Slot's first read is
// the read of its primary window. A year-on-year Slot's dependency on
// yesterday is readable at once and its primary window 30 s later, so the
// dependency is read first; the Query Group's sample, which classifies its
// early reads, must still be the primary window, the one ending at the Slot.
func TestTheSlotsSampleIsItsPrimaryWindowWhenADependencyIsReadFirst(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	withYesterdayDependency(t, &frozen)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	slotEnd := int64(contractRef.Slot.EvaluationTime)

	now := time.Unix(slotEnd+10, 0)
	var clock sync.Mutex
	recheck := &lookbackRecheck{hosts: map[string]string{"192.0.2.10": "60"}}
	engine, err := lookback.New(lookback.Options{Recheck: recheck.read, UnspreadFirstSamples: true,
		Now:    func() time.Time { clock.Lock(); defer clock.Unlock(); return now },
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" }, Owns: func(execution.QueryGroupIdentity) bool { return true },
		Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &windowedHostProvider{hosts: []string{"192.0.2.10"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: 30 * time.Second, Lookback: engine})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.Unix(slotEnd, 0) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	consumer := &recordingConsumer{}
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, consumer); err != nil {
		t.Fatal(err)
	}

	step := time.Duration(frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef].StepMillis) * time.Millisecond
	clock.Lock()
	now = now.Add(time.Duration(lookback.RungSteps[0] * float64(step)))
	clock.Unlock()
	engine.Step(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		recheck.mu.Lock()
		read := len(recheck.specs)
		recheck.mu.Unlock()
		if read > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recheck of the sample: %+v", engine.Stats())
		}
		time.Sleep(time.Millisecond)
	}
	recheck.mu.Lock()
	defer recheck.mu.Unlock()
	if got := recheck.specs[0].LogicalWindow; got.End != slotEnd {
		t.Fatalf("the sample's window is [%d, %d), want the primary window ending at the Slot %d, not yesterday's", got.Start, got.End, slotEnd)
	}
}

// The same Slot with a hold: the trial read that lowers h (h design section
// 5) is made on the primary window and compared with the primary's formal
// first read. Before, the dependency's query took the reservation and the
// second query gave the reservation up, so such a group's h never lowered.
func TestAHeldSlotsTrialReadIsItsPrimaryWindow(t *testing.T) {
	// The queries share a deadline and are planned by digest; the
	// dependency's expression is chosen so it is prepared before or after
	// the primary, and the reservation must be the primary's either way.
	for _, dependencyFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "dependency planned first", false: "primary planned first"}[dependencyFirst], func(t *testing.T) {
			heldSlotTrialRead(t, dependencyFirst)
		})
	}
}

func heldSlotTrialRead(t *testing.T, dependencyFirst bool) {
	hold := 300 * time.Second
	var contractRef execution.FrozenExecutionContractRef
	var frozen FrozenPlan
	var evaluation time.Time
	for n := 1; ; n++ {
		if n > 50 {
			t.Fatalf("fixture: no dependency expression planned it first = %v", dependencyFirst)
		}
		contractRef, frozen = frozenExecution(t)
		withYesterdayMerge(t, &frozen, fmt.Sprintf("a + %d", n))
		evaluation = time.Unix(int64(contractRef.Slot.EvaluationTime), 0)
		frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluation.Add(10 * time.Minute).UnixMilli()
		for i := range frozen.Requirements {
			frozen.Requirements[i].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
		}
		contractRef.ReadHoldMillis = hold.Milliseconds()
		contractRef = bindFrozenDueDigest(t, contractRef, frozen)
		prepared, err := Prepare(contractRef, frozen, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if (prepared.Queries[0].Requirements[0].Role == execution.InputRoleAlgorithmDependency) == dependencyFirst {
			break
		}
	}
	slotEnd := int64(contractRef.Slot.EvaluationTime)

	now := evaluation
	var clock sync.Mutex
	at := func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	set := func(to time.Time) { clock.Lock(); now = to; clock.Unlock() }
	recheck := &lookbackRecheck{hosts: map[string]string{"192.0.2.10": "60"}}
	evidence := make(chan lookback.EarlierReadEvidence, 4)
	// The recheck says how many bytes it delivered, as the query service's
	// answer does: the engine counts them as the trial read finishes.
	reported := func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		return recheck.read(ctx, spec, deliveredBytesSink{sink})
	}
	engine, err := lookback.New(lookback.Options{Recheck: reported, UnspreadFirstSamples: true, Now: at,
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" }, Owns: func(execution.QueryGroupIdentity) bool { return true },
		Owned:           func() int { return 1 },
		CurrentReadHold: func(execution.QueryGroupIdentity) time.Duration { return hold },
		OnEarlierRead:   func(e lookback.EarlierReadEvidence) { evidence <- e }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &windowedHostProvider{hosts: []string{"192.0.2.10"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: 30 * time.Second, Lookback: engine})
	if err != nil {
		t.Fatal(err)
	}
	source.now = at
	source.wait = func(context.Context, time.Duration) error { return nil }
	request := execution.QueryExecutionRequest{Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}
	_, err = source.Execute(context.Background(), request, &recordingConsumer{})
	heldReady, deferred := ReadinessDeferredAt(err)
	if !deferred || !heldReady.After(evaluation.Add(hold)) {
		t.Fatalf("fixture: the held Slot answered %v (ready at %s) at its evaluation time %s, want it deferred past its hold", err, heldReady, evaluation)
	}

	// The trial read starts the recheck budget before the candidate
	// readiness: the readiness without the hold, plus the lowered hold.
	step := time.Duration(frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef].StepMillis) * time.Millisecond
	set(heldReady.Add(-hold + execution.LoweredReadHold(hold, step) - lookback.RecheckTimeout))
	engine.StepEarly(context.Background())
	// Wait for the trial read to have finished, not only to have asked: the
	// engine counts its bytes in the same step that files its answer, and a
	// formal first read that finds it still running takes it as overtaken.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var earlier uint64
		for _, source := range engine.Stats().Sources {
			earlier += source.EarlierReadBytes
		}
		if earlier > 0 {
			break
		}
		if time.Now().After(deadline) {
			reads := map[string]map[string]uint64{}
			for name, source := range engine.Stats().Sources {
				reads[name] = source.EarlierReads
			}
			t.Fatalf("no trial read was made; earlier-read outcomes %v", reads)
		}
		time.Sleep(time.Millisecond)
	}

	set(heldReady.Add(time.Second))
	if _, err := source.Execute(context.Background(), request, &recordingConsumer{}); err != nil {
		t.Fatalf("Execute at the held readiness: %v", err)
	}
	select {
	case got := <-evidence:
		if got.Outcome != lookback.EarlierEqual || !got.Observed {
			t.Fatalf("trial read outcome %q observed %v, want it compared with the primary's first read and equal", got.Outcome, got.Observed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no trial read evidence after the formal first read")
	}
	recheck.mu.Lock()
	defer recheck.mu.Unlock()
	if got := recheck.specs[0].LogicalWindow; got.End != slotEnd {
		t.Fatalf("the trial read the window [%d, %d), want the primary window ending at the Slot %d", got.Start, got.End, slotEnd)
	}
}

// Every Slot with a query has exactly one first read, the query carrying the
// primary requirement with the smallest RequirementID, whatever the order
// the queries are planned in (the h design, section 14).
func TestEverySlotHasExactlyOneFirstRead(t *testing.T) {
	planned := func(requirements ...execution.DataRequirement) PlannedQuery {
		return PlannedQuery{Requirements: requirements}
	}
	primary := func(id string) execution.DataRequirement {
		return execution.DataRequirement{RequirementID: execution.RequirementID(id), Role: execution.InputRolePrimary}
	}
	dependency := func(id string) execution.DataRequirement {
		return execution.DataRequirement{RequirementID: execution.RequirementID(id), Role: execution.InputRoleAlgorithmDependency}
	}
	for _, tc := range []struct {
		name    string
		queries []PlannedQuery
		want    int
	}{
		{"single query", []PlannedQuery{planned(primary("p"))}, 0},
		{"dependency planned first", []PlannedQuery{planned(dependency("a")), planned(primary("p"))}, 1},
		{"primary and dependencies", []PlannedQuery{planned(dependency("y")), planned(primary("p")), planned(dependency("w"))}, 1},
		{"two primaries, smaller id second", []PlannedQuery{planned(primary("p2")), planned(primary("p1"))}, 1},
		{"two primaries, smaller id first", []PlannedQuery{planned(primary("p1")), planned(primary("p2"))}, 0},
		{"primary shared with a dependency", []PlannedQuery{planned(dependency("a")), planned(dependency("b"), primary("p"))}, 1},
		{"no primary requirement", []PlannedQuery{planned(dependency("a")), planned(dependency("b"))}, 0},
		{"no query", nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := slotFirstRead(tc.queries)
			if got != tc.want {
				t.Fatalf("first read = %d, want %d", got, tc.want)
			}
			marked := 0
			for index := range tc.queries {
				if index == got {
					marked++
				}
			}
			if len(tc.queries) > 0 && marked != 1 {
				t.Fatalf("%d first reads marked, want exactly one", marked)
			}
		})
	}
}

// A Slot executed again as its second attempt is not read again as a first
// read: it takes no sample, whichever of its queries is marked.
func TestASecondAttemptTakesNoSample(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	withYesterdayDependency(t, &frozen)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	slotEnd := int64(contractRef.Slot.EvaluationTime)
	engine, err := lookback.New(lookback.Options{UnspreadFirstSamples: true,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		},
		Now:    func() time.Time { return time.Unix(slotEnd+10, 0) },
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" }, Owns: func(execution.QueryGroupIdentity) bool { return true },
		Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &windowedHostProvider{hosts: []string{"192.0.2.10"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: 30 * time.Second, Lookback: engine})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.Unix(slotEnd+60, 0) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 2}, &recordingConsumer{}); err != nil {
		t.Fatal(err)
	}
	for name, stats := range engine.Stats().Sources {
		if stats.Samples[lookback.OutcomeCaptured] != 0 || stats.FirstReads != 0 {
			t.Fatalf("%s: samples %v first reads %d after a second attempt, want none", name, stats.Samples, stats.FirstReads)
		}
	}
}

// deliveredBytesSink states each batch's delivered bytes when the stub did
// not, as a provider's batch does.
type deliveredBytesSink struct{ inner execution.ProviderSeriesSink }

func (sink deliveredBytesSink) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	if batch.Delivery.Bytes == 0 {
		batch.Delivery.Bytes = 1
	}
	return sink.inner.ConsumeProviderSeries(ctx, batch)
}
