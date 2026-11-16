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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// These cases replay a Redis failover that returns Slot Progress to an earlier Slot, on a real
// redis-server, through the production coordinator, the production State and
// gap store and the production Progress store under a real owner fence:
//
//	k-1 commits; k commits; k+1 begins. The store fails over to a replica
//	that has k-1's writes and nothing after them -- State(k), Progress(k)
//	and begin(k+1) are gone. k+1, already running, reads State(k-1), writes
//	State(k+1), and its Progress commit is refused: Progress says k is next.
//
// The failover is a snapshot of every key taken after k-1 and put back, keys
// and lifetimes, in the middle of k+1: after its begin, before it reads
// anything. Nothing about the shape is built by hand -- the later State is
// the one the later Slot wrote.

const (
	failoverQueryGroup = "query-group"
	failoverWorker     = "worker-failover"
	failoverStatePfx   = "alarmd-failover-state"
	failoverOwnerPfx   = "alarmd-failover-owner"
	failoverProgPfx    = "alarmd-failover-progress"
)

var (
	failoverSeriesA = strings.Repeat("a", 64)
	failoverSeriesB = strings.Repeat("b", 64)
)

// failoverHarness is one Query Group on a fresh redis-server, run Slot by
// Slot through the production coordinator.
type failoverHarness struct {
	t           *testing.T
	client      *redis.Client
	state       *state.ExecutionStore
	progress    *progress.Store
	ports       *recordingPorts
	coordinator *worker.SlotExecutionCoordinator
	fence       execution.OwnerFence
	recorder    *metric.Recorder
	resolve     *failoverResolve

	mu           sync.Mutex
	observations []observability.Observation
}

// failoverResolve is the finalization port with the failover in it: armed,
// the next Slot's first step after its begin puts the snapshot back.
type failoverResolve struct {
	next     execution.QueryFreeFinalizationSource
	snapshot func()
	armed    bool
}

func (resolve *failoverResolve) ResolveFinalization(ctx context.Context, request execution.SlotExecutionRequest) (execution.QueryFreeFinalization, error) {
	if resolve.armed {
		resolve.armed = false
		resolve.snapshot()
	}
	return resolve.next.ResolveFinalization(ctx, request)
}

// failoverPlans answers the admitter for the one Plan these Slots run, under
// the activation the recording ports report.
type failoverPlans struct{}

func (failoverPlans) IsPlanActive(_ context.Context, _ execution.FrozenExecutionContractRef, plan execution.PlanKey, epoch execution.StateApplyEpoch) (bool, error) {
	return plan.PlanIdentity == planIdentity() && plan.ShardIndex == 0 && epoch == 1, nil
}

func newFailoverHarness(t *testing.T) *failoverHarness {
	t.Helper()
	ctx := context.Background()
	address := redistest.Start(t, "--protected-mode", "no").Addr
	h := &failoverHarness{t: t, recorder: metric.NewRecorder(metric.BuildInfo{})}
	h.client = redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = h.client.Close() })
	owner := openG3AOwnershipStore(t, address, failoverOwnerPfx)
	var backend *state.RedisBackend
	h.state, backend = openG3AStateStore(t, address, failoverStatePfx)
	t.Cleanup(func() { _ = backend.Close() })
	h.progress = openG3AProgressStore(t, owner, failoverProgPfx)
	now := time.Now().Truncate(time.Millisecond)
	authority, err := owner.AcquireControlLeader(ctx, "control-failover", now, 10*time.Minute)
	if err != nil {
		t.Fatalf("acquire control leader: %v", err)
	}
	if _, err := owner.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: failoverQueryGroup, DesiredWorkerID: failoverWorker,
		PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatalf("publish assignment: %v", err)
	}
	lease, err := owner.Acquire(ctx, failoverQueryGroup, failoverWorker, now, 10*time.Minute)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	h.fence = lease.Fence
	admitter, err := ownership.NewAdmitter(owner, failoverPlans{})
	if err != nil {
		t.Fatal(err)
	}
	observer := observability.Multi(h.recorder, observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
		// The Slot's coordinates ride on the context, as the logger reads
		// them.
		if observation.Trace.EvaluationTime == 0 {
			observation.Trace.EvaluationTime = observability.TraceFieldsFromContext(ctx).EvaluationTime
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.observations = append(h.observations, observation)
	}))
	h.ports, _, h.coordinator = workerG4CoordinatorWith(t, observer, nil, func(ports *worker.Ports) {
		h.resolve = &failoverResolve{next: ports.Finalization}
		ports.Finalization = h.resolve
		ports.State, ports.GapGuard, ports.Progress, ports.Admission = h.state, h.state, h.progress, admitter
	})
	return h
}

