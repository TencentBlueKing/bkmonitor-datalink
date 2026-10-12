package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	uq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type codecWorkerSink struct {
	consumer execution.QueryExecutionConsumer
	query    execution.PhysicalQuerySpec
	template execution.NamedInputBinding
}

func (sink codecWorkerSink) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	indices := make([]uint32, batch.Dataset.Len())
	for i := range indices {
		indices[i] = uint32(i)
	}
	view, err := execution.NewDatasetView(batch.Dataset, indices)
	if err != nil {
		return err
	}
	binding := sink.template
	binding.Dataset, binding.View = batch.Dataset, view
	binding.ProviderResult = batch.CompletionRef
	binding.Provenance.PhysicalQuery = sink.query.Digest
	return sink.consumer.ConsumeSeries(ctx, execution.SeriesExecutionBatch{PhysicalQuery: sink.query.Digest,
		QueryRevision: sink.query.PlanFacts.QueryRevision, CompletionRef: batch.CompletionRef,
		Dataset: batch.Dataset, Inputs: []execution.NamedInputBinding{binding}, Delivery: batch.Delivery})
}

func codecWorkerSpec(t *testing.T) execution.PhysicalQuerySpec {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{Provider: execution.ProviderUQ,
		ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList:   []execution.QueryClause{{DataSource: "bkmonitor", TableID: "sample.cpu", FieldName: "usage", ReferenceName: "a", Driver: "influxdb", TimeField: "time", TimeAggregation: execution.QueryFunction{Method: "max", Window: "60s"}}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	window := validInternalExecution().Inputs[0].QueryWindow
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window, ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

const workerSharedHeader = "{\"seq\":0,\"v\":1}\n{\"seq\":1,\"schemas\":[{\"id\":0,\"columns\":[\"_time\",\"_value\"],\"types\":[\"float\",\"float\"],\"group_keys\":[\"host\"]}]}\n"
const workerSharedSeries = "{\"seq\":2,\"series\":[{\"s\":0,\"g\":[\"sample-host\"],\"r\":[[1788000000000,50.1]]}]}\n"
const workerSharedEnd = "{\"seq\":3,\"end\":{\"series\":1,\"points\":1,\"is_partial\":false}}\n"
const workerLegacyBody = `{"series":[{"columns":["_time","_value"],"types":["float","float"],"group_keys":["host"],"group_values":["sample-host"],"values":[[1788000000000,50.1]]}],"is_partial":false}`

func attachCodecWorker(t *testing.T, fixture fixture, client *uq.Client, spec execution.PhysicalQuerySpec) {
	attachCodecWorkerInput(t, fixture, client, spec, validInternalExecution())
}

func attachCodecWorkerInput(t *testing.T, fixture fixture, client *uq.Client, spec execution.PhysicalQuerySpec, input execution.InternalExecution) {
	t.Helper()
	fixture.ports.executeOverride = func(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
		input.Requirements[0].LogicalQueryRef = execution.LogicalQueryRef(spec.PlanFacts.QueryRevision)
		for i := range input.EffectiveTimeFacts {
			input.EffectiveTimeFacts[i].SeriesIdentity = ""
		}
		header := execution.InternalExecutionHeader{ExecutionID: "execution-1", Contract: request.Contract,
			DuePlans: input.DuePlans, Requirements: input.Requirements, EffectiveTimeFacts: input.EffectiveTimeFacts,
			RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: spec.Digest, QueryRevision: spec.PlanFacts.QueryRevision}}, DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
		if err := consumer.Begin(ctx, header); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		done, err := client.Execute(ctx, execution.QueryAttempt{Spec: spec, Slot: request.Contract.Slot, Operation: request.Operation, AttemptNo: 1, DeadlineUnixMilli: header.DeadlineUnixMilli}, codecWorkerSink{consumer: consumer, query: spec, template: input.Inputs[0]})
		if err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		return execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{{Ref: done.Ref, PhysicalQuery: done.PhysicalQuery, QueryRevision: spec.PlanFacts.QueryRevision,
			Completeness: done.Completeness, DataState: done.DataState, Delivery: done.Delivery, RouteFacts: done.RouteFacts, Stats: done.Stats}}}, nil
	}
}

func codecWorkerRequest(t *testing.T, spec execution.PhysicalQuerySpec) execution.SlotExecutionRequest {
	t.Helper()
	input := validInternalExecution()
	input.Requirements[0].LogicalQueryRef = execution.LogicalQueryRef(spec.PlanFacts.QueryRevision)
	digest, err := execution.DeriveDuePlanSetDigest(input.DuePlans, input.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	request := slotRequest(execution.OperationNormal)
	request.Contract.DuePlanSetDigest = digest
	request.DuePlanTargets.DuePlanSetDigest = digest
	return request
}

func TestSharedCodecWorkerNeverFinalizesDamagedOrOverRetainedInput(t *testing.T) {
	for _, sample := range []struct {
		name, body string
		retained   uint64
	}{
		{"missing footer", workerSharedHeader + workerSharedSeries, 1 << 20},
		{"wrong footer counts", workerSharedHeader + workerSharedSeries + strings.Replace(workerSharedEnd, `"points":1`, `"points":2`, 1), 1 << 20},
		{"retained rejected", workerSharedHeader + workerSharedSeries + workerSharedEnd, 100},
	} {
		t.Run(sample.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", uq.SharedSchemaMediaType)
				_, _ = io.WriteString(w, sample.body)
			}))
			defer server.Close()
			client, err := uq.NewClientWithLimits(server.URL, "alarmd", server.Client(), uq.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			fixture := newFixtureWithBudget(t, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: sample.retained, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
			spec := codecWorkerSpec(t)
			attachCodecWorker(t, fixture, client, spec)
			result, err := fixture.coordinator.Execute(context.Background(), codecWorkerRequest(t, spec))
			if err == nil || result.Completed {
				t.Fatalf("bad body committed result=%+v err=%v", result, err)
			}
			if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 || !isZeroProgressCommit(fixture.ports.lastProgress) {
				t.Fatal("failed codec crossed the business commit boundary")
			}
			if sample.name == "retained rejected" {
				found := false
				for _, observation := range *fixture.observations {
					if observation.CapacityBudget == observability.CapacityBudgetRetainedBytes {
						found = true
					}
				}
				if !found {
					t.Fatalf("retained error lost its capacity observation: %v", err)
				}
			}
		})
	}
}

