// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

var (
	hotSeries  = execution.SeriesIdentityDigest(strings.Repeat("a", 64))
	calmSeries = execution.SeriesIdentityDigest(strings.Repeat("b", 64))
)

// supplementHeader is one Slot of a one-Level threshold Plan (value >= 50 is
// anomalous, one anomaly in a window of one triggers, recovery off, so a
// series under the threshold is NORMAL when its window is whole) over one
// primary query.
func supplementHeader(t *testing.T) execution.InternalExecutionHeader {
	t.Helper()
	return supplementHeaderWith(t, nil)
}

// supplementHeaderWith is supplementHeader with the Plan detecting no-data
// as given.
func supplementHeaderWith(t *testing.T, noData *contract.NoDataConfigV1) execution.InternalExecutionHeader {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "worker-supplement-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"},
		BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent",
		MissingValuePolicy: contract.MissingValuePolicyRequired}
	threshold := json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection, NoData: noData,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2},
			StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries,
				QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			Levels: []contract.LevelIRV2{{Definition: contract.LevelDefinitionV2{LevelID: 4, Priority: 1},
				Connector:  contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: strategy.DetectorKindThreshold, Version: 1, Config: threshold}}},
				TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
					Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1,
					Config: json.RawMessage(`{"enabled":false,"consecutive_windows":0}`)},
			}}}}
	compiled, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64),
			NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-v1",
			HistoryCellSemanticsVersion: "history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	built, ok := compiled.Plan()
	if !ok {
		t.Fatalf("compile terminal=%+v levels=%+v", compiled.PlanTerminal(), compiled.LevelTerminals())
	}
	identity := planIdentity()
	due := execution.DuePlan{Identity: identity, CompiledPlan: built, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule-v1", CompletionDeadlineUnixMilli: 1_788_000_060_000}
	// A threshold declares no named inputs of its own: the Level reads the
	// primary query, as the multi-Level fixture wires it.
	primary := materializeWorkerRequirement(t, workerAlgorithmRequirementForLevel(t, 4, "primary",
		strategy.AlgorithmInputPrimary, -60, 0, nil, strategy.AlgorithmReadinessEager,
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"}}))
	primary.Consumers = []execution.DataRequirementConsumer{{
		Consumer:                  execution.ConsumerRef{Plan: identity, LevelID: 4, HasLevel: true},
		ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000,
	}}
	requirements := []execution.DataRequirement{primary}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.QueryRevision = execution.QueryRevision(requirements[0].LogicalQueryRef)
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{ExecutionID: "supplement", Contract: contractRef,
		DuePlans: []execution.DuePlan{due}, Requirements: requirements, DeadlineUnixMilli: due.CompletionDeadlineUnixMilli}
	header.RequiredPhysicalQueries = []execution.PlannedPhysicalQueryRef{{
		Digest: "physical-primary", QueryRevision: execution.QueryRevision(requirements[0].LogicalQueryRef)}}
	return header
}

// supplementRead is the Slot's query answering with one point per series,
// at the values given.
func supplementRead(t *testing.T, header execution.InternalExecutionHeader, values map[execution.SeriesIdentityDigest]string) (
	[]execution.SeriesExecutionBatch, execution.QueryExecutionCompletion,
) {
	t.Helper()
	requirement := header.Requirements[0]
	query := header.RequiredPhysicalQueries[0]
	provider := execution.ProviderResultRef("provider-primary")
	sourceTime := int64(header.Contract.Slot.EvaluationTime) + requirement.RelativeWindow.EndOffsetSeconds - 1
	series := make([]execution.SeriesIdentityDigest, 0, len(values))
	for identity := range values {
		series = append(series, identity)
	}
	sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
	var batches []execution.SeriesExecutionBatch
	var delivered execution.SeriesDelivery
	for index, identity := range series {
		recordID, err := contract.DeriveRecordIDV2(string(identity), sourceTime)
		if err != nil {
			t.Fatal(err)
		}
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: recordID, SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: string(identity)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(values[identity])},
			Dimensions:        map[string]json.RawMessage{}, ReceivedTime: sourceTime}})
		view, err := execution.NewDatasetView(dataset, []uint32{0})
		if err != nil {
			t.Fatal(err)
		}
		delivery := execution.SeriesDelivery{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
			Series: 1, Records: 1, Bytes: 64, Digest: fmt.Sprintf("%064x", index+1)}
		batches = append(batches, execution.SeriesExecutionBatch{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
			CompletionRef: provider, Dataset: dataset, Delivery: delivery, Inputs: []execution.NamedInputBinding{{
				Consumer: requirement.Consumers[0].Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role, ProviderResult: provider,
				QueryWindow: requirement.AbsoluteWindow(header.Contract.Slot.EvaluationTime), Dataset: dataset, View: view,
				Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactSeries,
				Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1},
			}}})
		if delivered, err = execution.AccumulateSeriesDelivery(delivered, delivery); err != nil {
			t.Fatal(err)
		}
	}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true,
		PhysicalQueries: []execution.PhysicalQueryCompletion{{Ref: provider, PhysicalQuery: query.Digest,
			QueryRevision: query.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Delivery: delivered}}}
	return batches, completion
}

