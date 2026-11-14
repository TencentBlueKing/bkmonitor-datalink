// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The cases here run a Slot whose input does not fit half its share through
// the coordinator's one Execute path (decision-020 batch 8, 7.3-7.6), against
// a query source that reads a slice as the store does: a series is delivered
// when its value on the sliced dimension satisfies the request's conditions
// (InputSlice.Matches), the same function the conditions are rendered from.
// What a case expects is read from the source's own record of what it
// delivered, and from the stores' record of what was written, never from the
// slicer's own counters alone.

// slicedPool is the retained-byte pool of the sliced fixtures: a share of
// three quarters of it, 768 KiB, and a slice limit of at most half that.
const slicedPool = 1 << 20

const slicedShare = slicedPool / 4 * 3

// slicedSeries is one series of the sliced fixture: its value on the
// dimension the Slot is sliced on, and the padding, in a dimension outside
// its identity, that makes its input as large as the case needs. cloud, set,
// is its value on a second dimension, which a case groups by too
// (cloudHostFacts).
type slicedSeries struct {
	host    string
	padding int
	cloud   string
}

// digest is the series' identity, a function of its sliced value alone.
func (series slicedSeries) digest() string {
	sum := sha256.Sum256([]byte("sliced-series/" + series.host))
	return hex.EncodeToString(sum[:])
}

// slicedHosts is count series named host-00, host-01, ..., each padded to
// padding bytes: their last characters spread over the ten digits, as a
// numbered fleet's do.
func slicedHosts(count, padding int) []slicedSeries {
	series := make([]slicedSeries, count)
	for index := range series {
		series[index] = slicedSeries{host: fmt.Sprintf("host-%02d", index), padding: padding}
	}
	return series
}

// sliceableFacts is the frozen facts of a structured metric query grouped by
// host, every function aggregating by it: what SliceableDimensions accepts.
func sliceableFacts() *execution.QueryPlanFacts {
	return &execution.QueryPlanFacts{Provider: execution.ProviderUQ, QueryList: []execution.QueryClause{{
		TableID: "system.cpu_summary", Dimensions: []string{"host"},
		Functions: []execution.QueryFunction{{Method: "mean", Dimensions: []string{"host"}}},
	}}}
}

// slicedCall is what one query of the Slot asked and was answered.
type slicedCall struct {
	slice     execution.InputSlice
	delivered []string
	padding   int
	completed bool
	// partial is a read answered short of FULL, and timedOut one that ran
	// out of time (slicedSource.timedOutRead).
	partial, timedOut bool
	err               error
}

// slicedSource is the query source over a fixed set of series. It honours
// the request's slice unless ignoreSlice, which is a store that drops the
// conditions and answers the whole Query Group on every read. completeness
// answers each read that reaches its completion, given which one it is
// (from one) and, for a read that delivered series, which of those it is
// (from one; zero for a read that delivered none); FULL when nil. It is also
// asked before a read streams, with held -1: a read it answers UNAVAILABLE
// then streams nothing, as a query whose answer did not arrive delivered
// nothing. partial is filled with the series every read that did not
// complete delivered.
type slicedSource struct {
	t           *testing.T
	ports       *recordingPorts
	series      []slicedSeries
	facts       *execution.QueryPlanFacts
	ignoreSlice bool
	// tooLong is a store that refuses every slice's query as too long and
	// answers a whole read.
	tooLong bool
	// plans and requirements replace the fixture's one Plan when set.
	plans        []execution.DuePlan
	requirements []execution.DataRequirement
	completeness func(read, held int) execution.Completeness
	// targetMissing answers every read as the provider answers a query whose
	// table routes nowhere in the space: no series, UNAVAILABLE, its one
	// attempt failed QUERY_TARGET_MISSING, as access/uq's
	// unavailableCompletion completes SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS
	// with no series.
	targetMissing bool
	// mu guards calls, partial and incomplete: the next slice is read on a
	// goroutine of its own while the Slot completes the one before.
	mu      sync.Mutex
	started atomic.Int64
	calls   []slicedCall
	partial map[string]bool
	// incomplete is set once any read was answered short of FULL.
	incomplete bool
	// timeoutWhole times out every read of the whole Query Group, the zero
	// slice; queryMillis is what every answered read says its query took.
	timeoutWhole bool
	queryMillis  uint64
	// timeoutTimes times out that many slices once timeoutAfter reads of
	// slices were answered, every read of each - read ahead or again on the
	// Slot's goroutine - as one; answered counts the reads answered.
	timeoutAfter, timeoutTimes, answered int
	timedOut                             map[string]bool
	// timeoutKind is how a read runs out of time (slicedTimeout): UQ's 504
	// unless set, through provider's real client.
	timeoutKind slicedTimeout
	provider    slicedProvider
	// timeoutBySize times out every read, whole or a slice, that holds
	// timeoutHolding series or more, as UQ does a request too large for its
	// 30 s: zero is every read, a backend slow as a whole.
	timeoutBySize  bool
	timeoutHolding int
}

// holding is how many of the source's series the slice holds.
func (source *slicedSource) holding(slice execution.InputSlice) int {
	count := 0
	for _, series := range source.series {
		if in, err := slice.Matches(map[string]string{"host": series.host, "cloud": series.cloud}); err == nil && in {
			count++
		}
	}
	return count
}

func (source *slicedSource) Execute(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	source.started.Add(1)
	source.ports.record("query")
	call := slicedCall{slice: request.Slice}
	if source.timeoutWhole && request.Slice.IsZero() || source.timeoutBySize && source.holding(request.Slice) >= source.timeoutHolding {
		completion, err := source.timedOutRead(ctx, request, consumer, &call)
		source.mu.Lock()
		source.calls = append(source.calls, call)
		source.mu.Unlock()
		return completion, err
	}
	source.mu.Lock()
	digest := request.Slice.Digest()
	timedOut := source.timedOut[digest]
	if !timedOut && !request.Slice.IsZero() && source.timeoutTimes > 0 && source.answered >= source.timeoutAfter {
		source.timeoutTimes--
		if source.timedOut == nil {
			source.timedOut = map[string]bool{}
		}
		source.timedOut[digest], timedOut = true, true
	}
	source.mu.Unlock()
	if timedOut {
		completion, err := source.timedOutRead(ctx, request, consumer, &call)
		source.mu.Lock()
		source.calls = append(source.calls, call)
		source.mu.Unlock()
		return completion, err
	}
	completion, err := source.answer(ctx, request, consumer, &call)
	call.completed, call.err = err == nil, err
	source.mu.Lock()
	source.calls = append(source.calls, call)
	if err == nil && !request.Slice.IsZero() {
		source.answered++
	}
	source.mu.Unlock()
	return completion, err
}

// headerFor is the header the source begins every read of request with, as
// the access source begins one before it sends the query (access.Source).
func (source *slicedSource) headerFor(request execution.QueryExecutionRequest) execution.InternalExecutionHeader {
	input := validInternalExecution()
	plans, requirements := input.DuePlans, input.Requirements
	if source.plans != nil {
		plans, requirements = source.plans, source.requirements
	}
	queryRevision := execution.QueryRevision(requirements[0].LogicalQueryRef)
	header := execution.InternalExecutionHeader{
		ExecutionID: "execution-1", Contract: request.Contract, DuePlans: plans, Requirements: requirements,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "physical-query-1", QueryRevision: queryRevision}},
		DeadlineUnixMilli:       1_788_000_030_000,
	}
	for _, series := range source.series {
		header.EffectiveTimeFacts = append(header.EffectiveTimeFacts, execution.BoundEffectiveTimeFact{
			Consumer:       execution.ConsumerRef{Plan: planIdentity(), LevelID: 5, HasLevel: true},
			SeriesIdentity: execution.SeriesIdentityDigest(series.digest()), Fact: effectiveTimeFactForTest(plans[0].CompiledPlan),
		})
	}
	if source.facts != nil {
		header.QueryFacts = map[execution.LogicalQueryRef]execution.QueryPlanFacts{requirements[0].LogicalQueryRef: *source.facts}
	}
	return header
}