func TestSharedCodecUsesRealEvaluatorAndSameWorkerBusinessResult(t *testing.T) {
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16, Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32, MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	var results []execution.SlotExecutionResult
	for _, shared := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shared {
				w.Header().Set("Content-Type", uq.SharedSchemaMediaType)
				_, _ = io.WriteString(w, workerSharedHeader+workerSharedSeries+workerSharedEnd)
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, workerLegacyBody)
			}
		}))
		defer server.Close()
		client, err := uq.NewClientWithLimits(server.URL, "alarmd", server.Client(), uq.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		fixture := newFixture(t, true, "")
		ports := fixture.ports
		fixture.coordinator, err = worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness, Events: ports, State: ports, Progress: ports, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
		if err != nil {
			t.Fatal(err)
		}
		spec := codecWorkerSpec(t)
		attachCodecWorker(t, fixture, client, spec)
		result, err := fixture.coordinator.Execute(context.Background(), codecWorkerRequest(t, spec))
		if err != nil || !result.Completed {
			t.Fatalf("worker shared=%t result=%+v err=%v", shared, result, err)
		}
		results = append(results, result)
		// Persisted business values may contain received-time evidence, so this
		// check compares verdict/progress behavior, not two real clocks' bytes.
		if fixture.ports.lastProgress.Completion.Kind == "" {
			t.Fatal("worker did not commit completion evidence")
		}
	}
	// Timing and retained resource accounting are not business equivalence.
	left, right := results[0], results[1]
	left.Timing = right.Timing
	left.Usage = right.Usage
	if !reflect.DeepEqual(left, right) {
		a, _ := json.Marshal(left)
		b, _ := json.Marshal(right)
		t.Fatalf("worker verdict differs old=%s shared=%s", a, b)
	}
}