func supplementRequest(contractRef execution.FrozenExecutionContractRef, series ...execution.SeriesIdentityDigest) execution.SlotExecutionRequest {
	request := workerSlotRequest(contractRef)
	sorted := append([]execution.SeriesIdentityDigest(nil), series...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	request.Operation, request.Supplement = execution.OperationSupplement, &execution.SupplementScope{Series: sorted}
	return request
}

// outcomesBySeries is every Level outcome the evaluation reached, by series.
func outcomesBySeries(results []execution.EvaluationResult) map[execution.SeriesIdentityDigest][]execution.LevelOutcomeKind {
	outcomes := make(map[execution.SeriesIdentityDigest][]execution.LevelOutcomeKind)
	for _, result := range results {
		for _, plan := range result.Plans {
			for _, outcome := range plan.LevelOutcomes {
				outcomes[outcome.SeriesIdentityDigest] = append(outcomes[outcome.SeriesIdentityDigest], outcome.Outcome)
			}
		}
	}
	return outcomes
}

func runSupplement(t *testing.T, header execution.InternalExecutionHeader, values map[execution.SeriesIdentityDigest]string,
	configure func(*recordingPorts), scope ...execution.SeriesIdentityDigest,
) (*recordingPorts, *recordingEvaluator, execution.SupplementFacts) {
	t.Helper()
	ports, evaluator, coordinator := workerG4Coordinator(t)
	if configure != nil {
		configure(ports)
	}
	batches, completion := supplementRead(t, header, values)
	ports.executeOverride = streamExecution(header, batches, completion)
	result, err := coordinator.Execute(context.Background(), supplementRequest(header.Contract, scope...))
	if err != nil || result.Supplement == nil {
		t.Fatalf("supplement result %+v error %v", result, err)
	}
	facts := *result.Supplement
	if facts.Decided() != facts.Candidates {
		t.Fatalf("facts %+v decide %d of %d", facts, facts.Decided(), facts.Candidates)
	}
	return ports, evaluator, facts
}

// A supplement evaluates its series under the Plan's gap marker as the Slot
// did, and writes no marker: where the marker withheld NORMAL from the Slot
// it withholds it from the supplement, and an ABNORMAL is raised as the Slot
// would have raised it. Nothing a Slot writes beside events and State is
// written: no marker, no Progress, no begun Slot.
func TestASupplementUnderAnOpenGuardWithholdsNormalAndStillRaisesAbnormal(t *testing.T) {
	header := supplementHeader(t)
	values := map[execution.SeriesIdentityDigest]string{hotSeries: "80", calmSeries: "10"}

	// Without a marker the calm series is NORMAL: the guard below is what
	// withholds it.
	_, evaluator, facts := runSupplement(t, header, values, func(ports *recordingPorts) { ports.gapMissing = true },
		hotSeries, calmSeries)
	if outcomes := outcomesBySeries(evaluator.results); len(outcomes[calmSeries]) != 1 ||
		outcomes[calmSeries][0] != execution.LevelOutcomeNormal || outcomes[hotSeries][0] != execution.LevelOutcomeAbnormal {
		t.Fatalf("outcomes without a marker %v", outcomes)
	}
	if facts.Candidates != 2 || facts.Admitted != 2 || facts.Points != 2 {
		t.Fatalf("facts without a marker %+v", facts)
	}

	// The fixture's marker stands on the Plan, warming.
	ports, evaluator, facts := runSupplement(t, header, values, nil, hotSeries, calmSeries)
	outcomes := outcomesBySeries(evaluator.results)
	for _, outcome := range outcomes[calmSeries] {
		if outcome == execution.LevelOutcomeNormal {
			t.Fatalf("NORMAL reached under an open marker: %v", outcomes)
		}
	}
	if len(outcomes[calmSeries]) != 1 || len(outcomes[hotSeries]) != 1 || outcomes[hotSeries][0] != execution.LevelOutcomeAbnormal {
		t.Fatalf("outcomes under the marker %v, want ABNORMAL raised and NORMAL withheld", outcomes)
	}
	if ports.eventCount != 1 || facts.Admitted != 2 || facts.Candidates != 2 {
		t.Fatalf("events %d facts %+v, want the ABNORMAL written and both series' State", ports.eventCount, facts)
	}
	if len(ports.gapMutations) != 0 || !isZeroProgressCommit(ports.lastProgress) || ports.lastBegin.OwnerFence != (execution.OwnerFence{}) {
		t.Fatalf("a supplement wrote a marker %+v, Progress %+v or began the Slot %+v",
			ports.gapMutations, ports.lastProgress, ports.lastBegin)
	}
	for _, stage := range *ports.trace {
		if stage == "gap_before" || stage == "gap_after" || stage == "progress_commit" {
			t.Fatalf("trace %v, want no marker or Progress write", *ports.trace)
		}
	}
}

// A series of the read outside the supplement's scope was the Slot's own
// and is not evaluated again, nor counted.
func TestASupplementEvaluatesOnlyTheSeriesItWasGiven(t *testing.T) {
	header := supplementHeader(t)
	ports, evaluator, facts := runSupplement(t, header,
		map[execution.SeriesIdentityDigest]string{hotSeries: "80", calmSeries: "10"},
		func(ports *recordingPorts) { ports.gapMissing = true }, calmSeries)
	outcomes := outcomesBySeries(evaluator.results)
	if _, evaluated := outcomes[hotSeries]; evaluated || len(outcomes[calmSeries]) != 1 {
		t.Fatalf("outcomes %v, want the scoped series only", outcomes)
	}
	if facts.Candidates != 1 || facts.Admitted != 1 || ports.eventCount != 0 {
		t.Fatalf("facts %+v events %d", facts, ports.eventCount)
	}
}

// rememberingState is the fake store that keeps what it was written: a
// series written at a version reads back found at that version.
type rememberingState struct {
	*recordingPorts
	applied map[execution.StateKeyIdentity]execution.StateMutation
}

func (store *rememberingState) LoadRuntime(ctx context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	result, err := store.recordingPorts.LoadRuntime(ctx, request)
	for index, item := range request.Items {
		mutation, written := store.applied[item.Identity]
		if !written {
			continue
		}
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateFoundReady, BlobRevision: 1,
			PersistedApplyVersion: mutation.ApplyVersion, PersistedMutationDigest: mutation.MutationDigest}
		for _, level := range mutation.Levels {
			view.Levels = append(view.Levels, execution.RuntimeLevelStateView{LevelID: level.LevelID,
				LevelStateCompatibility: level.LevelStateCompatibility, HistoryCompleteness: level.HistoryCompleteness,
				GapReasonCode: level.GapReasonCode, WarmupRequirementRef: level.WarmupRequirementRef})
		}
		result.Items[index] = view
	}
	return result, err
}