// snapshot is every key with what is left of its life, and the function that
// puts the database back to exactly that.
func (h *failoverHarness) snapshot() func() {
	h.t.Helper()
	ctx := context.Background()
	keys, err := h.client.Keys(ctx, "*").Result()
	if err != nil {
		h.t.Fatalf("list keys: %v", err)
	}
	type saved struct {
		dump string
		ttl  time.Duration
	}
	values := make(map[string]saved, len(keys))
	for _, key := range keys {
		dump, err := h.client.Dump(ctx, key).Result()
		if err != nil {
			h.t.Fatalf("dump %s: %v", key, err)
		}
		ttl, err := h.client.PTTL(ctx, key).Result()
		if err != nil {
			h.t.Fatalf("ttl %s: %v", key, err)
		}
		if ttl < 0 {
			ttl = 0
		}
		values[key] = saved{dump: dump, ttl: ttl}
	}
	return func() {
		if err := h.client.FlushDB(ctx).Err(); err != nil {
			h.t.Errorf("failover flush: %v", err)
			return
		}
		for key, value := range values {
			if err := h.client.RestoreReplace(ctx, key, value.ttl, value.dump).Err(); err != nil {
				h.t.Errorf("failover restore %s: %v", key, err)
			}
		}
	}
}

// run executes the Slot at the given time for the given series.
func (h *failoverHarness) run(at execution.EvaluationTime, operation execution.Operation, series ...string) (execution.SlotExecutionResult, error) {
	h.t.Helper()
	header, batches, completion := failoverRound(h.t, at, series...)
	h.ports.executeOverride = streamExecution(header, batches, completion)
	request := workerSlotRequest(header.Contract)
	request.Operation = operation
	request.OwnerFence = h.fence
	return h.coordinator.Execute(context.Background(), request)
}

func (h *failoverHarness) nextSlot() execution.EvaluationTime {
	h.t.Helper()
	loaded, err := h.progress.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: failoverQueryGroup})
	if err != nil || loaded.Progress == nil {
		h.t.Fatalf("load Progress = (%+v, %v)", loaded, err)
	}
	return loaded.Progress.NextSlot
}

func (h *failoverHarness) stateKey(series string) string {
	h.t.Helper()
	key, err := state.RuntimeStateKeyV3(failoverStatePfx, execution.StateKeyIdentity{
		Plan: planIdentity(), StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(series)})
	if err != nil {
		h.t.Fatal(err)
	}
	return key
}

func (h *failoverHarness) stateBytes(series string) string {
	h.t.Helper()
	raw, err := h.client.Get(context.Background(), h.stateKey(series)).Result()
	if err != nil {
		h.t.Fatalf("read State of %s: %v", series[:4], err)
	}
	return raw
}

// stateTime is the evaluation time the stored State of a series was written
// at, read through the store as the next Slot reads it.
func (h *failoverHarness) stateTime(at execution.EvaluationTime, series string) execution.EvaluationTime {
	h.t.Helper()
	header, _, _ := failoverRound(h.t, at, series)
	version, err := execution.BuildApplyVersion(header.Contract, 1)
	if err != nil {
		h.t.Fatal(err)
	}
	loaded, err := h.state.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: header.Contract,
		Items: []execution.StatePreflightItem{{Identity: execution.StateKeyIdentity{Plan: planIdentity(), StateGeneration: "state-v1",
			SeriesIdentityDigest: execution.SeriesIdentityDigest(series)}, ApplyVersion: version}}})
	if err != nil || len(loaded.Items) != 1 {
		h.t.Fatalf("load State = (%+v, %v)", loaded, err)
	}
	return loaded.Items[0].PersistedApplyVersion.EvaluationTime
}

// skipLines is how many observations named a series skipped before its
// evaluation, and coverage the history coverage of the progress commit of
// the Slot at the given time, as the line renders it.
func (h *failoverHarness) skipLines(at execution.EvaluationTime) (int, string) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	skips, coverage := 0, ""
	for _, observation := range h.observations {
		if observation.Stage == observability.StageMutationCompared &&
			string(observation.ReasonCode) == "state_superseded_before_evaluation" {
			skips++
		}
		if observation.Stage == observability.StageProgressCommitted && observation.Err == nil &&
			observation.HistoryCoverage != nil && observation.Trace.EvaluationTime == int64(at) {
			raw, err := json.Marshal(observation.HistoryCoverage)
			if err != nil {
				h.t.Fatal(err)
			}
			coverage = string(raw)
		}
	}
	return skips, coverage
}

