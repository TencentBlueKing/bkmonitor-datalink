package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type noDataSampleRound struct {
	result    execution.EvaluationResult
	state     execution.StatePreflightResult
	memory    execution.NoDataLoadResult
	mutations []execution.PlanNoDataMutation
	outcomes  []nodata.SlotOutcome
}

// CheckSeriesSampleNoDataWorkerRegression is test-only access to the worker's
// synthetic-series path. The external test supplies its existing real Redis
// fixture: neither history nor NoData memory is reconstructed by the test.
// Outputs here are provisional events, not Kafka acknowledgements.
func CheckSeriesSampleNoDataWorkerRegression(t *testing.T, off, on *state.ExecutionStore) {
	t.Helper()
	due := noDataWiredPlan(t)
	due.CompiledPlan = noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{Continuous: 3, Level: 2})
	sampler, err := observability.NewSeriesSampler(observability.SeriesSampleLimits{
		RecordsPerMinute: 8, BytesPerMinute: 8 * observability.SeriesSampleMaxBytes, QueueCapacity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := sampler.Select([]observability.SeriesSampleSelection{{
		QueryGroup: "query-group", WindowID: "no-data-window", OpenedAt: now, ExpiresAt: now.Add(time.Minute),
		TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, StrategyID: due.Identity.StrategyID,
		StateGeneration: string(due.StateGeneration), PlanScheduleRevision: string(due.ScheduleRevision),
		SeriesKind: string(execution.SeriesKindNoData),
	}}); err != nil {
		t.Fatal(err)
	}
	baseline := runNoDataSampleRounds(t, due, off, nil)
	sampled := runNoDataSampleRounds(t, due, on, sampler)
	for index := range baseline {
		if !reflect.DeepEqual(baseline[index], sampled[index]) {
			t.Fatalf("round %d sampling changed worker output, state or NoData memory\noff=%+v\non=%+v", index+1, baseline[index], sampled[index])
		}
	}
}

func runNoDataSampleRounds(t *testing.T, due execution.DuePlan, store *state.ExecutionStore, sampler *observability.SeriesSampler) []noDataSampleRound {
	t.Helper()
	ctx := context.Background()
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16,
		Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16,
			MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
			MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator.SetSeriesSampler(sampler)
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := execution.DeriveStateRetentionRequirement(view.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	var rounds []noDataSampleRound
	var stateItems []execution.StatePreflightItem
	var openedIdentity string
	// A,U,A,A is not three consecutive absent periods; only A,U,A,A,A
	// triggers. One present period then recovers through the same evaluator.
	for index, input := range []string{"absent", "unavailable", "absent", "absent", "absent", "present"} {
		stream := noDataWiredStream(t, due, store)
		stream.header.Contract.Slot.EvaluationTime += execution.EvaluationTime(index * 60)
		stream.header.ExecutionID = "sampling-regression"
		stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationNormal}
		stream.coordinator.budget.MaxStateMutations, stream.coordinator.budget.MaxEvents = 100, 100
		stream.coordinator.ports.State, stream.coordinator.ports.Evaluator = store, evaluator
		stream.coordinator.ports.Observer = observability.ObserverFunc(func(context.Context, observability.Observation) {})
		stream.effective = mustPrepareAlwaysEffectiveTimeFacts(t, stream.header)
		stream.gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
			Identity: execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}, Status: execution.GapMissing,
		}}}
		completeness := execution.CompletenessFull
		if input == "unavailable" {
			completeness = execution.CompletenessUnavailable
		}
		stream.bindings = []execution.NamedInputBinding{{Consumer: execution.ConsumerRef{Plan: due.Identity}, Completeness: completeness}}
		if err := stream.loadNoDataMemory(ctx); err != nil {
			t.Fatal(err)
		}
		var prepared []preparedSeries
		if input == "present" {
			dataset := execution.NewDataset([]contract.CanonicalRecordV2{{Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"returned"`)}}})
			dataView, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			prepared = []preparedSeries{{due: due, inputs: []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{{Role: execution.InputRolePrimary, View: dataView}}}}}}
		}
		if err := stream.evaluateNoData(ctx, prepared, 4); err != nil {
			t.Fatalf("round %d: %v", index+1, err)
		}
		round := noDataSampleRound{result: stream.evaluated, mutations: stream.noDataMutations, outcomes: stream.noDataOutcomes}
		var events []contract.TriggerEventV1
		var mutations []execution.StateMutation
		for _, plan := range round.result.Plans {
			for _, result := range plan.StateResults {
				events = append(events, result.Events...)
				mutations = append(mutations, result.Mutation)
			}
		}
		if index < 4 && len(events) != 0 {
			t.Fatalf("round %d bridged an unavailable period: %+v", index+1, events)
		}
		if index >= 4 {
			want := contract.TriggerEventAbnormal
			if input == "present" {
				want = contract.TriggerEventRecovery
			}
			if len(events) != 1 || events[0].EventKind != want {
				t.Fatalf("round %d events=%+v, want %s", index+1, events, want)
			}
			if input == "present" && events[0].RecordRef.DimensionIdentityDigest != openedIdentity {
				t.Fatal("recovery changed the open alert's series identity")
			}
			openedIdentity = events[0].RecordRef.DimensionIdentityDigest
		}
		if len(mutations) > 0 {
			applied, err := store.ApplyRuntime(ctx, execution.StateApplyRequest{Contract: stream.header.Contract, Items: mutations, Retention: retention})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range applied.Items {
				if item.Status != execution.StateApplied {
					t.Fatalf("state not stored: %+v", item)
				}
			}
		}
		if err := stream.coordinator.applyNoDataMemory(ctx, stream.request, stream.noDataMutations); err != nil {
			t.Fatal(err)
		}
		if len(stream.stateItems) > 0 {
			stateItems = stream.stateItems
		}
		version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
		if err != nil {
			t.Fatal(err)
		}
		for i := range stateItems {
			stateItems[i].ApplyVersion = version
		}
		round.state, err = store.LoadRuntime(ctx, execution.StatePreflightRequest{Contract: stream.header.Contract, Items: stateItems})
		if err != nil {
			t.Fatal(err)
		}
		memoryItems, err := noDataPreflightForHeader(stream.header)
		if err != nil {
			t.Fatal(err)
		}
		round.memory, err = store.LoadNoData(ctx, execution.NoDataLoadRequest{Contract: stream.header.Contract, Items: memoryItems})
		if err != nil {
			t.Fatal(err)
		}
		if input == "unavailable" {
			if len(mutations) != 0 || len(round.mutations) != 0 || len(round.outcomes) != 1 || round.outcomes[0] != nodata.OutcomeSkippedQueryNotFull {
				t.Fatalf("unavailable advanced NoData: %+v", round)
			}
			// Renewals describe this read's maintenance, not stored memory.
			if !reflect.DeepEqual(round.memory.Items, rounds[index-1].memory.Items) || !reflect.DeepEqual(round.state.Items[0].History, rounds[index-1].state.Items[0].History) {
				t.Fatalf("unavailable changed stored memory or trigger history: before=%+v after=%+v; before history=%+v after=%+v", rounds[index-1].memory, round.memory, rounds[index-1].state.Items[0].History, round.state.Items[0].History)
			}
		}
		if sampler != nil {
			checkNoDataWorkerSample(t, sampler, input, index, round)
		}
		rounds = append(rounds, round)
		stream.releaseProvisional()
	}
	return rounds
}

func checkNoDataWorkerSample(t *testing.T, sampler *observability.SeriesSampler, input string, index int, round noDataSampleRound) {
	t.Helper()
	select {
	case record := <-sampler.Records():
		defer record.Release()
		if input == "unavailable" {
			t.Fatal("unavailable round invented a sampled decision")
		}
		var sample observability.SeriesSample
		if err := json.Unmarshal(record.Bytes(), &sample); err != nil {
			t.Fatal(err)
		}
		if sample.SeriesKind != string(execution.SeriesKindNoData) || !sample.Provisional || len(sample.Levels) != 1 || sample.Levels[0].LevelID != 2 {
			t.Fatalf("sample used ordinary Plan levels: %+v", sample)
		}
		level := sample.Levels[0]
		outcome := round.result.Plans[0].LevelOutcomes[0]
		if level.Outcome != string(outcome.Outcome) || level.Reason != string(outcome.ReasonCode) || level.TriggerWindow != 3 || level.TriggerRequired != 3 {
			t.Fatalf("sample disagrees with final NoData decision: %+v / %+v", level, outcome)
		}
		if index >= 4 && (sample.EventID == "" || sample.RecoveryHeld || level.TriggerObserved == nil) {
			t.Fatalf("sample lost trigger/recovery facts: %+v", sample)
		}
	default:
		if input != "unavailable" {
			t.Fatalf("round %d sample missing: %+v", index+1, sampler.Health())
		}
	}
}