func (store *rememberingState) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	result, err := store.recordingPorts.ApplyRuntime(ctx, request)
	if err == nil {
		for _, item := range request.Items {
			store.applied[item.Identity] = item
		}
	}
	return result, err
}

// The same bucket is evaluated once. The Slot evaluates the series its read
// had; a supplement of the Slot given that series and a late one evaluates
// the late one only, the other's State being at the Slot already; and a
// second supplement of the Slot evaluates neither, both being there now.
func TestTheSameBucketIsEvaluatedOnceAcrossTheSlotAndItsSupplements(t *testing.T) {
	header := supplementHeader(t)
	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	store := &rememberingState{recordingPorts: ports, applied: map[execution.StateKeyIdentity]execution.StateMutation{}}
	coordinator := supplementCoordinator(t, ports, evaluator, store, 100)
	evaluations := func() map[execution.SeriesIdentityDigest]int {
		counted := make(map[execution.SeriesIdentityDigest]int)
		for series, outcomes := range outcomesBySeries(evaluator.results) {
			counted[series] = len(outcomes)
		}
		return counted
	}

	// The Slot: its read had the hot series only.
	batches, completion := supplementRead(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"})
	ports.executeOverride = streamExecution(header, batches, completion)
	slot := workerSlotRequest(header.Contract)
	slot.DuePlanTargets.DuePlanSetDigest = header.Contract.DuePlanSetDigest
	if result, err := coordinator.Execute(context.Background(), slot); err != nil || !result.Completed {
		t.Fatalf("Slot result %+v error %v", result, err)
	}
	if counted := evaluations(); counted[hotSeries] != 1 || ports.eventCount != 1 {
		t.Fatalf("Slot evaluations %v events %d", counted, ports.eventCount)
	}

	// A later read of the Slot has both.
	late := map[execution.SeriesIdentityDigest]string{hotSeries: "80", calmSeries: "90"}
	for round, want := range []execution.SupplementFacts{
		{Candidates: 2, Admitted: 1, Points: 1, CrossedT: 1},
		{Candidates: 2, CrossedT: 2},
	} {
		batches, completion = supplementRead(t, header, late)
		ports.executeOverride = streamExecution(header, batches, completion)
		result, err := coordinator.Execute(context.Background(), supplementRequest(header.Contract, hotSeries, calmSeries))
		if err != nil || result.Supplement == nil || *result.Supplement != want {
			t.Fatalf("supplement %d: facts %+v error %v, want %+v", round+1, result.Supplement, err, want)
		}
	}
	if counted := evaluations(); counted[hotSeries] != 1 || counted[calmSeries] != 1 || ports.eventCount != 2 {
		t.Fatalf("evaluations %v events %d, want each series evaluated and raised once", counted, ports.eventCount)
	}
}

// A Plan whose activation moved since the Slot was frozen is not the Plan
// the Slot evaluated: its series are config_drift, and nothing is written.
func TestASupplementOfAPlanWhoseActivationMovedWritesNothing(t *testing.T) {
	header := supplementHeader(t)
	ports, _, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"},
		func(ports *recordingPorts) {
			ports.gapMissing = true
			ports.activationChangeAt = 1
			ports.activationSelection = execution.ActivationNone
		}, hotSeries)
	if facts.ConfigDrift != 1 || facts.Admitted != 0 || ports.eventCount != 0 || ports.stateApplyCalls != 0 || len(ports.gapMutations) != 0 {
		t.Fatalf("facts %+v events %d state %d gaps %+v", facts, ports.eventCount, ports.stateApplyCalls, ports.gapMutations)
	}
}