func (source *slicedSource) answer(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer, call *slicedCall) (execution.QueryExecutionCompletion, error) {
	input := validInternalExecution()
	requirements := input.Requirements
	if source.plans != nil {
		requirements = source.requirements
	}
	queryRevision := execution.QueryRevision(requirements[0].LogicalQueryRef)
	header := source.headerFor(request)
	if err := consumer.Begin(ctx, header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	reads, heldBefore := 1, 0
	source.mu.Lock()
	earlierCalls := append([]slicedCall(nil), source.calls...)
	source.mu.Unlock()
	for _, earlier := range earlierCalls {
		if earlier.completed {
			reads++
			if len(earlier.delivered) != 0 {
				heldBefore++
			}
		}
	}
	unavailable := source.targetMissing || source.completeness != nil && source.completeness(reads, -1) == execution.CompletenessUnavailable
	var delivery execution.SeriesDelivery
	for _, series := range source.series {
		if unavailable {
			break
		}
		if !source.ignoreSlice {
			in, err := request.Slice.Matches(map[string]string{"host": series.host, "cloud": series.cloud})
			if err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
			if !in {
				continue
			}
		}
		host, _ := json.Marshal(series.host)
		dimensions := map[string]json.RawMessage{"host": host,
			"padding": json.RawMessage(`"` + strings.Repeat("p", series.padding) + `"`)}
		if series.cloud != "" {
			dimensions["cloud"], _ = json.Marshal(series.cloud)
		}
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
			RecordID: derivedTestRecordID(), SourceTime: 1_788_000_000, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: series.digest()},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.1`)},
			Dimensions:        dimensions,
			ReceivedTime:      1_788_000_000,
		}})
		view, _ := execution.NewDatasetView(dataset, []uint32{0})
		binding := input.Inputs[0]
		binding.Dataset, binding.View = dataset, view
		batch := execution.SeriesDelivery{PhysicalQuery: "physical-query-1", QueryRevision: queryRevision,
			Series: 1, Records: 1, Digest: "delivery-" + series.digest()[:16]}
		accumulated, err := execution.AccumulateSeriesDelivery(delivery, batch)
		if err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		delivery = accumulated
		call.delivered = append(call.delivered, series.digest())
		call.padding += series.padding
		if err := consumer.ConsumeSeries(ctx, execution.SeriesExecutionBatch{
			PhysicalQuery: "physical-query-1", QueryRevision: queryRevision, CompletionRef: "provider-result-1",
			Dataset: dataset, Inputs: []execution.NamedInputBinding{binding}, Delivery: batch,
		}); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
	}
	completeness := execution.CompletenessFull
	if unavailable {
		completeness = execution.CompletenessUnavailable
	} else if source.completeness != nil {
		held := 0
		if len(call.delivered) != 0 {
			held = heldBefore + 1
		}
		completeness = source.completeness(reads, held)
	}
	physical := execution.PhysicalQueryCompletion{Ref: "provider-result-1", PhysicalQuery: "physical-query-1",
		QueryRevision: queryRevision, Completeness: completeness, DataState: execution.DataStateData, Delivery: delivery,
		Stats: execution.ProviderStats{QueryMillis: source.queryMillis}}
	if len(call.delivered) == 0 {
		physical.DataState = execution.DataStateEmpty
	}
	if completeness == execution.CompletenessUnavailable {
		physical.DataState = execution.DataStateUnknown
	}
	if completeness != execution.CompletenessFull {
		call.partial = true
		source.mu.Lock()
		source.incomplete = true
		if source.partial == nil {
			source.partial = make(map[string]bool)
		}
		for _, series := range call.delivered {
			source.partial[series] = true
		}
		source.mu.Unlock()
	}
	if completeness == execution.CompletenessPartial {
		physical.PartialEvidence = &execution.PartialEvidence{Kind: execution.PartialEvidenceOmissionStable, Version: 1,
			EvidenceDigest: strings.Repeat("d", 64), OmissionOnly: true, ReturnedRecordsStable: true}
	}
	if source.targetMissing {
		physical.RouteFacts = execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1,
			Result: execution.RouteAttemptFailed, ReasonCode: execution.ReasonCode(contract.ReasonQueryTargetMissing),
			Detail: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"}}}
	}
	if source.tooLong && !request.Slice.IsZero() {
		physical.RouteFacts = execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1,
			Rejection: &execution.ProviderRejection{Class: execution.RejectionClassQueryTooLong}}}}
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true,
		PhysicalQueries: []execution.PhysicalQueryCompletion{physical}}
	completion.CompletionBindings = accessShapedCompletionBindings(source.t, header, completion.PhysicalQueries)
	return completion, nil
}

// landed is the reads that reached their completion, in order: the slices
// the Slot landed.
func (source *slicedSource) landed() []slicedCall {
	var landed []slicedCall
	for _, call := range source.calls {
		if call.completed {
			landed = append(landed, call)
		}
	}
	return landed
}

// slicedFixture is the coordinator over the sliced source, with State kept
// across attempts.
type slicedFixture struct {
	ports        *recordingPorts
	state        *persistedRetryState
	source       *slicedSource
	coordinator  *worker.SlotExecutionCoordinator
	observations *[]observability.Observation
}

// newSlicedFixture builds the fixture with a 1 MiB pool; maxStates is the
// State writes one window takes, so a slice can itself be written in
// windows.
func newSlicedFixture(t *testing.T, series []slicedSeries, facts *execution.QueryPlanFacts, maxStates uint64) *slicedFixture {
	t.Helper()
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true}
	state := &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}
	source := &slicedSource{t: t, ports: ports, series: series, facts: facts}
	observations := make([]observability.Observation, 0)
	fixture := &slicedFixture{ports: ports, state: state, source: source, observations: &observations}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: ports, State: state, GapGuard: ports, Events: ports}, maxStates)
	return fixture
}

// build makes the coordinator with the fixture's ports, the ones a case may
// replace taken from replaced, the fixture's evaluator when it names none.
func (fixture *slicedFixture) build(t *testing.T, replaced worker.Ports, maxStates uint64) *worker.SlotExecutionCoordinator {
	t.Helper()
	ports, observations := fixture.ports, fixture.observations
	if replaced.Evaluator == nil {
		replaced.Evaluator = ports
	}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports,
		Activation: replaced.Activation, Query: fixture.source, Sequencer: ports, Evaluator: replaced.Evaluator, Admission: ports,
		GapGuard: replaced.GapGuard, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness,
		Events: replaced.Events, State: replaced.State, Progress: ports, ExecutionEvidence: newMemoryEvidenceStore(),
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observations = append(*observations, observability.NormalizeObservation(observation))
		})},
		worker.ProvisionalBudget{MaxSeries: 200, MaxRetainedBytes: slicedPool, MaxStateMutations: maxStates, MaxEvents: 200, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

// countDigests is how many times each series appears in lists.
func countDigests(lists ...[]string) map[string]int {
	counts := make(map[string]int)
	for _, list := range lists {
		for _, digest := range list {
			counts[digest]++
		}
	}
	return counts
}

// stateWrites is how many times each series' State was sent to the store.
func (fixture *slicedFixture) stateWrites() map[string]int {
	counts := make(map[string]int)
	for _, key := range fixture.ports.appliedKeys {
		counts[string(key.SeriesIdentityDigest)]++
	}
	return counts
}

// noDataCensuses is how many no-data censuses the Slot reported: one a Slot,
// whatever its no-data Plans decided.
func (fixture *slicedFixture) noDataCensuses() int {
	count := 0
	for _, observation := range *fixture.observations {
		if observation.Stage == observability.StageNoDataDecided && observation.NoDataCensus != nil {
			count++
		}
	}
	return count
}

// A Slot of 24 series of 40 KiB, about 1 MiB of input, is past its 768 KiB
// share as a whole: before slicing it was refused. Read in slices, each one
// within half the share, it completes: every series is read in exactly one
// slice that landed, evaluated once and its State written once; every slice
// that landed held less than the limit (its padding alone is a lower bound
// of its input); the completion row counts the slices landed and the ones
// split; and what needs every series - the completion-only Plan, the no-data
// census, Progress - happens once, after the last slice was read.
func TestASlotPastHalfItsShareIsReadInSlicesEachSeriesWrittenOnce(t *testing.T) {
	series := slicedHosts(24, 40<<10)
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	plans, requirements, request := withCompletionOnlyPlan(t)
	fixture.source.plans, fixture.source.requirements = plans, requirements

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot completed in slices", result, err)
	}
	landed := fixture.source.landed()
	if len(landed) < 2 {
		t.Fatalf("landed %d reads, want the Slot read in more than one slice", len(landed))
	}
	reads := make([][]string, 0, len(landed))
	for _, call := range landed {
		reads = append(reads, call.delivered)
		if call.padding > slicedShare/2 {
			t.Fatalf("a landed slice held %d bytes of padding alone, past the slice limit %d", call.padding, slicedShare/2)
		}
		if call.slice.IsZero() {
			t.Fatal("a landed read was the whole Query Group, which does not fit")
		}
	}
	events := countDigests(fixture.ports.eventBatches...)
	writes := fixture.stateWrites()
	for _, one := range series {
		digest := one.digest()
		if read := countDigests(reads...)[digest]; read != 1 {
			t.Fatalf("series %s was read in %d landed slices, want exactly one", one.host, read)
		}
		if events[digest] != 1 || writes[digest] != 1 {
			t.Fatalf("series %s: %d events and %d State writes, want one of each", one.host, events[digest], writes[digest])
		}
	}
	if len(writes) != len(series) || len(fixture.state.saved) != len(series) {
		t.Fatalf("State written for %d keys (%d saved), want the %d series", len(writes), len(fixture.state.saved), len(series))
	}
	// A read the Slot let go is a slice past its limit, split; or a slice
	// read ahead that yielded to the slice before it, read again after.
	discarded := 0
	for _, call := range fixture.source.calls {
		switch {
		case call.completed:
		case strings.Contains(fmt.Sprint(call.err), "passed its limit"):
			discarded++
		case errors.Is(call.err, context.Canceled):
		default:
			t.Fatalf("a read ended by %v, want only slices let go at their limit or read ahead and let go", call.err)
		}
	}
	usage := result.Usage
	if usage.InputSlices != uint64(len(landed)) || usage.InputSlicesDiscarded != uint64(discarded) {
		t.Fatalf("usage counts %d slices and %d discarded, want %d landed and %d split", usage.InputSlices,
			usage.InputSlicesDiscarded, len(landed), discarded)
	}
	if usage.RetainedBytes > slicedShare {
		t.Fatalf("the Slot held %d bytes at its peak, past its share %d", usage.RetainedBytes, slicedShare)
	}
	if censuses := fixture.noDataCensuses(); censuses != 1 {
		t.Fatalf("no-data census reported %d times, want once for the Slot", censuses)
	}
	completionOnly := 0
	for _, observation := range *fixture.observations {
		if observation.Stage == observability.StageEvaluationCompleted && observation.Trace.BusinessID == "3" {
			completionOnly++
		}
	}
	if completionOnly != 1 {
		t.Fatalf("the Plan without series was decided %d times, want once", completionOnly)
	}
	trace := *fixture.ports.trace
	lastQuery := nthStage(t, trace, "query", countStage(trace, "query"))
	if countStage(trace, "progress_commit") != 1 || nthStage(t, trace, "progress_commit", 1) < lastQuery {
		t.Fatalf("Progress committed %d times, want once after the last slice's read: %v", countStage(trace, "progress_commit"), trace)
	}
}

// A store that drops the slice's conditions answers the whole Query Group to
// every slice: no split can make a slice fit, and the Slot is refused by the
// value-distribution word within the K_max bound instead of looping.
func TestASourceThatIgnoresTheSliceEndsValueConcentratedWithinKMax(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 40<<10), sliceableFacts(), 200)
	fixture.source.ignoreSlice = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot refused", result, err)
	}
	if reason, ok := worker.InputSliceRefusal(err); !ok || reason != contract.ReasonInputSliceValueConcentrated {
		t.Fatalf("refusal = %q, %v (err %v), want %s", reason, ok, err, contract.ReasonInputSliceValueConcentrated)
	}
	// Deterministic: the scheduler finalizes it as it does a share refusal,
	// under its own code and why.
	if refusal, final := execution.ShareRefusalOf(err); !final || refusal.Reason != contract.ReasonInputSliceValueConcentrated ||
		refusal.Detail == "" || refusal.LimitBytes != slicedShare {
		t.Fatalf("share refusal of %v = %+v, %t; want a final one under %s with why", err, refusal, final, contract.ReasonInputSliceValueConcentrated)
	}
	if reads := len(fixture.source.calls); reads > 2*inputslice.MaxLeaves-1 {
		t.Fatalf("%d reads before the refusal, past the 2*K_max-1 bound", reads)
	}
	if len(fixture.source.landed()) != 0 || fixture.ports.eventCount != 0 || len(fixture.state.saved) != 0 {
		t.Fatal("a Slot refused for its values landed a slice")
	}
}

// A store that refuses a slice's query as too long refuses the Slot under
// INPUT_SLICE_QUERY_TOO_LONG, final for the scheduler as a share refusal is,
// with the slice's condition bytes as why; nothing is written.
func TestASliceTheStoreRefusesAsTooLongEndsTheSlotFinal(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 40<<10), sliceableFacts(), 200)
	fixture.source.tooLong = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if reason, ok := worker.InputSliceRefusal(err); err == nil || result.Completed || !ok || reason != contract.ReasonInputSliceQueryTooLong {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot refused as %s", result, err, contract.ReasonInputSliceQueryTooLong)
	}
	if refusal, final := execution.ShareRefusalOf(err); !final || refusal.Reason != contract.ReasonInputSliceQueryTooLong ||
		!strings.HasPrefix(refusal.Detail, "conditions of ") || refusal.LimitBytes != slicedShare {
		t.Fatalf("share refusal of %v = %+v, %t; want a final one under %s with the slice's bytes", err, refusal, final, contract.ReasonInputSliceQueryTooLong)
	}
	if fixture.ports.eventCount != 0 || len(fixture.state.saved) != 0 {
		t.Fatal("a Slot refused for its slice's length wrote events or State")
	}
}

// A Slot with nothing to slice on - no query facts, or a PromQL query - is
// read whole, as before slicing: past half its share and within it, it
// completes in one read; past its share it is refused for why it could not be
// sliced, INPUT_SLICE_UNSUPPORTED with the reason on its query failure line
// (storage_dialect for a Slot whose facts say no store, promql for a PromQL
// query), never split; the share's own line and its deterministic refusal,
// which the scheduler gives the Slot up on, stay as they are.
func TestAnUnsliceableSlotIsReadWholeAndJudgedByItsShare(t *testing.T) {
	promql := sliceableFacts()
	promql.PromQL = &execution.PromQLQuery{Expression: "avg by (host) (cpu_usage)"}
	for _, test := range []struct {
		name  string
		facts *execution.QueryPlanFacts
		why   string
	}{{name: "no_facts", why: execution.InputSliceReasonStorageDialect}, {name: "promql", facts: promql, why: execution.InputSliceReasonPromQL}} {
		t.Run(test.name+"/within_share", func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(12, 40<<10), test.facts, 200)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v err=%v, want the Slot read whole and completed", result, err)
			}
			if calls := fixture.source.calls; len(calls) != 1 || !calls[0].slice.IsZero() || len(calls[0].delivered) != 12 {
				t.Fatalf("%d reads, want one whole read of the 12 series", len(calls))
			}
			if result.Usage.InputSlicesDiscarded != 0 || len(fixture.state.saved) != 12 {
				t.Fatalf("usage %+v, %d saved: want no split and every series written", result.Usage, len(fixture.state.saved))
			}
		})
		t.Run(test.name+"/past_share", func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(24, 40<<10), test.facts, 200)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v err=%v, want the Slot refused by its share", result, err)
			}
			word, sliced := worker.InputSliceRefusal(err)
			why, _ := execution.InputSliceUnsupportedOf(err)
			if !sliced || word != contract.ReasonInputSliceUnsupported || why != test.why || len(fixture.source.calls) != 1 {
				t.Fatalf("err %v (%s, %q) after %d reads, want one read refused INPUT_SLICE_UNSUPPORTED for %s",
					err, word, why, len(fixture.source.calls), test.why)
			}
			named := false
			for _, observation := range *fixture.observations {
				named = named || (observation.ReasonCode == observability.ReasonCode(contract.ReasonInputSliceUnsupported) &&
					observation.QueryFailure != nil && observation.QueryFailure.Detail == test.why)
			}
			if !named {
				t.Fatalf("no INPUT_SLICE_UNSUPPORTED line saying %s; observed %+v", test.why, *fixture.observations)
			}
			// The share's own line and its finalization stay: the Slot's
			// bytes against the share, and a refusal the scheduler gives the
			// Slot up on rather than reading it whole again.
			assertCapacityRejectionReason(t, fixture.observations, observability.CapacityBudgetRetainedBytes, contract.ReasonQGBudgetShareExceeded)
			if refusal, deterministic := execution.ShareRefusalOf(err); !deterministic || refusal.Reason != "" {
				t.Fatalf("err %v is %+v, %t; want the share's own deterministic refusal: the scheduler would read the Slot whole until its replay age",
					err, refusal, deterministic)
			}
		})
	}
}

// A Slot whose queries combine series across the dimension sliced on - a
// clause's top k of every series, a metric merge's top k - cannot be read in
// slices: each slice would answer its own top k. Past its share it is refused
// as INPUT_SLICE_UNSUPPORTED, cross_series, after one whole read. A merge
// that combines its references series by series, a / b or a or b, is read
// in slices, every series once.
func TestASlotCombiningSeriesAcrossTheSliceIsNotSliced(t *testing.T) {
	clauseTopK := sliceableFacts()
	clauseTopK.QueryList[0].Functions = append(clauseTopK.QueryList[0].Functions,
		execution.QueryFunction{Method: "topk", Arguments: []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: "1"}}})
	merged := func(expression string) *execution.QueryPlanFacts {
		facts := sliceableFacts()
		facts.MetricMerge = expression
		return facts
	}
	for _, test := range []struct {
		name  string
		facts *execution.QueryPlanFacts
	}{{"clause_topk", clauseTopK}, {"merge_topk", merged("topk(1, a)")}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(24, 40<<10), test.facts, 200)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			why, _ := execution.InputSliceUnsupportedOf(err)
			if err == nil || result.Completed || why != execution.InputSliceReasonCrossSeries || len(fixture.source.calls) != 1 {
				t.Fatalf("Execute() result=%+v err=%v after %d reads, want one whole read refused for cross_series",
					result, err, len(fixture.source.calls))
			}
			if fixture.ports.eventCount != 0 || len(fixture.state.saved) != 0 {
				t.Fatal("a refused Slot wrote events or State")
			}
		})
	}
	for _, expression := range []string{"a / b", "a or b"} {
		t.Run(expression, func(t *testing.T) {
			series := slicedHosts(24, 40<<10)
			fixture := newSlicedFixture(t, series, merged(expression), 200)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v err=%v, want the Slot completed in slices", result, err)
			}
			landed := fixture.source.landed()
			reads := make([][]string, 0, len(landed))
			for _, call := range landed {
				reads = append(reads, call.delivered)
			}
			for _, one := range series {
				if read := countDigests(reads...)[one.digest()]; read != 1 || len(landed) < 2 {
					t.Fatalf("series %s read in %d of %d landed slices, want once in more than one slice", one.host, read, len(landed))
				}
			}
			if len(fixture.state.saved) != len(series) {
				t.Fatalf("%d series saved, want the %d", len(fixture.state.saved), len(series))
			}
		})
	}
}

// A slice may take half of what the share leaves, the other half being its
// windows': a Slot whose input is past that half and within the whole share
// is still read in slices, none of them past the half, and every series is
// written once.
func TestASlotBetweenHalfItsShareAndItsShareIsStillReadInSlices(t *testing.T) {
	series := slicedHosts(14, 40<<10)
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot completed in slices", result, err)
	}
	landed := fixture.source.landed()
	if len(landed) < 2 {
		t.Fatalf("landed %d reads of %d bytes of padding, want more than one slice past half the share %d",
			len(landed), 14*(40<<10), slicedShare/2)
	}
	for _, call := range landed {
		if call.padding > slicedShare/2 {
			t.Fatalf("a landed slice held %d bytes of padding alone, past the slice limit %d", call.padding, slicedShare/2)
		}
	}
	if len(fixture.state.saved) != len(series) {
		t.Fatalf("%d series saved, want the %d", len(fixture.state.saved), len(series))
	}
}

// One series larger than the slice limit cannot be helped by a split: the
// Slot is refused by its share where the slice passes its limit, not split
// until K_max and named for its values. A sliceable Slot of small series
// beside it is not (the positive case above).
func TestASeriesLargerThanTheSliceLimitIsRefusedByTheShare(t *testing.T) {
	series := append(slicedHosts(4, 8<<10), slicedSeries{host: "host-huge", padding: 500 << 10})
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot refused by its share", result, err)
	}
	if reason, sliced := worker.InputSliceRefusal(err); sliced {
		t.Fatalf("refused as %s, want the share's word: a split does not mend one series", reason)
	}
	if len(fixture.source.calls) != 1 {
		t.Fatalf("%d reads, want the first read refused without a split", len(fixture.source.calls))
	}
	assertCapacityRejectionReason(t, fixture.observations, observability.CapacityBudgetRetainedBytes, contract.ReasonQGBudgetShareExceeded)
	if fixture.ports.eventCount != 0 || len(fixture.state.saved) != 0 {
		t.Fatal("a refused Slot wrote events or State")
	}
}

// withCompletionOnlyPlan is the fixture's due Plans with a second one, of
// business 3, that reads the same query and is handed no series: it is
// decided from its completion alone, which only the last slice can do.
func withCompletionOnlyPlan(t *testing.T) ([]execution.DuePlan, []execution.DataRequirement, execution.SlotExecutionRequest) {
	t.Helper()
	plans, requirements := baseDuePlanAndRequirements()
	second := plans[0]
	second.Identity.BusinessID = "3"
	plans = append(plans, second)
	requirements[0].Consumers = append(requirements[0].Consumers, execution.DataRequirementConsumer{
		Consumer:                  execution.ConsumerRef{Plan: second.Identity, LevelID: 5, HasLevel: true},
		ConsumerDeadlineUnixMilli: second.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000,
	})
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatal(err)
	}
	request := windowedRequest()
	request.Contract.DuePlanSetDigest = digest
	request.DuePlanTargets = execution.FrozenDuePlanTargets{DuePlanSetDigest: digest, Plans: []execution.PlanKey{plans[0].Key(), plans[1].Key()}}
	return plans, requirements, request
}

// markerStore is one Plan's gap marker kept by the rules the execution
// store applies them by (state.ExecutionStore.ApplyGap): a statement names
// the revision it expects and moves it one on; a second statement at the
// version of the marker is the same one again (ALREADY_APPLIED), or one that
// only extends it - more scopes, or more FULL Slots, at the same reason -
// through execution.ExtendSameSlotGap, the store's own function, and
// otherwise a CONFLICT. The marker a case reads is what those rules left.
type markerStore struct {
	t       *testing.T
	ports   *recordingPorts
	found   bool
	marker  storedMarker
	version execution.ApplyVersion
	digest  execution.MutationDigest
	sched   execution.PlanScheduleRevision
	shrunk  []string
	results []execution.GapGuardApplyStatus
}

// storedMarker is the marker's revision and scopes; revision zero when there
// is none.
type storedMarker struct {
	MarkerRevision uint64
	Scopes         []execution.GapScopeState
}

// gapped is whether the marker holds an open scope: a series written under
// it is guarded.
func (marker storedMarker) gapped() bool {
	for _, scope := range marker.Scopes {
		if scope.Status == execution.GapStatusGapped {
			return true
		}
	}
	return false
}

// covers is whether marker holds every gapped scope earlier did, with no
// fewer FULL Slots required: a marker that grew or stayed, never one that
// shrank.
func (marker storedMarker) covers(earlier storedMarker) bool {
	if marker.MarkerRevision < earlier.MarkerRevision {
		return false
	}
	for _, before := range earlier.Scopes {
		if before.Status != execution.GapStatusGapped {
			continue
		}
		held := false
		for _, scope := range marker.Scopes {
			held = held || scope.Scope == before.Scope && scope.Status == execution.GapStatusGapped &&
				scope.RequiredFullSlots >= before.RequiredFullSlots
		}
		if !held {
			return false
		}
	}
	return true
}

func (store *markerStore) LoadGaps(_ context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	store.ports.record("gap_load")
	result := execution.GapLoadResult{Items: make([]execution.GapGuardSnapshot, len(request.Items))}
	for index, item := range request.Items {
		snapshot := execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}
		if store.found {
			snapshot = execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapFound,
				MarkerRevision: store.marker.MarkerRevision, PersistedApplyVersion: store.version,
				PersistedMutationDigest: store.digest, LastScheduleRevision: store.sched,
				Scopes: append([]execution.GapScopeState(nil), store.marker.Scopes...)}
			if len(store.marker.Scopes) == 0 {
				snapshot.Status = execution.GapClearedTombstone
			}
		}
		result.Items[index] = snapshot
	}
	return result, nil
}

func (store *markerStore) LoadGapsInto(ctx context.Context, request execution.GapLoadRequest, accept func(execution.GapGuardSnapshot) error) error {
	result, err := store.LoadGaps(ctx, request)
	if err != nil {
		return err
	}
	for _, item := range result.Items {
		if err := accept(item); err != nil {
			return err
		}
	}
	return nil
}

func (store *markerStore) ApplyGap(_ context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	store.ports.record("gap_apply")
	store.ports.gapMutations = append(store.ports.gapMutations, request.Items...)
	result := execution.GapGuardApplyResult{Items: make([]execution.GapGuardApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		status := store.apply(mutation)
		store.results = append(store.results, status)
		result.Items[index] = execution.GapGuardApplyItemResult{Identity: mutation.Identity, Status: status}
	}
	return result, nil
}

func (store *markerStore) apply(mutation execution.PlanGapMutation) execution.GapGuardApplyStatus {
	before := store.marker
	var next []execution.GapScopeState
	if store.found && execution.CompareApplyVersion(store.version, mutation.ApplyVersion) == execution.ApplyVersionEqual {
		if store.digest == mutation.MutationDigest {
			return execution.GapGuardAlreadyApplied
		}
		extended, ok := execution.ExtendSameSlotGap(store.marker.Scopes, mutation.Scopes)
		if !ok || store.sched != mutation.ScheduleRevision {
			return execution.GapGuardConflict
		}
		next = extended
	} else {
		for _, scope := range mutation.Scopes {
			switch scope.Kind {
			case execution.GapOpen, execution.GapStrengthen:
				next = append(next, execution.GapScopeState{Scope: scope.Scope, Status: execution.GapStatusGapped,
					ReasonCode: scope.ReasonCode, RequiredFullSlots: scope.RequiredFullSlots})
			case execution.GapWarmup:
				next = append(next, execution.GapScopeState{Scope: scope.Scope, Status: execution.GapStatusWarming,
					ReasonCode: scope.ReasonCode, RequiredFullSlots: scope.RequiredFullSlots})
			}
		}
	}
	if store.marker.MarkerRevision != mutation.ExpectedMarkerRevision {
		return execution.GapGuardConflict
	}
	store.found = true
	store.marker = storedMarker{MarkerRevision: mutation.ExpectedMarkerRevision + 1, Scopes: next}
	store.version, store.digest, store.sched = mutation.ApplyVersion, mutation.MutationDigest, mutation.ScheduleRevision
	if !store.marker.covers(before) {
		store.shrunk = append(store.shrunk, fmt.Sprintf("%+v after %+v", store.marker, before))
	}
	return execution.GapGuardApplied
}

// coveredEvents is the event sink that, before it takes a batch, checks the
// invariant of 7.5: once a slice's PRIMARY came back PARTIAL, every event
// that goes out goes out under a gapped marker already in the store.
// failAfterPartialAt fails the n-th (from one) such batch, as a crash halfway
// through a slice's events.
type coveredEvents struct {
	*recordingPorts
	source             *slicedSource
	gaps               *markerStore
	uncovered          []string
	afterPartial       int
	failAfterPartialAt int
}

// afterPartial says whether the batch's series were read in a slice answered
// short of FULL or in one that landed after such a slice: judged by the order the slices
// landed, not by when the source answered them - the next slice is read
// while the Slot completes the one before.
func (source *slicedSource) afterPartial(batch []contract.TriggerEventV1) bool {
	if len(batch) == 0 {
		return false
	}
	digest := batch[0].RecordRef.DimensionIdentityDigest
	source.mu.Lock()
	defer source.mu.Unlock()
	partialBefore := false
	for _, call := range source.calls {
		if !call.completed {
			continue
		}
		for _, delivered := range call.delivered {
			if delivered == digest {
				return partialBefore || call.partial
			}
		}
		partialBefore = partialBefore || call.partial
	}
	return partialBefore
}

func (events *coveredEvents) WriteBatch(ctx context.Context, batch []contract.TriggerEventV1) error {
	if events.source.afterPartial(batch) {
		if !events.gaps.marker.gapped() {
			events.uncovered = append(events.uncovered, fmt.Sprint(len(events.eventBatches)))
		}
		events.afterPartial++
		if events.afterPartial == events.failAfterPartialAt {
			events.record("event_ack")
			return &retryableOutputTestError{err: errors.New("injected crash halfway through a slice's events")}
		}
	}
	return events.recordingPorts.WriteBatch(ctx, batch)
}

// partialEvaluator is the fixture's evaluator answering a series whose
// PRIMARY came back PARTIAL as it answers a degraded Slot - a provable
// abnormal outcome, its event, a warming State and the Plan's open - for
// that series: the evaluator sees the series' own inputs, not the Slot.
type partialEvaluator struct {
	*recordingPorts
	// mu takes one evaluation at a time: degraded is the recording ports'
	// for the one series being answered, and a batch's series are evaluated
	// side by side.
	mu sync.Mutex
}

func (evaluator *partialEvaluator) Evaluate(ctx context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	evaluator.mu.Lock()
	defer evaluator.mu.Unlock()
	partial := false
	for _, input := range request.Inputs {
		for _, binding := range input.Inputs {
			partial = partial || binding.Role == execution.InputRolePrimary && binding.Completeness == execution.CompletenessPartial
		}
	}
	if !partial {
		return evaluator.recordingPorts.Evaluate(ctx, request)
	}
	evaluator.degraded = true
	defer func() { evaluator.degraded = false }()
	result, err := evaluator.recordingPorts.Evaluate(ctx, request)
	if err != nil {
		return result, err
	}
	mutation := partialStateMutationForTest()
	mutation.Identity.SeriesIdentityDigest = request.State.Items[0].Identity.SeriesIdentityDigest
	mutation.ExpectedBlobRevision = request.State.Items[0].BlobRevision
	mutation.MutationDigest = ""
	if mutation, err = execution.BuildStateMutation(mutation); err != nil {
		return result, err
	}
	result.Plans[0].StateResults[0].Mutation = mutation
	return result, nil
}

// B3: a slice whose PRIMARY came back PARTIAL after a first slice that was
// all FULL opens the Plan's marker in its own head, before anything of it
// and before every later slice's events: the first slice's events went out
// with no marker, which was right for them, and every event after the
// PARTIAL answer is written under the marker, which only grows. (A series
// whose PRIMARY is PARTIAL writes no event of its own: it is answered from
// its bindings, not evaluated.) A crash halfway through a later slice's
// events, written in windows of three, leaves the marker over the events it
// wrote; the retry keeps it as it was or larger, writes the rest under it,
// and completes with every series whose PRIMARY completed written once.
func TestALaterPartialSliceOpensTheMarkerBeforeItsEventsAndARetryKeepsIt(t *testing.T) {
	series := slicedHosts(24, 40<<10)
	fixture := newSlicedFixture(t, series, sliceableFacts(), 3)
	fixture.ports.gapMissing = true
	gaps := &markerStore{t: t, ports: fixture.ports}
	events := &coveredEvents{recordingPorts: fixture.ports, source: fixture.source, gaps: gaps, failAfterPartialAt: 2}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: fixture.ports, State: fixture.state, GapGuard: gaps, Events: events,
		Evaluator: &partialEvaluator{recordingPorts: fixture.ports}}, 3)
	fixture.source.completeness = func(_, held int) execution.Completeness {
		if held == 2 {
			return execution.CompletenessPartial
		}
		return execution.CompletenessFull
	}
	request := windowedRequest()

	first, err := fixture.coordinator.Execute(context.Background(), request)
	if err == nil && first.Completed {
		t.Fatalf("Execute() result=%+v, want the crash halfway through a slice's events to stop it", first)
	}
	landed := fixture.source.landed()
	if len(landed) < 2 || len(fixture.source.partial) == 0 {
		t.Fatalf("landed %d slices, %d series PARTIAL: want the second slice read PARTIAL", len(landed), len(fixture.source.partial))
	}
	for _, digest := range landed[0].delivered {
		if fixture.source.partial[digest] {
			t.Fatal("the first slice was read PARTIAL")
		}
	}
	// The first slice's events went out before any marker: it was FULL.
	if marker := gaps.marker; !marker.gapped() {
		t.Fatalf("after the PARTIAL slice's head the store holds %+v, want its gapped marker", marker)
	}
	crashed := gaps.marker
	if events.afterPartial != 2 {
		t.Fatalf("%d event batches after the PARTIAL answer, want the crash in the second", events.afterPartial)
	}

	fixture.source.calls, events.failAfterPartialAt = nil, 0
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("retry result=%+v err=%v, want it completed", result, err)
	}
	if marker := gaps.marker; !marker.covers(crashed) {
		t.Fatalf("the retry left %+v, smaller than the %+v the crash left", marker, crashed)
	}
	if len(events.uncovered) != 0 || len(gaps.shrunk) != 0 {
		t.Fatalf("event batches %v went out with no marker over them; the marker shrank %v", events.uncovered, gaps.shrunk)
	}
	for _, status := range gaps.results {
		if status == execution.GapGuardConflict {
			t.Fatalf("a marker statement was refused as a conflict: %v", gaps.results)
		}
	}
	writes := fixture.stateWrites()
	for _, one := range series {
		if want := map[bool]int{false: 1, true: 0}[fixture.source.partial[one.digest()]]; writes[one.digest()] != want {
			t.Fatalf("series %s written %d times across the attempts, want %d", one.host, writes[one.digest()], want)
		}
	}
}

// slicedFirstSeen are the ports of a Plan new to its activation, forced to
// warm up and without a marker, over a State store that holds a record from
// an earlier Slot for the series in prior and what this Slot wrote.
type slicedFirstSeen struct {
	*persistedRetryState
	prior  map[execution.SeriesIdentityDigest]bool
	older  execution.ApplyVersion
	levels []execution.RuntimeLevelStateView
}

func (ports *slicedFirstSeen) LoadActivations(ctx context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	result, err := ports.recordingPorts.LoadActivations(ctx, request)
	for index := range result.Facts {
		result.Facts[index].Selected.ForceWarming, result.Facts[index].Selected.Arrived = true, true
	}
	return result, err
}

func (ports *slicedFirstSeen) LoadRuntime(ctx context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	result, err := ports.persistedRetryState.LoadRuntime(ctx, request)
	if err != nil {
		return result, err
	}
	ports.mu.Lock()
	defer ports.mu.Unlock()
	for index, item := range request.Items {
		if _, written := ports.saved[item.Identity]; written || !ports.prior[item.Identity.SeriesIdentityDigest] {
			continue
		}
		result.Items[index] = execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateFoundReady, BlobRevision: 1,
			PersistedApplyVersion: ports.older, PersistedMutationDigest: "earlier-slot", Levels: ports.levels}
	}
	return result, nil
}

// newSlicedFirstSeenFixture is the sliced fixture with the Plan new to its
// activation and the series in prior holding State from the Slot before.
func newSlicedFirstSeenFixture(t *testing.T, series []slicedSeries, prior []string) (*slicedFixture, *slicedFirstSeen) {
	t.Helper()
	return newSlicedFirstSeenFixtureOf(t, series, prior, 200)
}

// newSlicedFirstSeenFixtureOf is newSlicedFirstSeenFixture writing at most
// maxStates State mutations a window.
func newSlicedFirstSeenFixtureOf(t *testing.T, series []slicedSeries, prior []string, maxStates uint64) (*slicedFixture, *slicedFirstSeen) {
	t.Helper()
	fixture := newSlicedFixture(t, series, sliceableFacts(), maxStates)
	fixture.ports.gapMissing = true
	refs, err := execution.DeriveRuntimeLevelContractRefs(compiledPlanForTest(nil))
	if err != nil {
		t.Fatal(err)
	}
	older := frozenApplyVersion()
	older.EvaluationTime -= 60
	ports := &slicedFirstSeen{persistedRetryState: fixture.state, prior: map[execution.SeriesIdentityDigest]bool{}, older: older}
	for _, ref := range refs {
		ports.levels = append(ports.levels, execution.RuntimeLevelStateView{LevelID: ref.LevelID,
			LevelStateCompatibility: ref.LevelStateCompatibility, WarmupRequirementRef: ref.WarmupRequirementRef,
			HistoryCompleteness: execution.HistoryFull, LastProcessedEventTime: int64(older.EvaluationTime)})
	}
	for _, digest := range prior {
		ports.prior[execution.SeriesIdentityDigest(digest)] = true
	}
	gaps := &markerStore{t: t, ports: fixture.ports}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: ports, State: ports, GapGuard: gaps, Events: fixture.ports}, maxStates)
	return fixture, ports
}

// satisfiedMarkers is how many first-seen markers - a clear of the Plan-wide
// scope - the Slot sent.
func satisfiedMarkers(mutations []execution.PlanGapMutation) int {
	count := 0
	for _, mutation := range mutations {
		if len(mutation.Scopes) == 1 && mutation.Scopes[0].Scope == (execution.GapScope{}) && mutation.Scopes[0].Kind == execution.GapClear {
			count++
		}
	}
	return count
}

// First-seen is judged slice by slice and only ever moves toward seen
// (7.5): a Plan new to its activation, without a marker, finds nothing from
// before in its first two slices and writes them as first-seen, without a
// warm-up; its third slice finds one series that ran before, and from there
// the Plan warms up - nothing more of it is written, and the tail writes no
// satisfied marker - while the completion row names the two slices and
// their series that went out as first-seen. Which series a slice holds is
// learned from a first run of the same input with nothing from before.
func TestFirstSeenIsJudgedPerSliceAndAPlanFoundSeenWarmsUpFromThatSlice(t *testing.T) {
	firstSeenPerSlice(t, 200)
}

// The same, every window three State mutations at most: every slice of six
// series and more is cut into windows halfway through, and the windows it
// wrote let go of their series before the slice ends (dropWindowInputs).
// The slices written as first-seen and their series are counted whole all
// the same: a slice's series are counted as they are prepared, not by what
// is left of them at its end (what is left depended on whether a
// window was cut, which the reads ahead decided now and then).
func TestFirstSeenCountsTheSeriesOfASliceCutIntoWindows(t *testing.T) {
	firstSeenPerSlice(t, 3)
}

func firstSeenPerSlice(t *testing.T, maxStates uint64) {
	t.Helper()
	series := slicedHosts(24, 40<<10)
	learn, _ := newSlicedFirstSeenFixtureOf(t, series, nil, maxStates)
	if result, err := learn.coordinator.Execute(context.Background(), windowedRequest()); err != nil || !result.Completed {
		t.Fatalf("learning run result=%+v err=%v", result, err)
	}
	// The slices that hold series, in the order they are read: a slice may
	// hold none, and writes nothing as first-seen or otherwise.
	var slices []slicedCall
	seenAt := 0
	for index, slice := range learn.source.landed() {
		if len(slice.delivered) != 0 {
			slices = append(slices, slice)
			if len(slices) == 3 {
				seenAt = index
			}
		}
	}
	if len(slices) < 3 {
		t.Fatalf("the input lands in %d slices that hold series, want at least three", len(slices))
	}
	if satisfiedMarkers(learn.ports.gapMutations) != 1 || learn.ports.eventCount != len(series) {
		t.Fatalf("a Plan never seen sent %d satisfied markers and %d events, want one and every series'", satisfiedMarkers(learn.ports.gapMutations), learn.ports.eventCount)
	}

	// The Plan found to have run is warmed up as any forced Plan is: it
	// writes nothing more this attempt, its WARMING marker is written by the
	// protection path, and the attempt is retried to converge.
	fixture, _ := newSlicedFirstSeenFixtureOf(t, series, []string{slices[2].delivered[0]}, maxStates)
	request := windowedRequest()
	first, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || first.Completed || first.ReasonCode != execution.ReasonCode(contract.ReasonPlanReactivated) {
		t.Fatalf("Execute() result=%+v err=%v, want the Plan warmed up under %s", first, err, contract.ReasonPlanReactivated)
	}
	written := fixture.stateWrites()
	for index, slice := range fixture.source.landed() {
		for _, digest := range slice.delivered {
			if index < seenAt && written[digest] != 1 || index >= seenAt && written[digest] != 0 {
				t.Fatalf("slice %d series written %d times, want the slices before the one found seen written once and none after", index+1, written[digest])
			}
		}
	}
	if markers := satisfiedMarkers(fixture.ports.gapMutations); markers != 0 {
		t.Fatalf("a Plan found to have run sent %d satisfied markers", markers)
	}

	fixture.source.calls = nil
	mutations := len(fixture.ports.gapMutations)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	if markers := satisfiedMarkers(fixture.ports.gapMutations[mutations:]); markers != 0 {
		t.Fatal("the retry judged the Plan unseen again and sent its satisfied marker")
	}
	for index, slice := range fixture.source.landed() {
		for _, digest := range slice.delivered {
			if index >= seenAt && fixture.stateWrites()[digest] != 0 {
				t.Fatal("the retry wrote a series of the Plan it had found seen")
			}
		}
	}
	unseenSeries := uint64(len(slices[0].delivered) + len(slices[1].delivered))
	if first.Usage.FirstSeenUnseenSlices != 2 || first.Usage.FirstSeenUnseenSeries != unseenSeries {
		t.Fatalf("completion row counts %d slices and %d series written as first-seen, want 2 and %d",
			first.Usage.FirstSeenUnseenSlices, first.Usage.FirstSeenUnseenSeries, unseenSeries)
	}
}

// K_0 (7.4): a replica that read a Query Group's last Slot in slices starts
// its next one already sliced, from that reading, rather than reading the
// whole group first and letting it go: the next Slot's first read is a
// slice, and there are at least as many as its input over the slice limit.
// The first Slot, with no reading, began with the whole group, which did
// not fit. The next starts on the cut that Slot's sample made: its first
// ten hosts, ending in 0 1 3 4 6 7 9 a c d, classes [013], [46], [79a] and
// the rest, which holds the other 16 hosts (640 KiB, past the limit) - the
// hosts' last characters are sorted slowest, and the sample saw only the
// start (the limit 7.3 names). So one slice the next Slot started in is let
// go and split by its own sample, every series read once among the slices
// that land.
func TestTheNextSlotOfAQueryGroupStartsWithTheSlicesItsLastReadNeeded(t *testing.T) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	series := make([]slicedSeries, 24)
	for index := range series {
		series[index] = slicedSeries{host: "node-" + string(alphabet[index*len(alphabet)/len(series)]), padding: 40 << 10}
	}
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	request := windowedRequest()
	first, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !first.Completed {
		t.Fatalf("first Slot result=%+v err=%v", first, err)
	}
	if calls := fixture.source.calls; !calls[0].slice.IsZero() || calls[0].completed || first.Usage.InputSlicesDiscarded == 0 {
		t.Fatalf("first Slot began with %+v, want the whole group read and let go", calls[0].slice)
	}

	// The next read of the Query Group on this replica. What K_0 is counted
	// from is the group's last reading here, not the Slot, and the fixture's
	// evaluator writes at its one Slot: the next read is that Slot's request
	// again, over a State store that starts from nothing, which is not this
	// case's subject.
	fixture.source.calls = nil
	fixture.state.saved = map[execution.StateKeyIdentity]execution.StateMutation{}
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("next Slot result=%+v err=%v", result, err)
	}
	calls := fixture.source.calls
	padding := 0
	for _, one := range series {
		padding += one.padding
	}
	least := (padding + slicedShare/2 - 1) / (slicedShare / 2)
	if calls[0].slice.IsZero() || result.Usage.InputSlicesDiscarded != 1 || result.Usage.InputSliceStartDiscarded != 1 ||
		len(calls) != len(fixture.source.landed())+1 {
		t.Fatalf("next Slot read %d times, %d discarded, first read whole=%v: want it sliced from its first read, the rest let go",
			len(calls), result.Usage.InputSlicesDiscarded, calls[0].slice.IsZero())
	}
	// The row counts the slices that landed: the five started in less the
	// rest let go, and the four it was split into - eight.
	if len(calls) < least || result.Usage.InputSlices != uint64(len(fixture.source.landed())) || result.Usage.InputSlices != 8 {
		t.Fatalf("next Slot read %d slices (row says %d), want at least %d for its %d bytes of padding",
			len(calls), result.Usage.InputSlices, least, padding)
	}
	reads := make([][]string, 0, len(calls))
	for _, call := range fixture.source.landed() {
		reads = append(reads, call.delivered)
	}
	for _, one := range series {
		if countDigests(reads...)[one.digest()] != 1 {
			t.Fatalf("series %s read %d times in the next Slot", one.host, countDigests(reads...)[one.digest()])
		}
	}
}

// A later slice that answers the Plan's scope worse than the slice that
// opened its marker - UNAVAILABLE after PARTIAL - raises the scope's reason:
// within its Slot a marker's reason only rises (execution.GapReasonRank), so
// its head's statement is applied, not refused as a CONFLICT. The Slot
// completes in one attempt with the marker saying UNAVAILABLE, every event
// after the PARTIAL answer under it.
func TestALaterSliceAnsweringWorseRaisesTheMarkersReason(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 40<<10), sliceableFacts(), 3)
	fixture.ports.gapMissing = true
	gaps := &markerStore{t: t, ports: fixture.ports}
	events := &coveredEvents{recordingPorts: fixture.ports, source: fixture.source, gaps: gaps}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: fixture.ports, State: fixture.state, GapGuard: gaps, Events: events,
		Evaluator: &partialEvaluator{recordingPorts: fixture.ports}}, 3)
	partialRead := 0
	fixture.source.completeness = func(read, held int) execution.Completeness {
		switch {
		case held == 2:
			partialRead = read
			return execution.CompletenessPartial
		case held == -1 && partialRead != 0 && read == partialRead+2:
			return execution.CompletenessUnavailable
		}
		return execution.CompletenessFull
	}
	result, err := fixture.coordinator.Execute(context.Background(), windowedRequest())
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v, want the Slot completed in one attempt", result, err)
	}
	if partialRead == 0 || result.CompletionKind != execution.CompletionUnavailable {
		t.Fatalf("PARTIAL at read %d, completion %s: want a PARTIAL slice and an UNAVAILABLE one", partialRead, result.CompletionKind)
	}
	for _, status := range gaps.results {
		if status == execution.GapGuardConflict {
			t.Fatalf("a marker statement was refused as a conflict: %v", gaps.results)
		}
	}
	if !gaps.marker.gapped() || len(events.uncovered) != 0 || len(gaps.shrunk) != 0 {
		t.Fatalf("marker %+v; event batches %v went out with no marker over them; the marker shrank %v", gaps.marker, events.uncovered, gaps.shrunk)
	}
	for _, scope := range gaps.marker.Scopes {
		if scope.Status == execution.GapStatusGapped && execution.GapReasonRank(scope.ReasonCode) < execution.GapReasonRank(execution.ReasonCode(contract.ReasonQueryUnavailable)) {
			t.Fatalf("marker scope %+v still says the first slice's PARTIAL, want the later slice's UNAVAILABLE", scope)
		}
	}
}

// A slice that held none of the Plan's series can still come back PARTIAL:
// the series it left out are in no other slice. Its answer guards the Plan
// as a PARTIAL answer of the whole read would - the Plan's marker is opened
// before every later event, and the Slot completes with a partial gap, not
// FULL - rather than being dropped because no series carried it.
//
// The empty slice is read before others: 24 numbered hosts of 45 KiB, read
// whole and let go at the ninth over half the 768 KiB share (384 KiB). The
// root is cut by that sample, last digits 0 to 8: [012], [34], [56] and the
// rest - 9, 5, 4 and 6 hosts - and the exhausted slice, which holds none.
// [012], 405 KiB, is let go in turn and its children read after every slice
// made before them: the exhausted root slice is read, empty, before them.
func TestAnEmptySliceAnsweredPartialStillGuardsThePlan(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 45<<10), sliceableFacts(), 200)
	fixture.ports.gapMissing = true
	gaps := &markerStore{t: t, ports: fixture.ports}
	events := &coveredEvents{recordingPorts: fixture.ports, source: fixture.source, gaps: gaps}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: fixture.ports, State: fixture.state, GapGuard: gaps, Events: events}, 200)
	partialRead := 0
	fixture.source.completeness = func(read, held int) execution.Completeness {
		if held == 0 && read > 1 && (partialRead == 0 || partialRead == read) {
			partialRead = read
			return execution.CompletenessPartial
		}
		return execution.CompletenessFull
	}
	result, err := fixture.coordinator.Execute(context.Background(), windowedRequest())
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v", result, err)
	}
	if partialRead == 0 {
		t.Fatal("no slice after the first held no series: the case did not run")
	}
	if !gaps.marker.gapped() || result.CompletionKind == execution.CompletionFull {
		t.Fatalf("marker %+v, completion %s: want the Plan guarded by the empty PARTIAL slice", gaps.marker, result.CompletionKind)
	}
	if events.afterPartial == 0 || len(events.uncovered) != 0 {
		t.Fatalf("%d event batches after the PARTIAL answer, %v of them with no marker over them: want every one under it",
			events.afterPartial, events.uncovered)
	}
	if primary := fixture.ports.lastProgress.Completion.Primary; primary == nil || primary.Completeness != execution.CompletenessPartial {
		t.Fatalf("the completion's PRIMARY fact is %+v, want the round PARTIAL", primary)
	}
}

// overlapEvents is the event sink that, at the first window's events, waits
// - up to a bound - for the source to have begun a second read, and says
// whether it had: a read begun while a slice's events are written is the
// next slice read ahead.
type overlapEvents struct {
	*recordingPorts
	source     *slicedSource
	once       sync.Once
	overlapped bool
}

func (events *overlapEvents) WriteBatch(ctx context.Context, batch []contract.TriggerEventV1) error {
	events.once.Do(func() {
		for deadline := time.Now().Add(2 * time.Second); events.source.started.Load() < 2 && time.Now().Before(deadline); { // fixed-wait: the fake goes on either way; the case reads what came after
			time.Sleep(time.Millisecond) // fixed-wait: polling for a read the read ahead begins, bounded above
		}
		events.overlapped = events.source.started.Load() >= 2
	})
	return events.recordingPorts.WriteBatch(ctx, batch)
}

// The next slice is read while the one before completes: a second read has
// begun by the time the first slice's events are written. Replayed in the
// order it came, every series is still read in one slice that landed, its
// event and its State written once.
func TestTheNextSliceIsReadWhileTheSliceBeforeCompletes(t *testing.T) {
	series := slicedHosts(24, 40<<10)
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	events := &overlapEvents{recordingPorts: fixture.ports, source: fixture.source}
	fixture.coordinator = fixture.build(t, worker.Ports{Activation: fixture.ports, State: fixture.state,
		GapGuard: fixture.ports, Events: events}, 200)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v", result, err)
	}
	if len(fixture.source.landed()) < 2 {
		t.Fatalf("landed %d reads, want the Slot read in slices", len(fixture.source.landed()))
	}
	if !events.overlapped {
		t.Fatal("no second read began while the first slice's events were written")
	}
	if usage := result.Usage; usage.InputSlicesReadAhead == 0 || usage.InputSlicesReadAhead+usage.InputSlicesReadAheadOtherLeaf+
		usage.InputSlicesReadAheadYielded+usage.InputSlicesReadAheadFailed > usage.InputSlices+usage.InputSlicesDiscarded {
		t.Fatalf("usage counts %d slices read ahead and replayed (%d, %d, %d let go) of %d read: want some replayed, "+
			"no more ends than reads", usage.InputSlicesReadAhead, usage.InputSlicesReadAheadOtherLeaf,
			usage.InputSlicesReadAheadYielded, usage.InputSlicesReadAheadFailed, usage.InputSlices)
	}
	counts, writes := countDigests(fixture.ports.eventBatches...), fixture.stateWrites()
	for _, one := range series {
		if counts[one.digest()] != 1 || writes[one.digest()] != 1 {
			t.Fatalf("series %s: %d events and %d State writes, want one of each", one.host, counts[one.digest()], writes[one.digest()])
		}
	}
}

// A Query Group read whole that ran out of time starts its next read in
// slices: the timed-out read is remembered at no less than the backend's
// 30 s, and the next read is of slices only and completes, the backend
// answering every slice. One that answered is read whole again, however
// slowly it answered: its time is no measure of its size.
func TestAQueryGroupThatTimedOutReadWholeIsReadInSlicesNext(t *testing.T) {
	for _, test := range []struct {
		name           string
		timeout        bool
		queryMillis    uint64
		wantSlicedNext bool
	}{
		{"timed out", true, 0, true},
		// Answered, however slowly: a backend slow as a whole is no slice
		// too large, and is read whole again.
		{"answered slowly", false, 40_000, false},
		{"answered quickly", false, 1_000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
			fixture.source.timeoutWhole, fixture.source.queryMillis = test.timeout, test.queryMillis
			// The next read of the Query Group on this replica is the same
			// Slot's request again, as the K_0 case reads it: what it starts
			// in slices from is the read before.
			request := windowedRequest()
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed || (result.QueryAvailability == execution.QueryAvailabilityUnavailable) != test.timeout {
				t.Fatalf("first read result=%+v err=%v, want it completed, its query unavailable %t", result, err, test.timeout)
			}
			first := len(fixture.source.calls)
			fixture.source.timeoutWhole = false
			result, err = fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("next Slot result=%+v err=%v, want it completed", result, err)
			}
			calls := fixture.source.calls[first:]
			sliced := len(calls) > 1 && !calls[0].slice.IsZero()
			if sliced != test.wantSlicedNext {
				t.Fatalf("next Slot read in slices = %t (%d reads, first zero %t), want %t", sliced, len(calls),
					len(calls) > 0 && calls[0].slice.IsZero(), test.wantSlicedNext)
			}
		})
	}
}

// A slice that runs out of time is split and read again only when its time
// is put on its size: the slices before it in the Slot answered well under
// the target, the cooldown does not hold, and the Slot has not split so. Its
// children read, the Slot completes, split_succeeded; with the cooldown
// holding, or every slice slow - the backend slow, not the slice large - it
// fails as before, not_split; split and its children running out of time
// too, split_failed.
func TestASliceThatRunsOutOfTimeIsSplitOnlyWhenItIsLarge(t *testing.T) {
	for _, test := range []struct {
		name         string
		cooldown     bool
		sliceMillis  uint64
		timeoutTimes int
		want         string
	}{
		{"one slice large", false, 1_000, 1, observability.InputSliceTimeoutSplitSucceeded},
		{"the cooldown holds", true, 1_000, 1, observability.InputSliceTimeoutNotSplit},
		{"every slice slow", false, 20_000, 1, observability.InputSliceTimeoutNotSplit},
		{"its children run out of time too", false, 1_000, 100, observability.InputSliceTimeoutSplitFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
			// A Slot before that ran out of time read whole, its size not
			// known yet: the next is read in slices by that time.
			fixture.source.timeoutWhole = true
			request := windowedRequest()
			readWholeTimedOut(t, fixture, request)
			fixture.source.timeoutWhole = false
			fixture.source.mu.Lock()
			fixture.source.queryMillis, fixture.source.answered = test.sliceMillis, 0
			fixture.source.timeoutAfter, fixture.source.timeoutTimes = 1, test.timeoutTimes
			fixture.source.mu.Unlock()
			first, observed := len(fixture.source.calls), len(*fixture.observations)
			request.QueryCooldownHeld = test.cooldown
			result, err := fixture.coordinator.Execute(context.Background(), request)
			calls := fixture.source.calls[first:]
			if test.want == observability.InputSliceTimeoutSplitSucceeded {
				if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 1 {
					t.Fatalf("result=%+v err=%v, want it completed with one slice split for its time", result, err)
				}
				if calls[0].slice.IsZero() || len(calls) < 4 {
					t.Fatalf("%d reads, want the slices, the one timed out and its children", len(calls))
				}
				return
			}
			if test.want == observability.InputSliceTimeoutNotSplit {
				// Not put on its size: the slice the provider timed out
				// completes UNAVAILABLE as it came, and the Slot degraded.
				if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 0 {
					t.Fatalf("result=%+v err=%v, want it completed degraded, no slice split for its time", result, err)
				}
				return
			}
			if err == nil {
				t.Fatal("the Slot whose split slice's children ran out of time too completed")
			}
			// The Slot's own: the one before ran out of time read whole, and
			// said so on its query line too.
			var ended []string
			for _, observation := range (*fixture.observations)[observed:] {
				if facts := observation.QueryFailure; facts != nil && facts.SliceTimeout != "" {
					ended = append(ended, facts.SliceTimeout)
				}
			}
			if len(ended) != 1 || ended[0] != test.want {
				t.Fatalf("timed-out slices ended %v on the query line, want %s once", ended, test.want)
			}
		})
	}
}

// A Slot whose slice ran out of time for its size and was split completes,
// and the next Slot of its Query Group is read in the slices it landed: the
// slice too large is not read again whole, to run out of time and be split
// once a round. The backend times out that slice every time it is asked it.
func TestASliceSplitForItsTimeIsReadSplitTheRoundAfter(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
	// A Slot before that ran out of time read whole, its size not known
	// yet: the next is read in slices by that time.
	fixture.source.timeoutWhole = true
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	fixture.source.mu.Lock()
	fixture.source.timeoutWhole, fixture.source.queryMillis = false, 1_000
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = 1, 1
	fixture.source.mu.Unlock()
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 1 || result.Usage.InputSliceShapeKept != 1 {
		t.Fatalf("round 1: result=%+v err=%v, want it completed with one slice split for its time, kept", result, err)
	}
	landed := answeredSlices(fixture.source.calls[first:])
	first = len(fixture.source.calls)
	result, err = fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 1 {
		t.Fatalf("round 2: result=%+v err=%v, want it completed, read in the shape", result, err)
	}
	if result.Usage.InputSliceTimeouts != 0 {
		t.Fatalf("round 2 split %d slices for their time, want the slice split in round 1 read split", result.Usage.InputSliceTimeouts)
	}
	for _, call := range fixture.source.calls[first:] {
		if call.err != nil || call.timedOut {
			t.Fatalf("round 2 read slice %s: %v, want every slice answered", call.slice.Digest(), call.err)
		}
	}
	if got := answeredSlices(fixture.source.calls[first:]); !reflect.DeepEqual(got, landed) {
		t.Fatalf("round 2 read slices %v, want the ones round 1 landed %v", got, landed)
	}
}

// answeredSlices is the digests of the slices calls answered, sorted.
func answeredSlices(calls []slicedCall) []string {
	var digests []string
	for _, call := range calls {
		if call.err == nil && call.completed {
			digests = append(digests, call.slice.Digest())
		}
	}
	sort.Strings(digests)
	// A slice read ahead and read again is one slice.
	return slices.Compact(digests)
}

// shapedFixture is a fixture whose Query Group's slice ran out of time for
// its size in its first Slot read in slices and was split, the Slot
// completing: its shape kept. The backend times that slice out whenever it
// is asked it.
func shapedFixture(t *testing.T) (*slicedFixture, execution.SlotExecutionRequest) {
	t.Helper()
	fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
	fixture.source.timeoutWhole = true
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	fixture.source.mu.Lock()
	fixture.source.timeoutWhole, fixture.source.queryMillis = false, 1_000
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = 1, 1
	fixture.source.mu.Unlock()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || result.Usage.InputSliceShapeKept != 1 {
		t.Fatalf("result=%+v err=%v, want the slice split for its time kept", result, err)
	}
	return fixture, request
}

// A shape whose last kept split's leaves were each read whole under half a
// slice's target three rounds in a row (sliceDecayRounds) has that split
// taken back on the third: with no kept split left, the Query Group is read
// in slices counted anew the round after. Two quick rounds keep it.
func TestAShapeIsMergedBackAfterItsLeavesAreQuick(t *testing.T) {
	fixture, request := shapedFixture(t)
	const decayRounds = 3 // sliceDecayRounds
	for round := 1; round <= decayRounds; round++ {
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 1 {
			t.Fatalf("quick round %d: result=%+v err=%v, want it read in the shape", round, result, err)
		}
		if merged := result.Usage.InputSliceShapeMerged == 1; merged != (round == decayRounds) {
			t.Fatalf("quick round %d merged %t, want it merged on round %d only", round, merged, decayRounds)
		}
	}
	// The backend answers the slice again, as it would once it is no longer
	// too large to.
	fixture.source.mu.Lock()
	fixture.source.timedOut = nil
	fixture.source.mu.Unlock()
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 0 {
		t.Fatalf("after the merge: result=%+v err=%v, want it read in slices counted anew", result, err)
	}
}

// Under the query cooldown a slice the provider times out is no slice too
// large, slices before it quick or not: it completes UNAVAILABLE and the
// Slot degraded, and the next Slot asks it as it was, not split.
func TestUnderTheCooldownASliceTheProviderTimesOutIsAskedAgainAsItWas(t *testing.T) {
	fixture, request := shapedFixture(t)
	fixture.source.mu.Lock()
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = fixture.source.answered+1, 1
	fixture.source.mu.Unlock()
	request.QueryCooldownHeld = true
	first := len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed ||
		result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeKept != 0 {
		t.Fatalf("result=%+v err=%v, want it completed degraded, nothing split or kept", result, err)
	}
	var timedOut string
	for _, call := range fixture.source.calls[first:] {
		if call.timedOut {
			timedOut = call.slice.Digest()
		}
	}
	if timedOut == "" {
		t.Fatal("no slice ran out of time")
	}
	fixture.source.mu.Lock()
	fixture.source.timeoutTimes, fixture.source.timedOut = 0, nil
	fixture.source.mu.Unlock()
	first = len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 1 {
		t.Fatalf("read again: result=%+v err=%v, want it completed in the shape", result, err)
	}
	asked := false
	for _, call := range fixture.source.calls[first:] {
		asked = asked || call.slice.Digest() == timedOut
	}
	if !asked {
		t.Fatalf("read again, the slice %s the provider timed out was not asked as it was", timedOut)
	}
}

// A slice that has to be split twice to answer - split for its time, and a
// child of it out of time too - fails its first Slot and is read a level
// deeper by each Slot after, until one completes: that Slot splits the
// last for its time, the one after reads every slice answered.
func TestASliceThatNeedsTwoLevelsIsReadDeeperRoundByRound(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
	fixture.source.timeoutWhole = true
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	fixture.source.mu.Lock()
	fixture.source.timeoutWhole, fixture.source.queryMillis = false, 1_000
	// The slice, its child and its grandchild each run out of time once
	// asked, the slices before them answered.
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = 1, 3
	fixture.source.mu.Unlock()
	if _, err := fixture.coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("round 1, the slice and its child out of time, completed")
	}
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 1 || result.Usage.InputSliceShapeKept != 1 {
		t.Fatalf("round 2: result=%+v err=%v, want it completed a level deeper, the last split for its time and kept", result, err)
	}
	first := len(fixture.source.calls)
	result, err = fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeStarted != 1 {
		t.Fatalf("round 3: result=%+v err=%v, want it completed in the shape, nothing out of time", result, err)
	}
	for _, call := range fixture.source.calls[first:] {
		if call.err != nil || call.timedOut {
			t.Fatalf("round 3 slice %s: %v, want every slice answered", call.slice.Digest(), call.err)
		}
	}
}

// The first slice of a Slot read in a shape that runs out of time has no
// slice before it to be put on its size against: it is against what it was
// planned to take, its series the round before at the time a series took
// then, and split and read again for twice that - not left to the cooldown
// round after round.
func TestTheFirstSliceOfAShapeOutOfTimeIsSplit(t *testing.T) {
	fixture, request := shapedFixture(t)
	fixture.source.mu.Lock()
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = fixture.source.answered, 1
	fixture.source.mu.Unlock()
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 1 || result.Usage.InputSliceTimeouts != 1 {
		t.Fatalf("result=%+v err=%v, want the shape's first slice split for its time and the Slot completed", result, err)
	}
	if calls := fixture.source.calls[first:]; len(calls) == 0 || !calls[0].timedOut {
		t.Fatal("the shape's first slice did not run out of time")
	}
}

// slowBackend makes every slice the fixture is asked from now on run out of
// time, at the 30 s a read out of time is counted at, as a backend slow as a
// whole does.
func slowBackend(fixture *slicedFixture) {
	fixture.source.mu.Lock()
	defer fixture.source.mu.Unlock()
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = fixture.source.answered, 1_000
}

// firstSliceOf is the digest of the first slice a Slot read asked.
func firstSliceOf(t *testing.T, fixture *slicedFixture, from int) string {
	t.Helper()
	calls := fixture.source.calls[from:]
	if len(calls) == 0 {
		t.Fatal("the Slot asked no slice")
	}
	return calls[0].slice.Digest()
}

// A backend slow as a whole times a shape's first slice out with no slice
// before it to measure by: under the query cooldown that is no slice too
// large. The Slot fails and its shape is not made deeper - read again, it
// asks the same first slice.
func TestTheFirstSliceOfAShapeOutOfTimeUnderTheCooldownDeepensNothing(t *testing.T) {
	fixture, request := shapedFixture(t)
	slowBackend(fixture)
	request.QueryCooldownHeld = true
	var firsts []string
	for round := 0; round < 2; round++ {
		from := len(fixture.source.calls)
		if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
			t.Fatalf("round %d with every slice out of time under the cooldown: result=%+v err=%v, want it completed degraded", round, result, err)
		}
		firsts = append(firsts, firstSliceOf(t, fixture, from))
	}
	if firsts[0] != firsts[1] {
		t.Fatalf("under the cooldown the shape's first slice went %s to %s: made deeper", firsts[0], firsts[1])
	}
}

// Before the query cooldown holds, a backend slow as a whole has a shape
// split by each Slot that fails so - twice at most, three Slots in a row
// before the scheduler holds the cooldown (unavailableThreshold): 24 leaves
// more at most - and by none once it holds. The leaves are read as a Slot
// that completes after lands them.
func TestAShapeGrowsOnlyUntilTheCooldownHolds(t *testing.T) {
	const beforeCooldown = 3 // scheduler unavailableThreshold
	// landedAfter is the slices a Slot completing after rounds failing so
	// lands: rounds before the cooldown and held more under it.
	landedAfter := func(t *testing.T, held int) (before, after uint64) {
		fixture, request := shapedFixture(t)
		fixture.source.mu.Lock()
		fixture.source.timedOut = nil
		fixture.source.mu.Unlock()
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed {
			t.Fatalf("the shape read before: result=%+v err=%v", result, err)
		}
		before = result.Usage.InputSlices
		slowBackend(fixture)
		for round := 0; round < beforeCooldown+held; round++ {
			request.QueryCooldownHeld = round >= beforeCooldown
			// Before the cooldown each fails, its shape split; under it each
			// completes degraded, nothing put on a slice's size.
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if request.QueryCooldownHeld != (err == nil && result.Completed) {
				t.Fatalf("round %d with every slice out of time, cooldown %t: result=%+v err=%v", round, request.QueryCooldownHeld, result, err)
			}
		}
		fixture.source.mu.Lock()
		fixture.source.timedOut, fixture.source.timeoutTimes = nil, 0
		fixture.source.mu.Unlock()
		request.QueryCooldownHeld = false
		result, err = fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 1 {
			t.Fatalf("the Slot after: result=%+v err=%v, want it completed in the shape", result, err)
		}
		return before, result.Usage.InputSlices
	}
	before, grown := landedAfter(t, 0)
	_, held := landedAfter(t, 2)
	if grown <= before || grown-before > 24 {
		t.Fatalf("the shape went from %d to %d slices before the cooldown, want it grown by 24 at most", before, grown)
	}
	if held != grown {
		t.Fatalf("the shape grew to %d slices under the cooldown from %d, want none more", held, grown)
	}
}

// A slice out of time for its size that the window left is too short to
// split fails its Slot, split into its tree: the next Slot reads it split.
func TestASliceNotSplitForTheWindowIsReadSplitTheRoundAfter(t *testing.T) {
	fixture, request := shapedFixture(t)
	fixture.source.mu.Lock()
	fixture.source.timeoutAfter, fixture.source.timeoutTimes = fixture.source.answered+1, 1
	fixture.source.mu.Unlock()
	// Less left than the children of a slice out of time take, at the
	// backend's 30 s.
	short, cancel := context.WithTimeout(context.Background(), 3*time.Second) // fixed-wait: a deadline the window is judged against, not a wait: 3 s is under any slice's children at 30 s
	_, err := fixture.coordinator.Execute(short, request)
	cancel()
	if err == nil {
		t.Fatal("a Slot with a slice out of time and no window to split it completed")
	}
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeStarted != 1 {
		t.Fatalf("the Slot after: result=%+v err=%v, want it completed in the shape with the slice split", result, err)
	}
}

// A shape is voided once its Query Group is grouped by other dimensions: the
// Slot that finds the change completes in the slices it started in, and the
// next is read in slices counted anew.
func TestAShapeIsVoidedWhenItsQueryGroupIsGroupedOtherwise(t *testing.T) {
	fixture, request := shapedFixture(t)
	fixture.source.mu.Lock()
	fixture.source.facts = &execution.QueryPlanFacts{Provider: execution.ProviderUQ, QueryList: []execution.QueryClause{{
		TableID: "system.cpu_summary", Dimensions: []string{"host", "instance"},
		Functions: []execution.QueryFunction{{Method: "mean", Dimensions: []string{"host", "instance"}}},
	}}}
	fixture.source.timedOut = nil
	fixture.source.mu.Unlock()
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeVoidedDimensions != 1 {
		t.Fatalf("the Slot grouped otherwise: result=%+v err=%v, want it completed and the shape voided", result, err)
	}
	result, err = fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 0 {
		t.Fatalf("the Slot after: result=%+v err=%v, want it read with no shape", result, err)
	}
}

// A Query Group sliced for a read that ran out of time keeps its slices
// the rounds after they answered - read whole the next round, it would run
// out again, every other round - and is read whole again only after
// sliceDecayRounds rounds of slices that all answered quickly.
func TestASliceLearnedFromATimeoutIsKeptUntilItsReadsAreQuick(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
	fixture.source.timeoutWhole, fixture.source.queryMillis = true, 1_000
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	fixture.source.timeoutWhole = false
	const decayRounds = 3 // sliceDecayRounds
	sliced := func(round int) bool {
		first := len(fixture.source.calls)
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed {
			t.Fatalf("round %d: result=%+v err=%v, want it completed", round, result, err)
		}
		calls := fixture.source.calls[first:]
		return len(calls) > 1 && !calls[0].slice.IsZero()
	}
	for round := 2; round <= 1+decayRounds; round++ {
		if !sliced(round) {
			t.Fatalf("round %d after the timeout read whole, want it kept in slices", round)
		}
	}
	if sliced(2 + decayRounds) {
		t.Fatalf("after %d quick rounds in slices the Query Group is still sliced, want one fewer: whole", decayRounds)
	}
}

// A Query Group known small whose read whole runs out of time - a backend
// slow as a whole - is read whole again: the time is put on no slice's size
// and slices nothing.
func TestASmallQueryGroupThatRanOutOfTimeIsReadWholeAgain(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
	fixture.source.queryMillis = 1_000
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the first read: result=%+v err=%v, want it completed", result, err)
	}
	fixture.source.timeoutWhole = true
	readWholeTimedOut(t, fixture, request)
	fixture.source.timeoutWhole = false
	first := len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the read after: result=%+v err=%v, want it completed", result, err)
	}
	if calls := fixture.source.calls[first:]; len(calls) != 1 || !calls[0].slice.IsZero() {
		t.Fatalf("the read after the timeout made %d reads (first whole %t), want one read whole", len(calls),
			len(calls) > 0 && calls[0].slice.IsZero())
	}
}

// The slices a Query Group is read in do not grow on their own: a tree holds
// at least the slices asked, and each round asks what the round before was
// started in, not what its tree landed. 24 numbered hosts of 45 KiB: the
// first Slot, read whole, lets it go at the ninth over the slice limit (384
// KiB, half the share) and cuts its root by that sample, last digits 0 to 8
// - [012], [34], [56] and the rest, 9, 5, 4 and 6 hosts. Every round after
// starts on that cut (three slices asked, 1,080 KiB over half the share),
// and [012], 405 KiB, is past the limit and split as it streams: every round
// lands more slices than it was started in, the tree asking the landed count
// would hold more again. Five rounds in a row, reads answered without being
// quick (no decay), land the same slices.
func TestTheSlicesAQueryGroupIsReadInDoNotGrowRoundByRound(t *testing.T) {
	fixture := newSlicedFixture(t, slicedHosts(24, 45<<10), sliceableFacts(), 200)
	fixture.source.queryMillis = 10_000
	request := windowedRequest()
	if first, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !first.Completed ||
		!fixture.source.calls[0].slice.IsZero() || fixture.source.calls[0].completed {
		t.Fatalf("the first Slot result=%+v err=%v, want the whole group read, let go and read in slices", first, err)
	}
	var first []string
	for round := 2; round <= 6; round++ {
		before := len(fixture.source.calls)
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed {
			t.Fatalf("round %d: result=%+v err=%v, want it completed", round, result, err)
		}
		if result.Usage.InputSlicesDiscarded == 0 {
			t.Fatalf("round %d split no slice as it streamed, want the digits' slice past the limit: more landed than started", round)
		}
		var slices []string
		for _, call := range fixture.source.calls[before:] {
			if call.completed {
				slices = append(slices, call.slice.Digest())
			}
		}
		sort.Strings(slices)
		if len(slices) < 2 {
			t.Fatalf("round %d landed %d slices, want the Slot in slices", round, len(slices))
		}
		if first == nil {
			first = slices
			continue
		}
		if !reflect.DeepEqual(slices, first) {
			t.Fatalf("round %d landed %d slices %v, round 2 %d %v: want the same slices every round", round, len(slices), slices, len(first), first)
		}
	}
}