// Eight ordered points go through the actual provider, worker, evaluator and
// finalization: five anomalous points then three trigger misses. This tests a
// fixed historical sequence, not production State persistence across Slots.
func TestSharedCodecWorkerFiveOfFiveAndRecoveryThree(t *testing.T) {
	input := validInternalExecution()
	plan := compiledPlanWithTargetShapedForTest(t, "7", nil, func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.ExecutionSemantics.QueryWindow = 480
		plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":5,"required_anomalies":5,"step_seconds":60}`)
		plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":3}`)
	})
	input.DuePlans[0].CompiledPlan = plan
	input.Requirements[0].RelativeWindow.StartOffsetSeconds = -480
	input.Inputs[0].QueryWindow.Start -= 420
	input.EffectiveTimeFacts[0].Fact = effectiveTimeFactForTest(plan)
	baseSpec := codecWorkerSpec(t)
	baseSpec.Digest = ""
	baseSpec.LogicalWindow, baseSpec.AcceptedRange, baseSpec.ProviderRange = input.Inputs[0].QueryWindow, input.Inputs[0].QueryWindow, input.Inputs[0].QueryWindow
	spec, err := execution.BuildPhysicalQuerySpec(baseSpec)
	if err != nil {
		t.Fatal(err)
	}
	input.Requirements[0].LogicalQueryRef = execution.LogicalQueryRef(spec.PlanFacts.QueryRevision)
	digest, err := execution.DeriveDuePlanSetDigest(input.DuePlans, input.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	request := slotRequest(execution.OperationNormal)
	request.Contract.DuePlanSetDigest, request.DuePlanTargets.DuePlanSetDigest = digest, digest
	rows := make([][]any, 8)
	for index := range rows {
		value := 80
		if index >= 5 {
			value = 10
		}
		rows[index] = []any{int64(1788000000-420+index*60) * 1000, value}
	}
	rowJSON, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"series":[{"columns":["_time","_value"],"types":["float","float"],"group_keys":["host"],"group_values":["sample-host"],"values":` + string(rowJSON) + `}],"is_partial":false}`
	shared := workerSharedHeader + `{"seq":2,"series":[{"s":0,"g":["sample-host"],"r":` + string(rowJSON) + `}]}` + "\n" + strings.Replace(workerSharedEnd, `"points":1`, `"points":8`, 1)
	var mutations []execution.StateApplyRequest
	var events [][]contract.TriggerEventV1
	var progress []execution.ProgressCommitRequest
	for _, body := range []string{legacy, shared} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			media := "application/json"
			if body == shared {
				media = uq.SharedSchemaMediaType
			}
			w.Header().Set("Content-Type", media)
			_, _ = io.WriteString(w, body)
		}))
		defer server.Close()
		client, err := uq.NewClientWithLimits(server.URL, "alarmd", server.Client(), uq.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		detector, err := detect.NewEvaluator(detect.NewDefaultRegistry())
		if err != nil {
			t.Fatal(err)
		}
		evaluator, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16,
			Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16,
				MaxRequiredHistoryPoints: 32, MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		fixture := newFixture(t, true, "")
		ports := fixture.ports
		fixture.coordinator, err = worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports,
			Query: ports, Sequencer: ports, Evaluator: evaluator, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore,
			Hosts: worker.SharedHostBusiness, Events: ports, State: ports, Progress: ports,
			Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}, worker.ProvisionalBudget{MaxSeries: 100,
			MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
		if err != nil {
			t.Fatal(err)
		}
		attachCodecWorkerInput(t, fixture, client, spec, input)
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed {
			t.Fatalf("fixed sequence result=%+v err=%v", result, err)
		}
		mutations = append(mutations, ports.lastStateApply)
		events = append(events, ports.lastEvents)
		progress = append(progress, ports.lastProgress)
		if len(ports.lastEvents) != 2 || ports.lastEvents[0].EventKind != contract.TriggerEventAbnormal || ports.lastEvents[1].EventKind != contract.TriggerEventRecovery {
			t.Fatalf("fixed sequence events=%+v", ports.lastEvents)
		}
	}
	if !reflect.DeepEqual(mutations[0], mutations[1]) || !reflect.DeepEqual(events[0], events[1]) || !reflect.DeepEqual(progress[0], progress[1]) {
		t.Fatal("codec changed fixed-sequence State, stable TriggerEvents or Progress")
	}
}