// A Plan whose side effects are no longer admitted is config_drift too.
func TestASupplementOfAPlanNoLongerAdmittedWritesNothing(t *testing.T) {
	header := supplementHeader(t)
	ports, _, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"},
		func(ports *recordingPorts) { ports.gapMissing, ports.admissionRejectAt = true, 1 }, hotSeries)
	if facts.ConfigDrift != 1 || ports.eventCount != 0 || ports.stateApplyCalls != 0 {
		t.Fatalf("facts %+v events %d state %d", facts, ports.eventCount, ports.stateApplyCalls)
	}
}

// Output the sink would not take leaves the series' State where it was: the
// series are withheld, and a supplement is not retried for it.
func TestASupplementWhoseOutputIsNotAcknowledgedWithholdsTheState(t *testing.T) {
	header := supplementHeader(t)
	ports, _, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"},
		func(ports *recordingPorts) { ports.gapMissing, ports.failStage = true, "event_ack" }, hotSeries)
	if facts.Withheld != 1 || facts.Admitted != 0 || ports.stateApplyCalls != 0 {
		t.Fatalf("facts %+v state %d", facts, ports.stateApplyCalls)
	}
}

// A supplement request out of shape is refused before anything runs: the
// operation and the scope go together, and the scope names its series
// sorted, once each.
func TestASupplementRequestOutOfShapeIsRefusedBeforeAnythingRuns(t *testing.T) {
	header := supplementHeader(t)
	for name, mutate := range map[string]func(*execution.SlotExecutionRequest){
		"operation without scope": func(request *execution.SlotExecutionRequest) { request.Supplement = nil },
		"scope without operation": func(request *execution.SlotExecutionRequest) { request.Operation = execution.OperationNormal },
		"unsorted": func(request *execution.SlotExecutionRequest) {
			request.Supplement.Series = []execution.SeriesIdentityDigest{calmSeries, hotSeries}
		},
		"repeated": func(request *execution.SlotExecutionRequest) {
			request.Supplement.Series = []execution.SeriesIdentityDigest{hotSeries, hotSeries}
		},
		"empty": func(request *execution.SlotExecutionRequest) { request.Supplement.Series = nil },
		"unnamed": func(request *execution.SlotExecutionRequest) {
			request.Supplement.Series = []execution.SeriesIdentityDigest{""}
		},
	} {
		ports, _, coordinator := workerG4Coordinator(t)
		request := supplementRequest(header.Contract, hotSeries, calmSeries)
		mutate(&request)
		if _, err := coordinator.Execute(context.Background(), request); err == nil || len(*ports.trace) != 0 {
			t.Fatalf("%s: error %v trace %v, want a refusal before anything ran", name, err, *ports.trace)
		}
	}
}