// supersededCount reads alarmd_state_superseded_total off the registry, zero
// for a family the build does not register.
func (h *failoverHarness) supersededCount(site, operation string) float64 {
	h.t.Helper()
	families, err := h.recorder.Gatherer().Gather()
	if err != nil {
		h.t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "alarmd_state_superseded_total" {
			continue
		}
		for _, sample := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range sample.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["site"] == site && labels["operation"] == operation {
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// failover runs k-1 and k to completion, then k+1 across the failover, and
// returns k. series are the series k-1 and k carry; later those k+1 carries.
func (h *failoverHarness) failover(series, later []string) execution.EvaluationTime {
	h.t.Helper()
	previous := frozenContract().Slot.EvaluationTime
	slot, next := previous+60, previous+120
	if result, err := h.run(previous, execution.OperationNormal, series...); err != nil || !result.Completed {
		h.t.Fatalf("k-1 = (%+v, %v), want completed", result, err)
	}
	afterPrevious := h.snapshot()
	if result, err := h.run(slot, execution.OperationNormal, series...); err != nil || !result.Completed {
		h.t.Fatalf("k = (%+v, %v), want completed", result, err)
	}
	if got := h.nextSlot(); got != next {
		h.t.Fatalf("Progress after k names %d, want k+1 (%d)", got, next)
	}
	h.resolve.snapshot, h.resolve.armed = afterPrevious, true
	result, err := h.run(next, execution.OperationNormal, later...)
	if err == nil || result.Completed || !strings.Contains(err.Error(), "progress not committed") {
		h.t.Fatalf("k+1 across the failover = (%+v, %v), want State written and the Progress commit refused", result, err)
	}
	if got := h.nextSlot(); got != slot {
		h.t.Fatalf("Progress after the failover names %d, want k (%d)", got, slot)
	}
	for _, s := range later {
		if got := h.stateTime(slot, s); got != next {
			h.t.Fatalf("State of %s after the failover is at %d, want k+1's (%d)", s[:4], got, next)
		}
	}
	return slot
}

// TestASlotProgressReturnedToAfterAFailoverSkipsWhatTheLaterSlotWrote is the
// failover end to end, for each operation the Slot Progress came back to
// is run under: a replay, and a normal round, which is what the Runner runs
// once Progress is behind. The State that failed over is a later Slot's, so
// the Slot skips the series, completes, and the Query Group moves on --
// where it used to refuse STATE_STALE_VERSION on every attempt until its
// replay expired.
func TestASlotProgressReturnedToAfterAFailoverSkipsWhatTheLaterSlotWrote(t *testing.T) {
	for _, operation := range []execution.Operation{execution.OperationReplay, execution.OperationNormal} {
		t.Run(string(operation), func(t *testing.T) {
			h := newFailoverHarness(t)
			slot := h.failover([]string{failoverSeriesA}, []string{failoverSeriesA})
			later := h.stateBytes(failoverSeriesA)
			events := h.ports.eventCount

			result, err := h.run(slot, operation, failoverSeriesA)
			if err != nil || !result.Completed {
				t.Fatalf("k after the failover = (%+v, %v), want completed", result, err)
			}
			if got := h.nextSlot(); got != slot+60 {
				t.Fatalf("Progress after k names %d, want k+1 (%d)", got, slot+60)
			}
			if h.stateBytes(failoverSeriesA) != later || h.ports.eventCount != events {
				t.Fatalf("k wrote over the later Slot's State or sent events for it: events %d -> %d", events, h.ports.eventCount)
			}
			// Counted under the operation it ran as, at the state site.
			if got := h.supersededCount("state", string(operation)); got != 1 {
				t.Fatalf("state_superseded_total{site=state,operation=%s} = %v, want 1", operation, got)
			}
			// One line for the series, under its own word, and the round
			// that commits says how much of it was not evaluated.
			if skips, coverage := h.skipLines(slot); skips != 1 || !strings.Contains(coverage, `"superseded":1`) {
				t.Fatalf("skip lines = %d, progress coverage = %s; want one, and the coverage counting it", skips, coverage)
			}
			// And the Query Group is not held: k+1 runs again and finds its
			// own State there, applied.
			if result, err := h.run(slot+60, execution.OperationNormal, failoverSeriesA); err != nil || !result.Completed {
				t.Fatalf("k+1 after k = (%+v, %v), want completed", result, err)
			}
			if got := h.nextSlot(); got != slot+120 {
				t.Fatalf("Progress after k+1 names %d, want %d", got, slot+120)
			}
		})
	}
}

// TestASlotSkipsOnlyTheSeriesALaterSlotWroteAndWritesTheRest has two series
// in the Slot Progress came back to: one the later Slot wrote, one it did
// not carry. Skipping per series keeps the second one's decision; ending the
// whole Slot would have lost it.
func TestASlotSkipsOnlyTheSeriesALaterSlotWroteAndWritesTheRest(t *testing.T) {
	h := newFailoverHarness(t)
	both := []string{failoverSeriesA, failoverSeriesB}
	slot := h.failover(both, []string{failoverSeriesA})
	if got := h.stateTime(slot, failoverSeriesB); got != slot-60 {
		t.Fatalf("State of the series k+1 did not carry is at %d, want k-1's (%d)", got, slot-60)
	}
	later := h.stateBytes(failoverSeriesA)

	result, err := h.run(slot, execution.OperationReplay, both...)
	if err != nil || !result.Completed {
		t.Fatalf("k after the failover = (%+v, %v), want completed", result, err)
	}
	if h.stateBytes(failoverSeriesA) != later {
		t.Fatal("the later Slot's State was written over")
	}
	if got := h.stateTime(slot, failoverSeriesB); got != slot {
		t.Fatalf("State of the other series is at %d, want k's (%d)", got, slot)
	}
	if got := h.nextSlot(); got != slot+60 {
		t.Fatalf("Progress names %d, want k+1 (%d)", got, slot+60)
	}
	if got := h.supersededCount("state", "replay"); got != 1 {
		t.Fatalf("state_superseded_total{site=state,operation=replay} = %v, want 1: only the one series", got)
	}
}

// failoverRound is the frozen Slot at the given time for the given series,
// the G4 ring-ratio Plan with its two queries, each series one batch per
// query, its records named as the store requires: the id the series and
// source time derive.
func failoverRound(t *testing.T, at execution.EvaluationTime, series ...string) (
	execution.InternalExecutionHeader, []execution.SeriesExecutionBatch, execution.QueryExecutionCompletion,
) {
	t.Helper()
	compiled, requirements := workerG4CompiledPlan(t, strategy.DetectorKindSimpleRingRatio)
	identity := planIdentity()
	deadline := (int64(at) + 60) * 1000
	due := execution.DuePlan{Identity: identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule-v1", CompletionDeadlineUnixMilli: deadline}
	consumer := execution.ConsumerRef{Plan: identity, LevelID: 5, HasLevel: true}
	for index := range requirements {
		requirements[index].Consumers = []execution.DataRequirementConsumer{{Consumer: consumer,
			ConsumerDeadlineUnixMilli: deadline, DownstreamExecutionReserveMilliSec: 5_000}}
	}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.Slot.EvaluationTime = at
	contractRef.QueryRevision = "query-primary"
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{ExecutionID: fmt.Sprintf("l101-%d", at), Contract: contractRef,
		DuePlans: []execution.DuePlan{due}, Requirements: requirements, DeadlineUnixMilli: deadline}
	var batches []execution.SeriesExecutionBatch
	var completions []execution.PhysicalQueryCompletion
	for _, requirement := range requirements {
		query := execution.PlannedPhysicalQueryRef{
			Digest:        execution.PhysicalQueryDigest("physical-" + string(requirement.RequirementID)),
			QueryRevision: execution.QueryRevision(requirement.LogicalQueryRef),
		}
		header.RequiredPhysicalQueries = append(header.RequiredPhysicalQueries, query)
		providerRef := execution.ProviderResultRef("provider-" + string(requirement.RequirementID))
		sourceTime := int64(at) + requirement.RelativeWindow.EndOffsetSeconds - 1
		var delivery execution.SeriesDelivery
		for index, digest := range series {
			recordID, err := contract.DeriveRecordIDV2(digest, sourceTime)
			if err != nil {
				t.Fatal(err)
			}
			dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
				RecordID: recordID, SourceTime: sourceTime, BusinessID: "2",
				DimensionIdentity: contract.DimensionIdentityV2{Digest: digest},
				Values:            map[string]json.RawMessage{"value": json.RawMessage(`1`)},
				Dimensions:        map[string]json.RawMessage{}, ReceivedTime: sourceTime,
			}})
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			binding := execution.NamedInputBinding{
				Consumer: consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName,
				Role: requirement.Role, ProviderResult: providerRef, QueryWindow: requirement.AbsoluteWindow(at),
				Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactSeries,
				Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1},
			}
			batchDelivery := execution.SeriesDelivery{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
				Series: 1, Records: 1, Bytes: 64, Digest: fmt.Sprintf("%s-%d", digest[:8], index)}
			if delivery, err = execution.AccumulateSeriesDelivery(delivery, batchDelivery); err != nil {
				t.Fatal(err)
			}
			batches = append(batches, execution.SeriesExecutionBatch{
				PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision, CompletionRef: providerRef,
				Dataset: dataset, Inputs: []execution.NamedInputBinding{binding}, Delivery: batchDelivery,
			})
		}
		completions = append(completions, execution.PhysicalQueryCompletion{Ref: providerRef, PhysicalQuery: query.Digest,
			QueryRevision: query.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Delivery: delivery})
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: completions}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completions)
	return header, batches, completion
}