// supplementCoordinator is the fixture's coordinator with its State port and
// event budget replaced.
func supplementCoordinator(t *testing.T, ports *recordingPorts, evaluator *recordingEvaluator, state execution.StateStore,
	maxEvents uint64,
) *worker.SlotExecutionCoordinator {
	t.Helper()
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports,
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness,
		Events: ports, State: state, Progress: ports,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: maxEvents, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

// A marker cleared at the Slot's own version stood when the Slot was
// evaluated, so it withholds NORMAL from the supplement as it did from the
// Slot; one cleared before the Slot does not.
func TestASupplementReadsAMarkerClearedAtItsSlotAsTheSlotDid(t *testing.T) {
	header := supplementHeader(t)
	due := header.DuePlans[0]
	atSlot, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	before := atSlot
	before.EvaluationTime -= 60
	for _, tc := range []struct {
		name     string
		clearing execution.ApplyVersion
		normal   bool
	}{{"cleared at the Slot", atSlot, false}, {"cleared before it", before, true}} {
		_, evaluator, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{calmSeries: "10"},
			func(ports *recordingPorts) {
				ports.activatedGapMarkers = map[execution.PlanGapIdentity]execution.GapGuardSnapshot{due.GapIdentity(): {
					Identity: due.GapIdentity(), Status: execution.GapClearedTombstone, MarkerRevision: 4,
					LastScheduleRevision: due.ScheduleRevision, PersistedApplyVersion: tc.clearing,
					PersistedMutationDigest: "cleared"}}
			}, calmSeries)
		outcomes := outcomesBySeries(evaluator.results)[calmSeries]
		if len(outcomes) != 1 || (outcomes[0] == execution.LevelOutcomeNormal) != tc.normal || facts.Admitted != 1 {
			t.Fatalf("%s: outcomes %v facts %+v, NORMAL wanted %v", tc.name, outcomes, facts, tc.normal)
		}
	}
}

// laterState is a store whose every series has State from a later Slot.
type laterState struct{ *recordingPorts }

func (store laterState) LoadRuntime(ctx context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	result, err := store.recordingPorts.LoadRuntime(ctx, request)
	for index, item := range request.Items {
		later := item.ApplyVersion
		later.EvaluationTime += 60
		result.Items[index] = execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateFoundReady,
			BlobRevision: 2, PersistedApplyVersion: later, PersistedMutationDigest: "later",
			Levels: []execution.RuntimeLevelStateView{{LevelID: 4, LevelStateCompatibility: "compatibility",
				WarmupRequirementRef: "warmup", HistoryCompleteness: execution.HistoryFull}}}
	}
	return result, err
}

// A series a later Slot has decided is crossed_t: it is not evaluated at the
// Slot behind it, and nothing is written for it.
func TestASeriesALaterSlotDecidedIsNotSupplemented(t *testing.T) {
	header := supplementHeader(t)
	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	coordinator := supplementCoordinator(t, ports, evaluator, laterState{ports}, 100)
	batches, completion := supplementRead(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"})
	ports.executeOverride = streamExecution(header, batches, completion)
	result, err := coordinator.Execute(context.Background(), supplementRequest(header.Contract, hotSeries))
	if err != nil || result.Supplement == nil || *result.Supplement != (execution.SupplementFacts{Candidates: 1, CrossedT: 1}) ||
		len(evaluator.requests) != 0 || ports.eventCount != 0 || ports.stateApplyCalls != 0 {
		t.Fatalf("facts %+v error %v evaluations %d events %d state %d", result.Supplement, err, len(evaluator.requests),
			ports.eventCount, ports.stateApplyCalls)
	}
}

// A supplement over its own budget fails as it is. A Slot there completes
// with a gap opened on every Plan; a supplement moves no marker, so it has
// no such completion to fall back on.
func TestASupplementBeyondItsBudgetFailsWithoutWriting(t *testing.T) {
	header := supplementHeader(t)
	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	coordinator := supplementCoordinator(t, ports, evaluator, ports, 1)
	batches, completion := supplementRead(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80", calmSeries: "90"})
	ports.executeOverride = streamExecution(header, batches, completion)
	result, err := coordinator.Execute(context.Background(), supplementRequest(header.Contract, hotSeries, calmSeries))
	if err == nil || ports.eventCount != 0 || ports.stateApplyCalls != 0 || len(ports.gapMutations) != 0 {
		t.Fatalf("result %+v error %v events %d state %d gaps %+v", result, err, ports.eventCount, ports.stateApplyCalls, ports.gapMutations)
	}
}

// The Slot the item's no-data alert was raised at is not supplemented: see
// CheckASupplementLeavesARaisedNoDataAlertStanding. Against a real store,
// so the memory read is the one the worker's own rounds wrote.
func TestASupplementLeavesARaisedNoDataAlertStanding(t *testing.T) {
	address := startG3ARedis(t)
	store, backend := openG3AStateStore(t, address, "supplement-no-data")
	t.Cleanup(func() { _ = backend.Close() })
	worker.CheckASupplementLeavesARaisedNoDataAlertStanding(t, store)
}

// A series that reaches its evaluation and ends with nothing to write - here
// its Plan's marker could not be read, so the evaluation held the Plan - is
// withheld, and nothing is written for it.
func TestASupplementWhoseEvaluationDecidesNothingWithholdsTheSeries(t *testing.T) {
	header := supplementHeader(t)
	ports, evaluator, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"},
		func(ports *recordingPorts) { ports.gapLoadStatus = execution.GapUnavailable }, hotSeries)
	if len(evaluator.requests) != 1 || facts != (execution.SupplementFacts{Candidates: 1, Withheld: 1}) ||
		ports.eventCount != 0 || ports.stateApplyCalls != 0 {
		t.Fatalf("evaluations %d facts %+v events %d state %d", len(evaluator.requests), facts, ports.eventCount, ports.stateApplyCalls)
	}
}

// A Plan that detects no-data has its memory read by the supplement, and a
// series whose group nothing recorded absent is supplemented as any other.
func TestASupplementOfAPlanDetectingNoDataReadsItsMemory(t *testing.T) {
	header := supplementHeaderWith(t, &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"host"}})
	if header.DuePlans[0].CompiledPlan.NoData() == nil {
		t.Fatal("the fixture's Plan does not detect no-data")
	}
	ports, _, facts := runSupplement(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"},
		func(ports *recordingPorts) { ports.gapMissing = true }, hotSeries)
	if facts != (execution.SupplementFacts{Candidates: 1, Admitted: 1, Points: 1}) || ports.eventCount != 1 {
		t.Fatalf("facts %+v events %d, want the series supplemented", facts, ports.eventCount)
	}
}
